package apimanager

import (
	"openrouter_polling/config"
	"openrouter_polling/storage"
	"openrouter_polling/utils"
	"time"

	"github.com/sirupsen/logrus"
)

var Log *logrus.Logger

// ApiKeyStatus 在内存中描述单个 API 密钥的运行时状态。
// 它嵌入了数据库模型 storage.APIKey，并用于管理其在运行时的可用性和行为。
type ApiKeyStatus struct {
	storage.APIKey // 嵌入数据库模型
}

// NewApiKeyStatusFromModel 从数据库模型创建一个内存中的 ApiKeyStatus 实例。
func NewApiKeyStatusFromModel(dbKey *storage.APIKey) *ApiKeyStatus {
	return &ApiKeyStatus{
		APIKey: *dbKey,
	}
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
}

// IsCurrentlyCoolingDown 检查密钥当前是否正处于有效的冷却期内。
// 如果 CoolDownUntil 字段被设置并且其时间点在未来，则返回 true。
func (aks *ApiKeyStatus) IsCurrentlyCoolingDown() bool {
	return aks.CoolDownUntil != nil && time.Now().Before(*aks.CoolDownUntil)
}

// CanUse 判断密钥当前是否可以被选择用于API请求。
func (aks *ApiKeyStatus) CanUse() bool {
	if !aks.IsActive {
		return false
	}
	return true
}

// RecordFailure 在内存中记录一次密钥使用失败。
// 此方法会更新内存中 ApiKeyStatus 的状态，但不会将其持久化到数据库。
// 持久化操作由 ApiKeyManager 协调。
func (aks *ApiKeyStatus) RecordFailure() time.Duration {
	aks.FailureCount++
	now := time.Now()
	aks.LastFailureTime = &now
	aks.IsActive = false // 关键：在失败时将密钥标记为非活动。

	// 计算冷却时间
	cooldownDuration := config.AppSettings.KeyFailureCooldown
	if config.AppSettings.KeyMaxConsecutiveFailures > 0 && aks.FailureCount > (config.AppSettings.KeyMaxConsecutiveFailures/2) {
		failuresOverHalf := aks.FailureCount - (config.AppSettings.KeyMaxConsecutiveFailures / 2)
		if failuresOverHalf > 0 {
			multiplier := time.Duration(failuresOverHalf + 1)
			if multiplier < 2 {
				multiplier = 2
			}
			cooldownDuration *= multiplier
			if Log != nil {
				Log.Debugf("密钥 %s 渐进式冷却: 失败次数 %d, 乘数 %d, 当前冷却时间 %v",
					utils.SafeSuffix(aks.Key), aks.FailureCount, multiplier, cooldownDuration)
			}
		}
	}

	coolDownUntilTime := time.Now().Add(cooldownDuration)
	aks.CoolDownUntil = &coolDownUntilTime

	if Log != nil {
		Log.Warnf("密钥 %s 在内存中记录失败。失败次数: %d。将冷却 %v 直到 %s。",
			utils.SafeSuffix(aks.Key), aks.FailureCount, cooldownDuration, aks.CoolDownUntil.Format(time.RFC3339))
	}
	return cooldownDuration
}

// RecordSuccessOrReactivate 在内存中记录一次密钥使用成功或重新激活。
// 此方法会将内存中的 ApiKeyStatus 状态重置为活动，并清除失败计数和冷却信息。
func (aks *ApiKeyStatus) RecordSuccessOrReactivate() {
	changedState := !aks.IsActive || aks.FailureCount > 0
	aks.IsActive = true
	aks.FailureCount = 0
	aks.LastFailureTime = nil
	aks.CoolDownUntil = nil

	if changedState && Log != nil {
		Log.Infof("密钥 %s 在内存中已成功使用/重新激活。", utils.SafeSuffix(aks.Key))
	}
}

// UpdateLastUsed 在内存中更新密钥的上次使用时间。
func (aks *ApiKeyStatus) UpdateLastUsed() {
	now := time.Now()
	aks.LastUsedTime = &now
}

// ToSafe 将 ApiKeyStatus (及其嵌入的 storage.APIKey) 转换为 ApiKeyStatusSafe DTO。
func (aks *ApiKeyStatus) ToSafe() ApiKeyStatusSafe {
	return ApiKeyStatusSafe{
		KeySuffix:       utils.SafeSuffix(aks.Key),
		IsActive:        aks.IsActive,
		FailureCount:    aks.FailureCount,
		LastFailureTime: aks.LastFailureTime,
		CoolDownUntil:   aks.CoolDownUntil,
		LastUsedTime:    aks.LastUsedTime,
		Weight:          aks.Weight,
	}
}
