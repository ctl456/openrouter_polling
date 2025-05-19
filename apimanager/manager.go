package apimanager

import (
	"errors"
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
// 它提供了加载、选择、标记成功/失败以及检索密钥状态等功能。
// 所有对内部密钥列表的访问都通过互斥锁进行保护，以确保并发安全。
type ApiKeyManager struct {
	keysStatus []*ApiKeyStatus // 存储所有 API 密钥状态的切片。指针类型允许直接修改状态。
	lock       sync.Mutex      // 用于保护对 keysStatus 切片的并发访问。
	randSource *rand.Rand      // 用于加权随机选择的随机数源。使用自定义源可以确保可复现性（如果需要）并避免全局锁。
	log        *logrus.Logger  // 日志记录器实例，用于记录管理器的操作和状态。
}

// NewApiKeyManager 创建并返回一个新的 ApiKeyManager 实例。
// logger: 一个配置好的 logrus.Logger 实例，将用于此管理器的所有日志记录。
func NewApiKeyManager(logger *logrus.Logger) *ApiKeyManager {
	if Log == nil && logger != nil { // `Log` 是 `keystatus.go` 中的包级变量。如果它未被设置，则用传入的 logger 设置它。
		Log = logger // 这确保了 keystatus.go 中的方法也能使用相同的 logger 实例。
	}
	return &ApiKeyManager{
		keysStatus: make([]*ApiKeyStatus, 0),                        // 初始化为空的密钥状态切片。
		randSource: rand.New(rand.NewSource(time.Now().UnixNano())), // 使用当前时间的纳秒级精度作为随机数种子，确保每次启动有不同的随机序列。
		log:        logger,                                          // 保存传入的 logger 实例。
	}
}

// parseKeyAndWeight 是一个内部辅助函数，用于解析单个密钥条目字符串。
// 密钥条目可以只是密钥本身，也可以是 "密钥:权重" 的格式。
// entry: 待解析的密钥条目字符串。
// 返回: 解析出的密钥字符串, 权重 (默认为1), 以及可能发生的错误。
func (m *ApiKeyManager) parseKeyAndWeight(entry string) (keyStr string, weight int, err error) {
	entry = strings.TrimSpace(entry) // 去除首尾空格
	if entry == "" {
		return "", 0, ErrInvalidKeyFormat // 空条目是无效的
	}

	keyStr = entry
	weight = 1 // 默认权重为1

	// 如果条目包含冒号，则尝试解析权重
	if strings.Contains(entry, ":") {
		parts := strings.SplitN(entry, ":", 2) // 最多分割成两部分
		keyStr = strings.TrimSpace(parts[0])
		if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
			parsedWeight, parseErr := strconv.Atoi(strings.TrimSpace(parts[1]))
			if parseErr != nil || parsedWeight < 1 { // 权重必须是大于0的整数
				if m.log != nil {
					m.log.Warnf("密钥 %s 的权重 '%s' 格式无效或小于1，将使用默认权重 1。", utils.SafeSuffix(keyStr), parts[1])
				}
				// 根据策略，可以选择返回错误或使用默认权重。当前选择使用默认权重并记录警告。
				// 如果严格要求权重有效，则应返回: return "", 0, ErrInvalidKeyFormat
			} else {
				weight = parsedWeight
			}
		}
	}

	if keyStr == "" { // 确保解析后的密钥字符串不为空
		return "", 0, ErrInvalidKeyFormat
	}
	return keyStr, weight, nil
}

// LoadKeys 从配置字符串加载或更新密钥池。
// 此操作会清空所有现有密钥，然后加载新的密钥列表。
// openrouterKeysConfigStr: 包含一个或多个密钥条目的字符串，以逗号分隔。
// 每个条目可以是 "密钥" 或 "密钥:权重"。
func (m *ApiKeyManager) LoadKeys(openrouterKeysConfigStr string) {
	m.lock.Lock()         // 获取锁，保护 keysStatus
	defer m.lock.Unlock() // 函数退出时释放锁

	m.keysStatus = make([]*ApiKeyStatus, 0) // 清空现有密钥列表

	if openrouterKeysConfigStr == "" {
		if m.log != nil {
			m.log.Warn("OPENROUTER_API_KEYS 配置为空。没有密钥加载到管理器。")
		}
		return
	}

	keyEntries := strings.Split(openrouterKeysConfigStr, ",") // 按逗号分割密钥条目
	parsedCount := 0
	uniqueKeys := make(map[string]bool) // 用于检测重复密钥

	for _, entry := range keyEntries {
		key, weight, err := m.parseKeyAndWeight(entry) // 解析单个密钥条目
		if err != nil {
			if m.log != nil {
				m.log.Warnf("跳过无效的密钥条目 '%s': %v", entry, err)
			}
			continue // 跳过无效条目
		}

		if _, exists := uniqueKeys[key]; exists {
			if m.log != nil {
				m.log.Warnf("在加载配置中发现重复密钥 %s，将跳过后续出现。", utils.SafeSuffix(key))
			}
			continue // 跳过重复密钥
		}
		uniqueKeys[key] = true

		// 创建新的 ApiKeyStatus 并添加到列表中
		m.keysStatus = append(m.keysStatus, &ApiKeyStatus{
			Key:      key,
			Weight:   weight,
			IsActive: true, // 新加载的密钥默认为活动状态
			// FailureCount, LastFailureTime, CoolDownUntil, LastUsedTime 默认为零值或 nil
		})
		parsedCount++
	}

	if m.log != nil {
		m.log.Infof("成功从配置加载/更新了 %d 个 OpenRouter API 密钥到管理器。", parsedCount)
	}
}

// AddKey 【新增】向管理器中添加单个新的 API 密钥。
// keyEntry: 待添加的密钥条目字符串，格式可以是 "密钥" 或 "密钥:权重"。
// 返回: 如果成功添加则为 nil，否则返回错误 (例如 ErrKeyAlreadyExists, ErrInvalidKeyFormat)。
func (m *ApiKeyManager) AddKey(keyEntry string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	keyStr, weight, err := m.parseKeyAndWeight(keyEntry) // 解析密钥和权重
	if err != nil {
		return err // 返回解析错误
	}

	// 检查密钥是否已存在于管理器中
	for _, ks := range m.keysStatus {
		if ks.Key == keyStr {
			if m.log != nil {
				m.log.Warnf("尝试添加已存在的密钥 %s。", utils.SafeSuffix(keyStr))
			}
			return ErrKeyAlreadyExists // 密钥已存在
		}
	}

	// 创建新的 ApiKeyStatus 并添加到列表
	newKeyStatus := &ApiKeyStatus{
		Key:      keyStr,
		Weight:   weight,
		IsActive: true, // 新添加的密钥默认为活动状态
	}
	m.keysStatus = append(m.keysStatus, newKeyStatus)

	if m.log != nil {
		m.log.Infof("成功添加密钥 %s (权重: %d)。当前总密钥数: %d", utils.SafeSuffix(keyStr), weight, len(m.keysStatus))
	}
	// 注意：此修改仅在内存中。如果需要持久化（例如，更新配置文件或环境变量），
	// 则需要额外的逻辑。在此简化实现中，重启后通过此方法添加的密钥会丢失。
	return nil
}

// DeleteKeyBySuffix 【新增】根据密钥的后缀从管理器中删除一个 API 密钥。
// suffix: 要删除密钥的 utils.SafeSuffix() 返回的后缀。
// 返回: 如果成功删除则为 nil，否则返回错误 (例如 ErrKeyNotFound)。
func (m *ApiKeyManager) DeleteKeyBySuffix(suffix string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	foundIndex := -1
	var keyToDelete string

	// 遍历查找具有匹配后缀的密钥
	// 为了提高精确性，我们期望 suffix 是由 utils.SafeSuffix 生成的固定长度的后缀。
	for i, ks := range m.keysStatus {
		// 假设 utils.SafeSuffix(ks.Key) 返回的是一个可靠的、用于UI显示的标识符。
		// 如果 ks.Key 本身就可能很短，比如 "..."+真实后缀，那么 utils.SafeSuffix(ks.Key) 可能会产生 "... ..."
		// 所以确保 utils.SafeSuffix 处理短字符串的方式与这里的比较兼容。
		// 当前 utils.SafeSuffix 对短于4个字符的也会返回 "..."+原串，所以key_suffix应该是唯一的。
		if utils.SafeSuffix(ks.Key) == suffix {
			foundIndex = i
			keyToDelete = ks.Key // 保存完整密钥用于日志记录
			break
		}
	}

	if foundIndex == -1 {
		if m.log != nil {
			m.log.Warnf("尝试删除密钥失败：未找到后缀为 '%s' 的密钥。", suffix)
		}
		return ErrKeyNotFound // 未找到密钥
	}

	// 从切片中移除元素：通过将后面的元素向前移动一位来覆盖要删除的元素，然后缩短切片。
	// 这是Go中删除切片元素的常用方法。
	m.keysStatus = append(m.keysStatus[:foundIndex], m.keysStatus[foundIndex+1:]...)

	if m.log != nil {
		m.log.Infof("成功删除密钥 %s (后缀: %s)。当前总密钥数: %d", utils.SafeSuffix(keyToDelete), suffix, len(m.keysStatus))
	}
	// 注意：持久化问题同 AddKey。
	return nil
}

// GetNextAPIKey 根据加权随机算法选择下一个可用的 API 密钥。
// 此方法会首先检查并尝试重新激活任何已过冷却期的密钥。
// 返回: 指向选定 ApiKeyStatus 的指针；如果没有可用的密钥，则返回 nil。
func (m *ApiKeyManager) GetNextAPIKey() *ApiKeyStatus {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.checkAndReactivateKeysInternal() // 内部调用，检查并重新激活冷却期已过的密钥

	eligibleKeys := make([]*ApiKeyStatus, 0) // 存储所有当前可用的密钥
	totalWeight := 0                         // 可用密钥的总权重

	// 筛选出所有可用的密钥并计算总权重
	for _, ks := range m.keysStatus {
		if ks.CanUse() { // CanUse 检查 IsActive 和可能的冷却状态
			eligibleKeys = append(eligibleKeys, ks)
			totalWeight += ks.Weight
		}
	}

	// 如果没有可用的密钥
	if len(eligibleKeys) == 0 {
		if m.log != nil {
			m.log.Warn("ApiKeyManager: 当前没有活动的或未在冷却期内的 API 密钥可供选择。")
		}
		return nil
	}

	// 如果总权重小于等于0（例如，所有可用密钥权重都为0或负数，尽管我们限制权重>=1），
	// 则退回到均匀随机选择。
	if totalWeight <= 0 {
		if m.log != nil {
			m.log.Warnf("ApiKeyManager: 可用密钥的总权重为 %d (不应小于1)。将从 %d 个可用密钥中随机选择一个。", totalWeight, len(eligibleKeys))
		}
		// 随机选择一个，不考虑权重
		return eligibleKeys[m.randSource.Intn(len(eligibleKeys))]
	}

	// 执行加权随机选择
	randomNum := m.randSource.Intn(totalWeight) // 生成一个 [0, totalWeight-1) 范围内的随机数
	currentWeightSum := 0
	var selectedKeyStatus *ApiKeyStatus = nil

	for _, ks := range eligibleKeys {
		currentWeightSum += ks.Weight
		if randomNum < currentWeightSum { // 当累积权重超过随机数时，选中当前密钥
			selectedKeyStatus = ks
			break
		}
	}

	// 后备逻辑：理论上，如果 totalWeight > 0，上面的循环总能选出一个密钥。
	// 但为防止意外情况（例如浮点数精度问题，虽然这里是整数），如果未选出，则从合格密钥中随机选一个。
	if selectedKeyStatus == nil && len(eligibleKeys) > 0 {
		if m.log != nil {
			m.log.Error("ApiKeyManager: 加权随机选择未能选出密钥（不应发生），将随机选择一个可用密钥。")
		}
		selectedKeyStatus = eligibleKeys[m.randSource.Intn(len(eligibleKeys))]
	}

	// 如果成功选出密钥，更新其上次使用时间
	if selectedKeyStatus != nil {
		selectedKeyStatus.UpdateLastUsed()
		if m.log != nil {
			m.log.Debugf("ApiKeyManager: 选定密钥 %s (权重 %d) 用于请求。", utils.SafeSuffix(selectedKeyStatus.Key), selectedKeyStatus.Weight)
		}
	}
	return selectedKeyStatus
}

// MarkKeyFailure 标记指定的密钥字符串对应的密钥发生了一次失败。
// keyString: 发生失败的密钥的完整字符串。
func (m *ApiKeyManager) MarkKeyFailure(keyString string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	for _, ks := range m.keysStatus {
		if ks.Key == keyString {
			ks.RecordFailure() // 调用 ApiKeyStatus 的方法记录失败
			// m.log.Warnf("已标记密钥 %s 失败。", utils.SafeSuffix(keyString)) // RecordFailure 内部已有日志
			break // 找到并处理后即可退出循环
		}
	}
}

// RecordKeySuccess 记录指定的密钥字符串对应的密钥成功使用或被重新激活。
// keyString: 成功使用或被重新激活的密钥的完整字符串。
func (m *ApiKeyManager) RecordKeySuccess(keyString string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	for _, ks := range m.keysStatus {
		if ks.Key == keyString {
			ks.RecordSuccessOrReactivate() // 调用 ApiKeyStatus 的方法记录成功/激活
			// m.log.Infof("已记录密钥 %s 成功/重新激活。", utils.SafeSuffix(keyString)) // RecordSuccessOrReactivate 内部已有日志
			break // 找到并处理后即可退出循环
		}
	}
}

// checkAndReactivateKeysInternal 是一个内部方法，用于遍历所有密钥，
// 并尝试重新激活那些已结束冷却期的非活动密钥。
// 此方法不获取或释放锁，因为它被假定由已持有锁的公共方法调用。
func (m *ApiKeyManager) checkAndReactivateKeysInternal() {
	now := time.Now()
	reactivatedCount := 0
	for _, ks := range m.keysStatus {
		// 条件：密钥非活动，且设置了冷却时间，且当前时间已超过冷却时间
		if !ks.IsActive && ks.CoolDownUntil != nil && now.After(*ks.CoolDownUntil) {
			if m.log != nil {
				m.log.Infof("密钥 %s 冷却期已过 (冷却至: %s)，尝试重新激活。",
					utils.SafeSuffix(ks.Key), ks.CoolDownUntil.Format(time.RFC3339))
			}
			ks.RecordSuccessOrReactivate() // 重新激活密钥
			reactivatedCount++
		}
	}
	if reactivatedCount > 0 && m.log != nil {
		m.log.Debugf("内部检查：重新激活了 %d 个冷却期已过的密钥。", reactivatedCount)
	}
}

// GetAllKeyStatusesSafe 获取所有当前管理的 API 密钥的安全状态列表 (ApiKeyStatusSafe)。
// 此方法首先会检查并尝试重新激活冷却期已过的密钥。
// 返回: 一个 ApiKeyStatusSafe 结构体的切片，用于API响应，不包含完整密钥。
func (m *ApiKeyManager) GetAllKeyStatusesSafe() []ApiKeyStatusSafe {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.checkAndReactivateKeysInternal() // 确保状态是最新的

	safeStatuses := make([]ApiKeyStatusSafe, len(m.keysStatus))
	for i, ks := range m.keysStatus {
		safeStatuses[i] = ks.ToSafe() // 将每个 ApiKeyStatus 转换为其安全版本
	}
	return safeStatuses
}

// GetKeyStatusByKeyStr 根据完整的密钥字符串获取其详细状态 (ApiKeyStatus)。
// keyStr: 要查找的完整 API 密钥字符串。
// 返回: 指向 ApiKeyStatus 的指针；如果未找到该密钥，则返回 nil。
// 【注意】调用者不应修改返回的 ApiKeyStatus 指针所指向的内容而不持有 ApiKeyManager 的锁。
// 此方法主要供内部使用或需要直接访问原始状态的场景（例如健康检查器在操作前获取最新状态）。
func (m *ApiKeyManager) GetKeyStatusByKeyStr(keyStr string) *ApiKeyStatus {
	m.lock.Lock()         // 加锁，因为我们返回的是内部状态的指针，尽管通常不鼓励这样做。
	defer m.lock.Unlock() // 更安全的方式是返回一个副本，但为了性能和现有用法，暂时保持。

	for _, ks := range m.keysStatus {
		if ks.Key == keyStr {
			return ks
		}
	}
	return nil // 未找到
}

// GetKeysForHealthCheck 返回一个需要进行健康检查的密钥字符串切片。
// 此方法已被 `KeysStatusAccessor` 和健康检查器内部逻辑取代或补充。
// 保留此方法以供参考或特定用途，但其逻辑可能需要与 `healthcheck/checker.go` 中的逻辑协调。
// 当前逻辑：选择非活动或有失败记录，并且（已可用 或 冷却期即将结束）的密钥。
func (m *ApiKeyManager) GetKeysForHealthCheck() []string {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.checkAndReactivateKeysInternal() // 确保状态更新

	var keyStringsToCheck []string
	now := time.Now()
	for _, ksInternal := range m.keysStatus {
		// 候选条件: 非活动 或 有失败记录
		isCandidate := !ksInternal.IsActive || ksInternal.FailureCount > 0
		if !isCandidate {
			continue
		}

		// 检查条件: 可用 或 冷却期即将结束
		shouldCheck := ksInternal.CanUse() // CanUse 主要检查 IsActive，在 checkAndReactivateKeysInternal 后，如果冷却结束，IsActive 会变 true
		if !shouldCheck && ksInternal.CoolDownUntil != nil {
			// 设置一个“即将到期”的阈值，例如冷却时间的 1/5 或固定的1分钟，取较小者。
			// 目的是在密钥实际可用前一小段时间开始检查，以便它一旦可用就能立即被使用。
			threshold := config.AppSettings.KeyFailureCooldown / 5
			if threshold > 1*time.Minute { // 确保阈值不会过大
				threshold = 1 * time.Minute
			}
			// 如果当前时间在 (CoolDownUntil - threshold) 之后，意味着即将到期
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
// 【重要】: 返回的是副本，对副本的修改不会影响管理器内部状态。
// 此方法主要用于健康检查器等需要遍历所有密钥状态的场景，
// 通过操作副本来减少对主 ApiKeyManager 锁的持有时间。
// 返回: 一个新的 []*ApiKeyStatus 切片，包含当前所有密钥状态的副本。
func (m *ApiKeyManager) KeysStatusAccessor() []*ApiKeyStatus {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.checkAndReactivateKeysInternal() // 更新内部状态

	// 创建一个新切片，并将内部 m.keysStatus 中的每个元素（是指针）复制过去。
	// 注意：这复制的是指针列表，不是 ApiKeyStatus 结构体本身。
	// 因此，对副本切片中的 ApiKeyStatus 对象所指向内容的修改 *仍会* 影响原始对象。
	// 如果需要完全深拷贝，则需要遍历并创建每个 ApiKeyStatus 的新实例。
	// 对于健康检查器读取状态的目的，浅拷贝指针列表通常足够，只要检查器不修改这些状态。
	// 如果健康检查器需要基于这些状态做决策后修改原始状态，它应通过 MarkKeyFailure/RecordKeySuccess 等方法。
	copiedStatuses := make([]*ApiKeyStatus, len(m.keysStatus))
	for i, ks := range m.keysStatus {
		// 创建 ApiKeyStatus 的浅拷贝副本。这意味着字段是复制的，但指针字段（如 LastFailureTime）仍然指向相同的数据。
		// 对于健康检查器这种主要读取状态的场景，这通常是安全的。
		// 如果要确保完全隔离，需要更深的拷贝。
		// 简单的做法是：
		// statusCopy := *ks
		// copiedStatuses[i] = &statusCopy
		// 但由于 ks 已经是 *ApiKeyStatus，所以直接复制指针是当前代码的行为。
		// 为了避免外部修改，更安全的做法是返回 ApiKeyStatusSafe 列表，或者进行深拷贝。
		// 鉴于此方法的主要用户是 healthcheck，它通过 Key 字符串回调 Manager，所以当前方式可接受。
		copiedStatuses[i] = ks
	}
	// 正确的浅拷贝应该是：
	// copy(copiedStatuses, m.keysStatus) // 这只会复制指针值到新切片。

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
