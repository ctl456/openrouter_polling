package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv" // 用于从 .env 文件加载环境变量
)

// --- 全局变量和常量 ---
const (
	// 默认配置值
	DefaultKeyFailureCooldownSeconds  = 600
	DefaultKeyMaxConsecutiveFailures  = 3
	DefaultRetryWithNewKeyCount       = 3
	DefaultHealthCheckIntervalSeconds = 60 * 5
	DefaultRequestTimeoutSeconds      = 180
	DefaultPort                       = "8000"
	DefaultLogLevel                   = "info"
	DefaultGinMode                    = "debug" // 默认为 debug 模式，方便开发；生产环境建议设为 release
	DefaultOpenRouterAPIURL           = "https://openrouter.ai/api/v1/chat/completions"
	DefaultOpenRouterModelsURL        = "https://openrouter.ai/api/v1/models"
	DefaultModel                      = "deepseek/deepseek-chat-v3-0324:free"                     // 示例模型，用户应根据需求更改
	DefaultHTTPReferer                = "https://your-app-name.com"                               // 建议用户修改为自己的应用名或来源
	DefaultXTitle                     = "Your App Name"                                           // 建议用户修改为自己的应用名
	DefaultAdminPassword              = "admin"                                                   // 【重要】仅用于演示，请务必更改或从安全位置加载。空字符串表示禁用密码登录。
	DefaultSessionSecretKey           = "a-very-secret-and-random-key-replace-this-in-production" // 【重要】强烈建议替换为一个长且随机的字符串
)

// Settings 存储应用配置
type Settings struct {
	AppAPIKey                 string        // 此代理服务自身的 API 密钥，用于验证客户端对 /v1/* 接口的请求
	OpenRouterAPIKeys         string        // OpenRouter 的 API 密钥列表，逗号分隔，可带权重 (e.g., "key1:10,key2:5,key3")
	DefaultModel              string        // 默认使用的模型 ID
	OpenRouterAPIURL          string        // OpenRouter聊天API的端点URL
	OpenRouterModelsURL       string        // OpenRouter模型列表API的端点URL
	RequestTimeout            time.Duration // 对 OpenRouter 发出请求的超时时间
	AdminAPIKey               string        // 【旧】管理员操作的 API 密钥 (主要用于非Web UI的脚本化管理，Web UI 使用 session)
	KeyFailureCooldown        time.Duration // 密钥失败后的冷却时间
	KeyMaxConsecutiveFailures int           // 密钥最大连续失败次数，达到后可能触发更长冷却或禁用
	RetryWithNewKeyCount      int           // 使用新密钥重试次数
	HealthCheckInterval       time.Duration // 定期健康检查的间隔时间
	HTTPReferer               string        // 发往 OpenRouter 请求时携带的 HTTP-Referer
	XTitle                    string        // 发往 OpenRouter 请求时携带的 X-Title
	Port                      string        // 服务监听的端口号
	LogLevel                  string        // 日志级别 (e.g., "debug", "info", "warn", "error")
	GinMode                   string        // Gin 框架运行模式 ("debug" or "release")
	AdminPassword             string        `mapstructure:"ADMIN_PASSWORD"`     // 【新增】仪表盘登录密码。如果为空字符串，则仪表盘登录功能可能被视为禁用或需要其他验证方式。
	SessionSecretKey          string        `mapstructure:"SESSION_SECRET_KEY"` // 【新增】Session 密钥，用于加密和验证 session cookie
}

// AppSettings 是全局配置实例
var AppSettings Settings

// Init 初始化配置
// 此函数应在应用程序启动时调用一次。
func Init() {
	// 尝试从 .env 文件加载环境变量。如果文件不存在或加载失败，则忽略错误，
	// 因为环境变量也可以直接在操作系统级别设置。
	_ = godotenv.Load()
	AppSettings = loadConfig()
}

// loadConfig 从环境变量加载配置的内部函数
// 它使用辅助函数来获取和转换环境变量，并为缺失或无效的值提供默认值。
func loadConfig() Settings {
	// 注意：对于 AdminPassword，如果环境变量 ADMIN_PASSWORD 为空，
	// getStringEnv 会返回 DefaultAdminPassword ("admin")。
	// 如果希望空环境变量表示“无密码”或“禁用密码登录”，则需要特殊处理。
	// 当前逻辑下，若要禁用密码，需将 DefaultAdminPassword 设为空，并在代码中处理空密码的情况。
	// 或者，更推荐的做法是，如果 ADMIN_PASSWORD 环境变量未设置或为空，则禁用登录或发出严重警告。
	// 这里我们暂时保持原样，但在 main.go 中会添加对默认密码的警告。
	adminPassword := getStringEnv("ADMIN_PASSWORD", DefaultAdminPassword)
	if strings.TrimSpace(os.Getenv("ADMIN_PASSWORD")) == "" && DefaultAdminPassword != "" {
		// 如果环境变量明确设置为空字符串，但默认密码不是空字符串，则可能意味着用户想禁用密码。
		// 或者，这只是用户没有设置环境变量。
		// 为简单起见，如果环境变量为空，我们就用 DefaultAdminPassword。
		// 但如果 DefaultAdminPassword 也是空，那么 adminPassword 就会是空。
		// 实际应用中，对空密码的处理需要更明确的策略（例如，完全禁用 admin 面板）。
	}

	return Settings{
		AppAPIKey:                 os.Getenv("APP_API_KEY"), // APP_API_KEY 通常用于机器间认证
		OpenRouterAPIKeys:         os.Getenv("OPENROUTER_API_KEYS"),
		DefaultModel:              getStringEnv("DEFAULT_MODEL", DefaultModel),
		OpenRouterAPIURL:          getStringEnv("OPENROUTER_API_URL", DefaultOpenRouterAPIURL),
		OpenRouterModelsURL:       getStringEnv("OPENROUTER_MODELS_URL", DefaultOpenRouterModelsURL),
		RequestTimeout:            getDurationEnv("REQUEST_TIMEOUT_SECONDS", DefaultRequestTimeoutSeconds),
		AdminAPIKey:               os.Getenv("ADMIN_API_KEY"), // 保留，可能用于非UI的管理员操作
		KeyFailureCooldown:        getDurationEnv("KEY_FAILURE_COOLDOWN_SECONDS", DefaultKeyFailureCooldownSeconds),
		KeyMaxConsecutiveFailures: getIntEnv("KEY_MAX_CONSECUTIVE_FAILURES", DefaultKeyMaxConsecutiveFailures),
		RetryWithNewKeyCount:      getIntEnv("RETRY_WITH_NEW_KEY_COUNT", DefaultRetryWithNewKeyCount),
		HealthCheckInterval:       getDurationEnv("HEALTH_CHECK_INTERVAL_SECONDS", DefaultHealthCheckIntervalSeconds),
		HTTPReferer:               getStringEnv("HTTP_REFERER", DefaultHTTPReferer),
		XTitle:                    getStringEnv("X_TITLE", DefaultXTitle),
		Port:                      getStringEnv("PORT", DefaultPort),
		LogLevel:                  getStringEnv("LOG_LEVEL", DefaultLogLevel),
		GinMode:                   getStringEnv("GIN_MODE", DefaultGinMode),
		AdminPassword:             adminPassword,                                               // 【新增】使用上面获取的 adminPassword
		SessionSecretKey:          getStringEnv("SESSION_SECRET_KEY", DefaultSessionSecretKey), // 【新增】
	}
}

// getStringEnv 辅助函数：从环境变量获取字符串。
// key: 环境变量的名称。
// defaultValue: 如果环境变量未设置或为空，则返回此默认值。
func getStringEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// getIntEnv 辅助函数：从环境变量获取整数。
// key: 环境变量的名称。
// defaultValue: 如果环境变量未设置、为空或无法解析为整数，则返回此默认值。
func getIntEnv(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil { // 如果解析失败（例如，值不是数字），记录一个警告并返回默认值
		// log.Printf("警告: 环境变量 %s 的值 '%s' 无效，将使用默认值 %d. 错误: %v\n", key, valueStr, defaultValue, err)
		return defaultValue
	}
	return value
}

// getDurationEnv 辅助函数：从环境变量获取时间段（以秒为单位的整数）。
// key: 环境变量的名称。
// defaultValueInSeconds: 如果环境变量未设置、为空、无法解析为整数或为负数，则使用此默认值（秒）。
func getDurationEnv(key string, defaultValueInSeconds int) time.Duration {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return time.Duration(defaultValueInSeconds) * time.Second
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil || value < 0 { // 时间不能为负；如果解析失败或值为负，记录警告并返回默认值
		// log.Printf("警告: 环境变量 %s 的值 '%s' 无效或为负，将使用默认值 %d 秒. 错误: %v\n", key, valueStr, defaultValueInSeconds, err)
		return time.Duration(defaultValueInSeconds) * time.Second
	}
	return time.Duration(value) * time.Second
}
