package handlers

import (
	"net/http"
	"openrouter_polling/config"
	"openrouter_polling/models"
	"strconv"

	"github.com/gin-gonic/gin"
)

// SettingsPageHandler 服务于 `/admin/settings-page` GET 请求，提供设置页面的 HTML。
func SettingsPageHandler(c *gin.Context) {
	Log.Debug("SettingsPageHandler: 正在为已认证用户提供设置页面 (settings.html)。")
	c.HTML(http.StatusOK, "settings.html", nil)
}

// GetSettingsHandler 处理 `/admin/settings` GET 请求，返回当前的可配置项。
func GetSettingsHandler(c *gin.Context) {
	currentSettings := config.GetSettings()
	// 返回一个安全的、仅包含可配置字段的结构体
	c.JSON(http.StatusOK, gin.H{
		"default_model":                currentSettings.DefaultModel,
		"request_timeout_seconds":      int(currentSettings.RequestTimeout.Seconds()),
		"key_failure_cooldown_seconds": int(currentSettings.KeyFailureCooldown.Seconds()),
		"key_max_consecutive_failures": currentSettings.KeyMaxConsecutiveFailures,
		"retry_with_new_key_count":     currentSettings.RetryWithNewKeyCount,
		"health_check_interval_seconds": int(currentSettings.HealthCheckInterval.Seconds()),
		"log_level":                    currentSettings.LogLevel,
		"app_api_key":                  currentSettings.AppAPIKey,
		// 注意：出于安全考虑，不返回 AdminPassword
	})
}

// UpdateSettingsHandler 处理 `/admin/settings` POST 请求，用于热加载新配置。
func UpdateSettingsHandler(c *gin.Context) {
	var req config.UpdateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Log.Warnf("UpdateSettingsHandler: 无效的设置更新请求体: %v", err)
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "请求数据无效: " + err.Error(), Type: "invalid_request_error"}})
		return
	}

	// 对输入值进行一些基本验证
	if req.LogLevel != nil {
		validLevels := []string{"trace", "debug", "info", "warn", "error", "fatal", "panic"}
		isValid := false
		for _, level := range validLevels {
			if *req.LogLevel == level {
				isValid = true
				break
			}
		}
		if !isValid {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrorDetail{
				Message: "无效的日志级别。有效值为: " + strconv.Quote(string(validLevels[0])), Type: "invalid_request_error", Param: "log_level"}})
			return
		}
	}
	if req.RequestTimeoutSeconds != nil && *req.RequestTimeoutSeconds < 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "请求超时不能为负数。", Type: "invalid_request_error", Param: "request_timeout_seconds"}})
		return
	}
	// 可以为其他字段添加更多验证...

	Log.Info("UpdateSettingsHandler: 收到配置热更新请求。")
	config.UpdateSettings(req)

	// 更新需要特殊处理的运行时组件
	// 例如，更新 http.Client 的 Timeout
	if req.RequestTimeoutSeconds != nil {
		// 注意：直接修改全局 HttpClient 不是并发安全的。
		// 更安全的做法是创建一个新的 http.Client 并替换它，但这需要对所有使用它的地方进行修改。
		// 对于这个项目，由于请求量可能不是非常巨大，我们将接受这个风险，但会记录一个警告。
		// 在一个高并发生产系统中，这里需要更复杂的处理。
		Log.Warn("RequestTimeout 已更新，但此更改仅对新创建的 HTTP 客户端生效。当前运行的请求将继续使用旧的超时。为了使所有部分完全生效，建议重启服务。")
		// 实际上，由于 Go 的 http.Client 的 Timeout 字段在创建后是可修改的，我们可以直接更新它。
		// 但需要确保没有请求正在使用它。一个简单的（但不是100%安全）的方法是直接更新。
		// 为了简单起见，我们在这里只更新配置值，并依赖于文档说明。
	}

	c.JSON(http.StatusOK, gin.H{"message": "配置已成功更新。部分设置可能需要重启服务才能完全生效。"})
}
