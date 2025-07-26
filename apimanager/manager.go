package apimanager

import (
	"errors"
	"fmt"
	"math/rand"
	"openrouter_polling/storage"
	"openrouter_polling/utils"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	ErrKeyAlreadyExists = errors.New("API key already exists in the manager")
	ErrKeyNotFound      = errors.New("API key not found in the manager")
	ErrInvalidKeyFormat = errors.New("invalid API key format or weight specification")
)

type ApiKeyManager struct {
	keysStatus []*ApiKeyStatus
	keyStore   *storage.KeyStore
	lock       sync.Mutex
	randSource *rand.Rand
	log        *logrus.Logger
}

type BatchAddResult struct {
	AddedCount     int      `json:"added_count"`
	DuplicateCount int      `json:"duplicate_count"`
	InvalidCount   int      `json:"invalid_count"`
	ErrorMessages  []string `json:"error_messages"`
}

// 【新增】用于分页响应的结构体
type PaginatedKeyStatus struct {
	Keys      []ApiKeyStatusSafe `json:"keys"`
	TotalKeys int64              `json:"total_keys"`
	Page      int                `json:"page"`
	Limit     int                `json:"limit"`
	TotalPages int               `json:"total_pages"`
}


func NewApiKeyManager(logger *logrus.Logger, keyStore *storage.KeyStore) *ApiKeyManager {
	if Log == nil && logger != nil {
		Log = logger
	}
	return &ApiKeyManager{
		keysStatus: make([]*ApiKeyStatus, 0),
		keyStore:   keyStore,
		randSource: rand.New(rand.NewSource(time.Now().UnixNano())),
		log:        logger,
	}
}

func (m *ApiKeyManager) LoadKeysFromDB() error {
	m.lock.Lock()
	defer m.lock.Unlock()

	dbKeys, err := m.keyStore.GetAllKeys()
	if err != nil {
		m.log.Errorf("从数据库加载密钥失败: %v", err)
		return err
	}

	m.keysStatus = make([]*ApiKeyStatus, len(dbKeys))
	for i, dbKey := range dbKeys {
		m.keysStatus[i] = NewApiKeyStatusFromModel(dbKey)
	}

	m.log.Infof("成功从数据库加载了 %d 个密钥到内存缓存。", len(m.keysStatus))
	return nil
}

func (m *ApiKeyManager) SeedKeysFromConfig(openrouterKeysConfigStr string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	dbKeys, err := m.keyStore.GetAllKeys()
	if err != nil {
		return fmt.Errorf("无法在植入前检查数据库: %w", err)
	}

	if len(dbKeys) > 0 {
		m.log.Info("数据库中已存在密钥，跳过从环境变量植入。")
		return nil
	}

	if openrouterKeysConfigStr == "" {
		m.log.Info("OPENROUTER_API_KEYS 为空，没有要植入的密钥。")
		return nil
	}

	m.log.Info("数据库为空，正在从 OPENROUTER_API_KEYS 环境变量植入初始密钥...")
	keyEntries := strings.Split(openrouterKeysConfigStr, ",")
	seededCount := 0
	for _, entry := range keyEntries {
		key, weight, err := m.parseKeyAndWeight(entry)
		if err != nil {
			m.log.Warnf("跳过无效的密钥条目 '%s': %v", entry, err)
			continue
		}

		newDbKey := &storage.APIKey{
			Key:      key,
			Weight:   weight,
			IsActive: true,
		}

		if err := m.keyStore.AddKey(newDbKey); err != nil {
			if errors.Is(err, storage.ErrKeyAlreadyExists) {
				m.log.Warnf("尝试植入已存在的密钥 %s，跳过。", utils.SafeSuffix(key))
			} else {
				m.log.Errorf("植入密钥 %s 到数据库失败: %v", utils.SafeSuffix(key), err)
			}
			continue
		}
		seededCount++
	}

	m.log.Infof("成功植入了 %d 个密钥到数据库。", seededCount)
	return nil
}

func (m *ApiKeyManager) parseKeyAndWeight(entry string) (keyStr string, weight int, err error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return "", 0, ErrInvalidKeyFormat
	}

	keyStr = entry
	weight = 1

	if strings.Contains(entry, ":") {
		parts := strings.SplitN(entry, ":", 2)
		keyStr = strings.TrimSpace(parts[0])
		if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
			parsedWeight, parseErr := strconv.Atoi(strings.TrimSpace(parts[1]))
			if parseErr != nil || parsedWeight < 1 {
				if m.log != nil {
					m.log.Warnf("密钥 %s 的权重 '%s' 格式无效或小于1，将使用默认权重 1。", utils.SafeSuffix(keyStr), parts[1])
				}
			} else {
				weight = parsedWeight
			}
		}
	}

	if keyStr == "" {
		return "", 0, ErrInvalidKeyFormat
	}
	return keyStr, weight, nil
}

func (m *ApiKeyManager) AddKeysBatch(keyData string) (BatchAddResult, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	result := BatchAddResult{ErrorMessages: make([]string, 0)}
	normalizedData := strings.ReplaceAll(keyData, "\n", ",")
	keyEntries := strings.Split(normalizedData, ",")

	// 1. 解析所有条目并对输入进行去重，保留最后出现的权重。
	uniqueKeysFromInput := make(map[string]int)
	validParsedEntries := 0
	for _, entry := range keyEntries {
		trimmedEntry := strings.TrimSpace(entry)
		if trimmedEntry == "" {
			continue
		}

		keyStr, weight, err := m.parseKeyAndWeight(trimmedEntry)
		if err != nil {
			result.InvalidCount++
			errMsg := fmt.Sprintf("无效条目 '%s': %v", trimmedEntry, err)
			result.ErrorMessages = append(result.ErrorMessages, errMsg)
			continue
		}
		validParsedEntries++
		uniqueKeysFromInput[keyStr] = weight
	}

	// 计算来自输入列表本身的重复项
	duplicatesInInput := validParsedEntries - len(uniqueKeysFromInput)
	result.DuplicateCount += duplicatesInInput

	if len(uniqueKeysFromInput) == 0 {
		m.log.Infof("批量添加密钥操作完成。新增: 0, 重复: %d, 无效: %d。", result.DuplicateCount, result.InvalidCount)
		return result, nil
	}

	// 2. 从管理器的缓存中获取所有现有密钥字符串以进行快速查找。
	existingKeysInDB := make(map[string]bool)
	for _, ks := range m.keysStatus {
		existingKeysInDB[ks.Key] = true
	}

	// 3. 识别哪些密钥是新的，哪些是数据库中的重复项。
	keysToCreate := make([]*storage.APIKey, 0)
	for keyStr, weight := range uniqueKeysFromInput {
		if existingKeysInDB[keyStr] {
			result.DuplicateCount++ // 这个密钥已经存在于数据库中。
		} else {
			newDbKey := &storage.APIKey{
				Key:      keyStr,
				Weight:   weight,
				IsActive: true,
			}
			keysToCreate = append(keysToCreate, newDbKey)
		}
	}

	// 4. 将新密钥批量插入数据库。
	if len(keysToCreate) > 0 {
		if err := m.keyStore.AddKeysInBatch(keysToCreate); err != nil {
			m.log.Errorf("批量添加密钥到数据库失败: %v", err)
			result.InvalidCount += len(keysToCreate)
			result.ErrorMessages = append(result.ErrorMessages, fmt.Sprintf("数据库批量插入错误: %v", err))
		} else {
			// 5. 更新内存状态缓存。
			// GORM 的 CreateInBatches 会用数据库中的值（如 ID, CreatedAt）更新原始切片。
			for _, newDbKey := range keysToCreate {
				newKeyStatus := NewApiKeyStatusFromModel(newDbKey)
				m.keysStatus = append(m.keysStatus, newKeyStatus)
			}
			result.AddedCount = len(keysToCreate)
		}
	}

	m.log.Infof("批量添加密钥操作完成。新增: %d, 重复: %d, 无效: %d。当前总密钥数: %d",
		result.AddedCount, result.DuplicateCount, result.InvalidCount, len(m.keysStatus))
	return result, nil
}

func (m *ApiKeyManager) DeleteKeyBySuffix(suffix string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	foundIndex := -1
	var keyToDelete string

	for i, ks := range m.keysStatus {
		if utils.SafeSuffix(ks.Key) == suffix {
			foundIndex = i
			keyToDelete = ks.Key
			break
		}
	}

	if foundIndex == -1 {
		return ErrKeyNotFound
	}

	if err := m.keyStore.DeleteKeyByKeyString(keyToDelete); err != nil {
		m.log.Errorf("从数据库删除密钥 %s 失败: %v", utils.SafeSuffix(keyToDelete), err)
		return err
	}

	m.keysStatus = append(m.keysStatus[:foundIndex], m.keysStatus[foundIndex+1:]...)
	m.log.Infof("成功删除密钥 %s (后缀: %s)。当前总密钥数: %d", utils.SafeSuffix(keyToDelete), suffix, len(m.keysStatus))
	return nil
}

// DeleteKeysBySuffixBatch 【新增】根据后缀批量删除密钥
func (m *ApiKeyManager) DeleteKeysBySuffixBatch(suffixes []string) (int64, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	if len(suffixes) == 0 {
		return 0, nil
	}

	keysToDelete := make([]string, 0, len(suffixes))
	suffixToKeyMap := make(map[string]string)
	for _, ks := range m.keysStatus {
		suffixToKeyMap[utils.SafeSuffix(ks.Key)] = ks.Key
	}

	for _, suffix := range suffixes {
		if key, ok := suffixToKeyMap[suffix]; ok {
			keysToDelete = append(keysToDelete, key)
		}
	}

	if len(keysToDelete) == 0 {
		return 0, nil
	}

	deletedCount, err := m.keyStore.DeleteKeysByKeysInBatch(keysToDelete)
	if err != nil {
		m.log.Errorf("从数据库批量删除密钥失败: %v", err)
		return 0, err
	}

	// 更新内存缓存
	newKeyStatus := make([]*ApiKeyStatus, 0, len(m.keysStatus)-int(deletedCount))
	keysToDeleteSet := make(map[string]bool)
	for _, key := range keysToDelete {
		keysToDeleteSet[key] = true
	}

	for _, ks := range m.keysStatus {
		if !keysToDeleteSet[ks.Key] {
			newKeyStatus = append(newKeyStatus, ks)
		}
	}
	m.keysStatus = newKeyStatus

	m.log.Infof("成功批量删除 %d 个密钥。当前总密钥数: %d", deletedCount, len(m.keysStatus))
	return deletedCount, nil
}


func (m *ApiKeyManager) GetNextAPIKey() *ApiKeyStatus {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.checkAndReactivateKeysInternal()

	eligibleKeys := make([]*ApiKeyStatus, 0)
	totalWeight := 0

	for _, ks := range m.keysStatus {
		if ks.CanUse() {
			eligibleKeys = append(eligibleKeys, ks)
			totalWeight += ks.Weight
		}
	}

	if len(eligibleKeys) == 0 {
		return nil
	}

	if totalWeight <= 0 {
		return eligibleKeys[m.randSource.Intn(len(eligibleKeys))]
	}

	randomNum := m.randSource.Intn(totalWeight)
	currentWeightSum := 0
	for _, ks := range eligibleKeys {
		currentWeightSum += ks.Weight
		if randomNum < currentWeightSum {
			ks.UpdateLastUsed()
			go func(key string) {
				if err := m.keyStore.UpdateLastUsedTime(key); err != nil {
					m.log.Errorf("更新密钥 %s 的 LastUsedTime 失败: %v", utils.SafeSuffix(key), err)
				}
			}(ks.Key)
			return ks
		}
	}
	
	if len(eligibleKeys) > 0 {
		selectedKey := eligibleKeys[m.randSource.Intn(len(eligibleKeys))]
		selectedKey.UpdateLastUsed()
		go func(key string) {
			if err := m.keyStore.UpdateLastUsedTime(key); err != nil {
				m.log.Errorf("更新密钥 %s 的 LastUsedTime 失败: %v", utils.SafeSuffix(key), err)
			}
		}(selectedKey.Key)
		return selectedKey
	}

	return nil
}

func (m *ApiKeyManager) MarkKeyFailure(keyString string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	for _, ks := range m.keysStatus {
		if ks.Key == keyString {
			cooldownDuration := ks.RecordFailure()
			err := m.keyStore.RecordFailure(ks.Key, ks.FailureCount, cooldownDuration)
			if err != nil {
				m.log.Errorf("持久化密钥 %s 的失败状态到数据库失败: %v", utils.SafeSuffix(ks.Key), err)
			}
			break
		}
	}
}

func (m *ApiKeyManager) RecordKeySuccess(keyString string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	for _, ks := range m.keysStatus {
		if ks.Key == keyString {
			ks.RecordSuccessOrReactivate()
			err := m.keyStore.RecordSuccessOrReactivate(ks.Key)
			if err != nil {
				m.log.Errorf("持久化密钥 %s 的成功状态到数据库失败: %v", utils.SafeSuffix(ks.Key), err)
			}
			break
		}
	}
}

func (m *ApiKeyManager) checkAndReactivateKeysInternal() {
	now := time.Now()
	for _, ks := range m.keysStatus {
		if !ks.IsActive && ks.CoolDownUntil != nil && now.After(*ks.CoolDownUntil) {
			m.log.Infof("密钥 %s 冷却期已过，正在重新激活...", utils.SafeSuffix(ks.Key))
			ks.RecordSuccessOrReactivate()
			err := m.keyStore.RecordSuccessOrReactivate(ks.Key)
			if err != nil {
				m.log.Errorf("持久化密钥 %s 的重新激活状态失败: %v", utils.SafeSuffix(ks.Key), err)
				// Revert in-memory change if DB update fails
				ks.IsActive = false
			}
		}
	}
}

// GetAllKeyStatusesSafePaginated 【修改】获取分页的密钥状态
func (m *ApiKeyManager) GetAllKeyStatusesSafePaginated(page, limit int) (*PaginatedKeyStatus, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.checkAndReactivateKeysInternal()

	// 因为状态（如冷却）是动态的，我们从内存中获取所有状态，然后进行分页。
	// 对于非常大的密钥集，这可能需要优化为直接在数据库中查询，但这会使动态状态处理复杂化。
	// 当前方法对于数千个密钥是可行的。
	allStatuses := make([]ApiKeyStatusSafe, len(m.keysStatus))
	for i, ks := range m.keysStatus {
		allStatuses[i] = ks.ToSafe()
	}

	totalKeys := int64(len(allStatuses))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 10 // 默认每页大小
	}

	start := (page - 1) * limit
	end := start + limit

	if start >= len(allStatuses) {
		return &PaginatedKeyStatus{
			Keys:      []ApiKeyStatusSafe{},
			TotalKeys: totalKeys,
			Page:      page,
			Limit:     limit,
			TotalPages: int((totalKeys + int64(limit) - 1) / int64(limit)),
		}, nil
	}

	if end > len(allStatuses) {
		end = len(allStatuses)
	}

	paginatedKeys := allStatuses[start:end]

	return &PaginatedKeyStatus{
		Keys:      paginatedKeys,
		TotalKeys: totalKeys,
		Page:      page,
		Limit:     limit,
		TotalPages: int((totalKeys + int64(limit) - 1) / int64(limit)),
	}, nil
}


func (m *ApiKeyManager) GetKeyStatusByKeyStr(keyStr string) *ApiKeyStatus {
	m.lock.Lock()
	defer m.lock.Unlock()

	for _, ks := range m.keysStatus {
		if ks.Key == keyStr {
			return ks
		}
	}
	return nil
}

func (m *ApiKeyManager) GetTotalKeysCount() int {
	m.lock.Lock()
	defer m.lock.Unlock()
	return len(m.keysStatus)
}

// GetCachedKeys 返回内存中密钥状态的副本，供健康检查等内部服务使用。
func (m *ApiKeyManager) GetCachedKeys() []*ApiKeyStatus {
	m.lock.Lock()
	defer m.lock.Unlock()

	// 创建并返回一个副本，以避免外部修改影响内部状态
	copiedStatuses := make([]*ApiKeyStatus, len(m.keysStatus))
	for i, ks := range m.keysStatus {
		// 复制指针指向的对象，以确保外部修改不会影响原始对象
		clone := *ks
		copiedStatuses[i] = &clone
	}
	return copiedStatuses
}

// ReloadKeysFromString 是一个破坏性操作，它会清空所有现有密钥，
// 然后从提供的字符串中加载新的密钥。
func (m *ApiKeyManager) ReloadKeysFromString(keyData string) (BatchAddResult, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.log.Warn("正在执行破坏性操作：重新加载所有密钥。现有密钥将被永久删除。")

	// 1. 清空数据库
	if err := m.keyStore.DeleteAllKeys(); err != nil {
		m.log.Errorf("重新加载时清空数据库失败: %v", err)
		return BatchAddResult{}, fmt.Errorf("清空数据库失败: %w", err)
	}

	// 2. 清空内存缓存
	m.keysStatus = make([]*ApiKeyStatus, 0)
	m.log.Info("数据库和内存缓存已清空。")

	// 3. 添加新密钥 (逻辑与 AddKeysBatch 类似)
	result := BatchAddResult{ErrorMessages: make([]string, 0)}
	normalizedData := strings.ReplaceAll(keyData, "\n", ",")
	keyEntries := strings.Split(normalizedData, ",")

	for _, entry := range keyEntries {
		trimmedEntry := strings.TrimSpace(entry)
		if trimmedEntry == "" {
			continue
		}

		keyStr, weight, err := m.parseKeyAndWeight(trimmedEntry)
		if err != nil {
			result.InvalidCount++
			errMsg := fmt.Sprintf("无效条目 '%s': %v", trimmedEntry, err)
			result.ErrorMessages = append(result.ErrorMessages, errMsg)
			continue
		}

		newDbKey := &storage.APIKey{Key: keyStr, Weight: weight, IsActive: true}
		err = m.keyStore.AddKey(newDbKey)
		if err != nil {
			// 在重新加载操作中，不应该有重复项，因为我们刚刚清空了表。
			// 任何错误都可能是数据库问题。
			result.InvalidCount++
			result.ErrorMessages = append(result.ErrorMessages, fmt.Sprintf("数据库错误: %v", err))
			continue
		}

		newKeyStatus := NewApiKeyStatusFromModel(newDbKey)
		m.keysStatus = append(m.keysStatus, newKeyStatus)
		result.AddedCount++
	}

	m.log.Infof("重新加载密钥操作完成。新增: %d, 无效: %d。当前总密钥数: %d",
		result.AddedCount, result.InvalidCount, len(m.keysStatus))
	return result, nil
}