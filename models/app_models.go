package models

import "time"

// ReloadKeysRequest /admin/reload-keys 端点的请求体结构。
type ReloadKeysRequest struct {
	OpenRouterAPIKeysStr string `json:"openrouter_api_keys_str" binding:"required"` // OpenRouter API 密钥字符串，逗号分隔，可带权重 (e.g., "key1:10,key2")
}

// ErrorDetail 错误详情结构，用于在 API 响应中提供统一的错误信息。
// 符合 OpenAI 错误对象的风格。
type ErrorDetail struct {
	Message string `json:"message"`         // 必需：可读的错误描述。
	Type    string `json:"type"`            // 必需：错误类型，例如 "api_error", "auth_error", "invalid_request_error"。
	Code    any    `json:"code,omitempty"`  // 可选：特定于错误的机器可读代码 (可以是数字状态码的字符串形式，或自定义错误代码如 "invalid_api_key")。
	Param   string `json:"param,omitempty"` // 可选：导致错误的参数名称 (如果错误与特定请求参数相关)。
}

// ErrorResponse 统一的错误响应结构，包装了 ErrorDetail。
// 符合 OpenAI 错误对象的风格。
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// AppStatusInfo 结构体用于 /admin/app-status 端点，提供应用的监控和配置状态。
type AppStatusInfo struct {
	StartTime                  time.Time `json:"start_time"`                    // 应用启动时间戳
	Uptime                     string    `json:"uptime"`                        // 应用已运行时间（人类可读格式）
	GoVersion                  string    `json:"go_version"`                    // 编译时使用的 Go 语言版本
	NumGoroutines              int       `json:"num_goroutines"`                // 当前活跃的 Goroutine 数量
	MemAllocatedMB             float64   `json:"mem_allocated_mb"`              // 当前分配的堆内存 (MB)
	MemTotalAllocatedMB        float64   `json:"mem_total_allocated_mb"`        // 自程序启动以来累计分配的堆内存 (MB)
	MemSysMB                   float64   `json:"mem_sys_mb"`                    // 程序从操作系统获取的总内存 (MB)
	NumGC                      uint32    `json:"num_gc"`                        // 已完成的垃圾回收周期数
	LastGC                     time.Time `json:"last_gc"`                       // 上次垃圾回收完成的时间戳
	OpenRouterAPIKeysProvided  bool      `json:"openrouter_api_keys_provided"`  // 是否配置了 OpenRouter API 密钥 (环境变量 OPENROUTER_API_KEYS 是否非空)
	DefaultModel               string    `json:"default_model"`                 // 应用配置的默认模型 ID
	OpenRouterAPIURL           string    `json:"openrouter_api_url"`            // OpenRouter 聊天 API 的目标 URL
	OpenRouterModelsURL        string    `json:"openrouter_models_url"`         // OpenRouter 模型列表 API 的目标 URL
	RequestTimeoutSeconds      float64   `json:"request_timeout_seconds"`       // 对 OpenRouter 请求的超时设置（秒）
	KeyFailureCooldownSeconds  float64   `json:"key_failure_cooldown_seconds"`  // API 密钥失败后的基础冷却时间（秒）
	KeyMaxConsecutiveFailures  int       `json:"key_max_consecutive_failures"`  // API 密钥被标记为非活动前的最大连续失败次数
	RetryWithNewKeyCount       int       `json:"retry_with_new_key_count"`      // 当一个密钥失败时，尝试使用其他密钥的次数
	HealthCheckIntervalSeconds float64   `json:"health_check_interval_seconds"` // API 密钥健康检查的间隔时间（秒）
	Port                       string    `json:"port"`                          // 应用监听的端口号
	LogLevel                   string    `json:"log_level"`                     // 当前配置的日志级别
	GinMode                    string    `json:"gin_mode"`                      // 当前 Gin 框架的运行模式 (debug/release)
	AdminPasswordConfigured    bool      `json:"admin_password_configured"`     // 【新增】仪表盘登录密码是否已配置且不是默认密码 (用于提示安全性)
}

// SSE (Server-Sent Events) 相关常量，用于流式 API 响应。
const (
	SSEDataPrefix  = "data: " // SSE 事件中数据行必须以此字符串开头。
	SSEDonePayload = "[DONE]" // OpenAI 风格的流结束时发送的特殊数据负载。
)
