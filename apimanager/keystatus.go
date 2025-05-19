package apimanager

import (
	"openrouter_polling/config"
	"openrouter_polling/utils"
	"time"

	"github.com/sirupsen/logrus"
)

var Log *logrus.Logger

// ApiKeyStatus 描述单个 API 密钥的详细状态。
// 此结构体包含密钥本身及其运行时属性，用于管理其可用性和行为。
type ApiKeyStatus struct {
	Key             string     // API 密钥字符串。这是敏感信息，应小心处理。
	IsActive        bool       // 标记密钥当前是否被认为是活动的和可用的。
	FailureCount    int        // 此密钥连续失败的次数。成功使用后会重置为0。
	LastFailureTime *time.Time // 上次记录失败的时间戳。如果从未失败或已成功重置，则为 nil。
	CoolDownUntil   *time.Time // 如果密钥因失败而处于冷却期，此字段指示冷却结束的时间。在此时间之前，密钥不应被使用。
	LastUsedTime    *time.Time // 上次成功使用此密钥的时间戳。
	Weight          int        // 密钥的权重，用于加权随机选择算法。权重越高的密钥被选中的概率越大。默认为1。
}

// ApiKeyStatusSafe 是 ApiKeyStatus 的一个“安全”版本，用于API响应，特别是面向管理员仪表盘。
// 它不暴露完整的 API 密钥，而是显示密钥的后缀，并包含其他对监控有用的状态信息。
// 字段明确使用 `json:"..."` 标签以匹配前端 (如 dashboard.html JavaScript) 的期望。
type ApiKeyStatusSafe struct {
	KeySuffix       string     `json:"key_suffix"`        // API 密钥的末尾几位（例如，最后4位），用于在UI中识别密钥而不暴露完整密钥。
	IsActive        bool       `json:"is_active"`         // 密钥当前是否激活。这应反映密钥是否已结束冷却且未被手动禁用。
	FailureCount    int        `json:"failure_count"`     // 连续失败次数。
	LastFailureTime *time.Time `json:"last_failure_time"` // 上次失败的时间戳。
	CoolDownUntil   *time.Time `json:"cool_down_until"`   // 密钥的冷却截止时间。如果非nil且在未来，表示密钥正在冷却。
	LastUsedTime    *time.Time `json:"last_used_time"`    // 上次使用此密钥的时间戳。
	Weight          int        `json:"weight"`            // 密钥的权重。
	// 前端可以直接通过检查 CoolDownUntil && new Date(CoolDownUntil) > new Date() 来判断。
}

// IsCurrentlyCoolingDown 检查密钥当前是否正处于有效的冷却期内。
// 如果 CoolDownUntil 字段被设置并且其时间点在未来，则返回 true。
func (aks *ApiKeyStatus) IsCurrentlyCoolingDown() bool {
	return aks.CoolDownUntil != nil && time.Now().Before(*aks.CoolDownUntil)
}

// CanUse 判断密钥当前是否可以被选择用于API请求。
// 主要检查 IsActive 状态。在当前的实现中，当密钥进入冷却期时，IsActive 会被设为 false。
// 因此，单独检查 IsActive 通常足够。
// 如果存在 IsActive 为 true 但仍在冷却的情况（不符合当前设计），则需要取消注释的额外检查。
func (aks *ApiKeyStatus) CanUse() bool {
	if !aks.IsActive {
		return false // 如果密钥被明确标记为非活动，则不可用。
	}
	// 假设：如果密钥正在冷却，RecordFailure 会将 IsActive 设置为 false。
	// 如果 IsActive 可能为 true 但 CoolDownUntil 仍然设置且在将来，则需要额外检查。
	// 例如:
	// if aks.IsCurrentlyCoolingDown() {
	//     return false
	// }
	return true // 否则，密钥可用。
}

// RecordFailure 记录一次密钥使用失败。
// 此方法会增加失败计数，更新上次失败时间，并将密钥设置为非活动状态。
// 它还会根据失败次数计算并设置一个渐进的冷却期。
func (aks *ApiKeyStatus) RecordFailure() {
	aks.FailureCount++
	now := time.Now()
	aks.LastFailureTime = &now
	aks.IsActive = false // 关键：在失败时将密钥标记为非活动。

	// 计算冷却时间，采用渐进式策略。
	cooldownDuration := config.AppSettings.KeyFailureCooldown // 基础冷却时间

	// 如果配置了最大连续失败次数，并且当前失败次数超过该值的一半，
	// 则增加冷却时间。这是一种惩罚机制，以避免反复使用有问题的密钥。
	// 这里的逻辑是：超过一半最大失败次数后，每多失败一次，冷却时间就增加一个基础冷却时间的倍数。
	// 例如，如果基础冷却10分钟，最大失败6次：
	// 第4次失败 (超过 6/2=3): 冷却时间 = 10 * ( (4 - 3) + 1 ) = 20 分钟
	// 第5次失败: 冷却时间 = 10 * ( (5 - 3) + 1 ) = 30 分钟
	// 第6次失败: 冷却时间 = 10 * ( (6 - 3) + 1 ) = 40 分钟
	// 【注意】这种乘法可能导致非常长的冷却时间，需要根据实际情况调整策略，例如设置冷却上限。
	if config.AppSettings.KeyMaxConsecutiveFailures > 0 && aks.FailureCount > (config.AppSettings.KeyMaxConsecutiveFailures/2) {
		failuresOverHalf := aks.FailureCount - (config.AppSettings.KeyMaxConsecutiveFailures / 2)
		if failuresOverHalf > 0 {
			// 确保乘数至少为2 (failuresOverHalf+1)，避免乘以1或更小的值。
			multiplier := time.Duration(failuresOverHalf + 1)
			if multiplier < 2 { // 额外保护，理论上 failuresOverHalf > 0 时，multiplier >= 2
				multiplier = 2
			}
			cooldownDuration *= multiplier
			if Log != nil {
				Log.Debugf("密钥 %s 渐进式冷却: 失败次数 %d, 乘数 %d, 当前冷却时间 %v",
					utils.SafeSuffix(aks.Key), aks.FailureCount, multiplier, cooldownDuration)
			}
		}
	}

	// 设置冷却截止时间。
	coolDownUntilTime := time.Now().Add(cooldownDuration)
	aks.CoolDownUntil = &coolDownUntilTime

	if Log != nil {
		Log.Warnf("密钥 %s 记录失败。失败次数: %d。将冷却 %v 直到 %s。",
			utils.SafeSuffix(aks.Key), aks.FailureCount, cooldownDuration, aks.CoolDownUntil.Format(time.RFC3339))
	}
}

// RecordSuccessOrReactivate 记录一次密钥使用成功，或在冷却期结束后/健康检查成功后重新激活密钥。
// 此方法会将密钥状态重置为活动，并清除失败计数和冷却信息。
func (aks *ApiKeyStatus) RecordSuccessOrReactivate() {
	// 仅当状态发生显著变化时（即，从非活动变为活动，或失败计数清零时）记录日志，以减少冗余日志。
	changedState := !aks.IsActive || aks.FailureCount > 0
	aks.IsActive = true
	aks.FailureCount = 0
	aks.LastFailureTime = nil // 清除上次失败时间
	aks.CoolDownUntil = nil   // 清除冷却截止时间

	if changedState && Log != nil { // 仅当状态实际改变时记录
		Log.Infof("密钥 %s 已成功使用/重新激活。", utils.SafeSuffix(aks.Key))
	}
}

// UpdateLastUsed 更新密钥的上次使用时间为当前时间。
// 这通常在密钥被选中并用于API请求后调用。
func (aks *ApiKeyStatus) UpdateLastUsed() {
	now := time.Now()
	aks.LastUsedTime = &now
}

// ToSafe 将 ApiKeyStatus 转换为 ApiKeyStatusSafe DTO (Data Transfer Object)。
// 这个 DTO 用于 API 响应，隐藏了完整的密钥，并可能包含一些计算字段（当前没有额外计算字段）。
func (aks *ApiKeyStatus) ToSafe() ApiKeyStatusSafe {
	return ApiKeyStatusSafe{
		KeySuffix:       utils.SafeSuffix(aks.Key), // 使用工具函数获取密钥的安全后缀
		IsActive:        aks.IsActive,              // 直接反映当前激活状态
		FailureCount:    aks.FailureCount,
		LastFailureTime: aks.LastFailureTime,
		CoolDownUntil:   aks.CoolDownUntil,
		LastUsedTime:    aks.LastUsedTime,
		Weight:          aks.Weight,
	}
}
