package apimanager

import (
	"errors"
	"fmt"
	"math/rand"
	"openrouter_polling/config"
	"openrouter_polling/utils"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// 定义一些包级别的错误，方便调用者进行类型检查。
var (
	ErrKeyAlreadyExists = errors.New("API key already exists in the manager")          // 尝试添加已存在的密钥时返回
	ErrKeyNotFound      = errors.New("API key not found in the manager")               // 尝试操作不存在的密钥时返回
	ErrInvalidKeyFormat = errors.New("invalid API key format or weight specification") // 密钥条目格式不正确时返回
)

// ApiKeyManager 结构体负责管理一组 API 密钥的状态。
type ApiKeyManager struct {
	keysStatus []*ApiKeyStatus
	lock       sync.Mutex
	randSource *rand.Rand
	log        *logrus.Logger
}

// 【新增】BatchAddResult 结构体用于报告批量添加操作的结果。
type BatchAddResult struct {
	AddedCount     int      `json:"added_count"`
	DuplicateCount int      `json:"duplicate_count"`
	InvalidCount   int      `json:"invalid_count"`
	ErrorMessages  []string `json:"error_messages"`
}

// NewApiKeyManager 创建并返回一个新的 ApiKeyManager 实例。
func NewApiKeyManager(logger *logrus.Logger) *ApiKeyManager {
	if Log == nil && logger != nil {
		Log = logger
	}
	return &ApiKeyManager{
		keysStatus: make([]*ApiKeyStatus, 0),
		randSource: rand.New(rand.NewSource(time.Now().UnixNano())),
		log:        logger,
	}
}

// parseKeyAndWeight 是一个内部辅助函数，用于解析单个密钥条目字符串。
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

// LoadKeys 从配置字符串加载或更新密钥池。
func (m *ApiKeyManager) LoadKeys(openrouterKeysConfigStr string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.keysStatus = make([]*ApiKeyStatus, 0)

	if openrouterKeysConfigStr == "" {
		if m.log != nil {
			m.log.Warn("OPENROUTER_API_KEYS 配置为空。没有密钥加载到管理器。")
		}
		return
	}

	keyEntries := strings.Split(openrouterKeysConfigStr, ",")
	parsedCount := 0
	uniqueKeys := make(map[string]bool)

	for _, entry := range keyEntries {
		key, weight, err := m.parseKeyAndWeight(entry)
		if err != nil {
			if m.log != nil {
				m.log.Warnf("跳过无效的密钥条目 '%s': %v", entry, err)
			}
			continue
		}

		if _, exists := uniqueKeys[key]; exists {
			if m.log != nil {
				m.log.Warnf("在加载配置中发现重复密钥 %s，将跳过后续出现。", utils.SafeSuffix(key))
			}
			continue
		}
		uniqueKeys[key] = true

		m.keysStatus = append(m.keysStatus, &ApiKeyStatus{
			Key:      key,
			Weight:   weight,
			IsActive: true,
		})
		parsedCount++
	}

	if m.log != nil {
		m.log.Infof("成功从配置加载/更新了 %d 个 OpenRouter API 密钥到管理器。", parsedCount)
	}
}

// 【修改/新增】AddKeysBatch 向管理器中添加一个或多个新的 API 密钥。
func (m *ApiKeyManager) AddKeysBatch(keyData string) (BatchAddResult, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	result := BatchAddResult{
		ErrorMessages: make([]string, 0),
	}

	normalizedData := strings.ReplaceAll(keyData, "\n", ",")
	keyEntries := strings.Split(normalizedData, ",")

	existingKeys := make(map[string]bool)
	for _, ks := range m.keysStatus {
		existingKeys[ks.Key] = true
	}

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
			if m.log != nil {
				m.log.Warnf("AddKeysBatch: %s", errMsg)
			}
			continue
		}

		if _, exists := existingKeys[keyStr]; exists {
			result.DuplicateCount++
			if m.log != nil {
				m.log.Warnf("AddKeysBatch: 尝试添加已存在的密钥 %s，跳过。", utils.SafeSuffix(keyStr))
			}
			continue
		}

		newKeyStatus := &ApiKeyStatus{
			Key:      keyStr,
			Weight:   weight,
			IsActive: true,
		}
		m.keysStatus = append(m.keysStatus, newKeyStatus)
		existingKeys[keyStr] = true
		result.AddedCount++
	}

	if m.log != nil && (result.AddedCount > 0 || result.DuplicateCount > 0 || result.InvalidCount > 0) {
		m.log.Infof("批量添加密钥操作完成。新增: %d, 重复: %d, 无效: %d。当前总密钥数: %d",
			result.AddedCount, result.DuplicateCount, result.InvalidCount, len(m.keysStatus))
	}
	return result, nil
}

// DeleteKeyBySuffix 根据密钥的后缀从管理器中删除一个 API 密钥。
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
		if m.log != nil {
			m.log.Warnf("尝试删除密钥失败：未找到后缀为 '%s' 的密钥。", suffix)
		}
		return ErrKeyNotFound
	}

	m.keysStatus = append(m.keysStatus[:foundIndex], m.keysStatus[foundIndex+1:]...)

	if m.log != nil {
		m.log.Infof("成功删除密钥 %s (后缀: %s)。当前总密钥数: %d", utils.SafeSuffix(keyToDelete), suffix, len(m.keysStatus))
	}
	return nil
}

// GetNextAPIKey 根据加权随机算法选择下一个可用的 API 密钥。
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
		if m.log != nil {
			m.log.Warn("ApiKeyManager: 当前没有活动的或未在冷却期内的 API 密钥可供选择。")
		}
		return nil
	}

	if totalWeight <= 0 {
		if m.log != nil {
			m.log.Warnf("ApiKeyManager: 可用密钥的总权重为 %d (不应小于1)。将从 %d 个可用密钥中随机选择一个。", totalWeight, len(eligibleKeys))
		}
		return eligibleKeys[m.randSource.Intn(len(eligibleKeys))]
	}

	randomNum := m.randSource.Intn(totalWeight)
	currentWeightSum := 0
	var selectedKeyStatus *ApiKeyStatus = nil

	for _, ks := range eligibleKeys {
		currentWeightSum += ks.Weight
		if randomNum < currentWeightSum {
			selectedKeyStatus = ks
			break
		}
	}

	if selectedKeyStatus == nil && len(eligibleKeys) > 0 {
		if m.log != nil {
			m.log.Error("ApiKeyManager: 加权随机选择未能选出密钥（不应发生），将随机选择一个可用密钥。")
		}
		selectedKeyStatus = eligibleKeys[m.randSource.Intn(len(eligibleKeys))]
	}

	if selectedKeyStatus != nil {
		selectedKeyStatus.UpdateLastUsed()
		if m.log != nil {
			m.log.Debugf("ApiKeyManager: 选定密钥 %s (权重 %d) 用于请求。", utils.SafeSuffix(selectedKeyStatus.Key), selectedKeyStatus.Weight)
		}
	}
	return selectedKeyStatus
}

// MarkKeyFailure 标记指定的密钥字符串对应的密钥发生了一次失败。
func (m *ApiKeyManager) MarkKeyFailure(keyString string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	for _, ks := range m.keysStatus {
		if ks.Key == keyString {
			ks.RecordFailure()
			break
		}
	}
}

// RecordKeySuccess 记录指定的密钥字符串对应的密钥成功使用或被重新激活。
func (m *ApiKeyManager) RecordKeySuccess(keyString string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	for _, ks := range m.keysStatus {
		if ks.Key == keyString {
			ks.RecordSuccessOrReactivate()
			break
		}
	}
}

// checkAndReactivateKeysInternal 是一个内部方法，用于遍历所有密钥。
func (m *ApiKeyManager) checkAndReactivateKeysInternal() {
	now := time.Now()
	reactivatedCount := 0
	for _, ks := range m.keysStatus {
		if !ks.IsActive && ks.CoolDownUntil != nil && now.After(*ks.CoolDownUntil) {
			if m.log != nil {
				m.log.Infof("密钥 %s 冷却期已过 (冷却至: %s)，尝试重新激活。",
					utils.SafeSuffix(ks.Key), ks.CoolDownUntil.Format(time.RFC3339))
			}
			ks.RecordSuccessOrReactivate()
			reactivatedCount++
		}
	}
	if reactivatedCount > 0 && m.log != nil {
		m.log.Debugf("内部检查：重新激活了 %d 个冷却期已过的密钥。", reactivatedCount)
	}
}

// GetAllKeyStatusesSafe 获取所有当前管理的 API 密钥的安全状态列表。
func (m *ApiKeyManager) GetAllKeyStatusesSafe() []ApiKeyStatusSafe {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.checkAndReactivateKeysInternal()

	safeStatuses := make([]ApiKeyStatusSafe, len(m.keysStatus))
	for i, ks := range m.keysStatus {
		safeStatuses[i] = ks.ToSafe()
	}
	return safeStatuses
}

// GetKeyStatusByKeyStr 根据完整的密钥字符串获取其详细状态。
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

// GetKeysForHealthCheck 返回一个需要进行健康检查的密钥字符串切片。
func (m *ApiKeyManager) GetKeysForHealthCheck() []string {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.checkAndReactivateKeysInternal()

	var keyStringsToCheck []string
	now := time.Now()
	for _, ksInternal := range m.keysStatus {
		isCandidate := !ksInternal.IsActive || ksInternal.FailureCount > 0
		if !isCandidate {
			continue
		}

		shouldCheck := ksInternal.CanUse()
		if !shouldCheck && ksInternal.CoolDownUntil != nil {
			threshold := config.AppSettings.KeyFailureCooldown / 5
			if threshold > 1*time.Minute {
				threshold = 1 * time.Minute
			}
			if now.After(ksInternal.CoolDownUntil.Add(-threshold)) {
				shouldCheck = true
			}
		}

		if shouldCheck {
			keyStringsToCheck = append(keyStringsToCheck, ksInternal.Key)
		}
	}
	if m.log != nil && len(keyStringsToCheck) > 0 {
		m.log.Debugf("为健康检查选定了 %d 个密钥。", len(keyStringsToCheck))
	}
	return keyStringsToCheck
}

// KeysStatusAccessor 返回当前 keysStatus 切片的副本的指针。
func (m *ApiKeyManager) KeysStatusAccessor() []*ApiKeyStatus {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.checkAndReactivateKeysInternal()

	copiedStatuses := make([]*ApiKeyStatus, len(m.keysStatus))
	copy(copiedStatuses, m.keysStatus)

	if m.log != nil {
		m.log.Debugf("KeysStatusAccessor: 提供了 %d 个密钥状态的访问器（指针列表副本）。", len(copiedStatuses))
	}
	return copiedStatuses
}

// GetTotalKeysCount 返回管理器中当前加载的 API 密钥总数。
func (m *ApiKeyManager) GetTotalKeysCount() int {
	m.lock.Lock()
	defer m.lock.Unlock()
	return len(m.keysStatus)
}
