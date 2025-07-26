package config

import (
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
)

// --- 全局变量和常量 ---
const (
	// 默认配置值
	DefaultKeyFailureCooldownSeconds = 600
	DefaultKeyMaxConsecutiveFailures = 3
	DefaultRetryWithNewKeyCount      = 3
	DefaultHealthCheckIntervalSeconds = 60 * 5
	DefaultRequestTimeoutSeconds     = 180
	DefaultPort                      = "8000"
	DefaultLogLevel                  = "info"
	DefaultGinMode                   = "debug"
	DefaultOpenRouterAPIURL          = "https://openrouter.ai/api/v1/chat/completions"
	DefaultOpenRouterModelsURL       = "https://openrouter.ai/api/v1/models"
	DefaultModel                     = "deepseek/deepseek-chat-v3-0324:free"
	DefaultHTTPReferer               = "https://your-app-name.com"
	DefaultXTitle                    = "Your App Name"
	DefaultAdminPassword             = "admin"
	DefaultDBType                    = "sqlite"
	DefaultDBConnectionStringSqlite  = "openrouter_keys.db"
	DefaultMySQLHost                 = "127.0.0.1"
	DefaultMySQLPort                 = "3306"
	DefaultMySQLDBName               = "openrouter_proxy"
	DefaultMySQLUser                 = "root"
	DefaultMySQLPassword             = ""
)

// Settings 存储应用配置
type Settings struct {
	AppAPIKey                 string
	OpenRouterAPIKeys         string
	DefaultModel              string
	OpenRouterAPIURL          string
	OpenRouterModelsURL       string
	RequestTimeout            time.Duration
	KeyFailureCooldown        time.Duration
	KeyMaxConsecutiveFailures int
	RetryWithNewKeyCount      int
	HealthCheckInterval       time.Duration
	HTTPReferer               string
	XTitle                    string
	Port                      string
	LogLevel                  string
	GinMode                   string
	AdminPassword             string
	DBType                    string
	DBConnectionStringSqlite  string
	MySQLHost                 string
	MySQLPort                 string
	MySQLDBName               string
	MySQLUser                 string
	MySQLPassword             string
}

// --- 配置热加载支持 ---
var (
	AppSettings Settings
	configLock  = &sync.RWMutex{}
	Log         *logrus.Logger // 由 main.go 注入
)

// Init 初始化配置
func Init(logger *logrus.Logger) {
	Log = logger
	_ = godotenv.Load()
	AppSettings = loadConfig()
}

// GetSettings 安全地获取当前配置的副本。
func GetSettings() Settings {
	configLock.RLock()
	defer configLock.RUnlock()
	return AppSettings
}

// UpdateSettingsRequest 定义了可以从 API 更新的配置字段。
// 使用指针类型可以区分 "未提供" 和 "设置为空值"。
type UpdateSettingsRequest struct {
	DefaultModel              *string `json:"default_model"`
	RequestTimeoutSeconds     *int    `json:"request_timeout_seconds"`
	KeyFailureCooldownSeconds *int    `json:"key_failure_cooldown_seconds"`
	KeyMaxConsecutiveFailures *int    `json:"key_max_consecutive_failures"`
	RetryWithNewKeyCount      *int    `json:"retry_with_new_key_count"`
	HealthCheckIntervalSeconds *int   `json:"health_check_interval_seconds"`
	LogLevel                  *string `json:"log_level"`
	AppAPIKey                 *string `json:"app_api_key"`
	AdminPassword             *string `json:"admin_password"`
}

// UpdateSettings 安全地更新全局配置。
func UpdateSettings(req UpdateSettingsRequest) {
	configLock.Lock()
	defer configLock.Unlock()

	if req.DefaultModel != nil {
		AppSettings.DefaultModel = *req.DefaultModel
		Log.Infof("配置热更新: DefaultModel -> %s", AppSettings.DefaultModel)
	}
	if req.RequestTimeoutSeconds != nil {
		AppSettings.RequestTimeout = time.Duration(*req.RequestTimeoutSeconds) * time.Second
		Log.Infof("配置热更新: RequestTimeout -> %v", AppSettings.RequestTimeout)
	}
	if req.KeyFailureCooldownSeconds != nil {
		AppSettings.KeyFailureCooldown = time.Duration(*req.KeyFailureCooldownSeconds) * time.Second
		Log.Infof("配置热更新: KeyFailureCooldown -> %v", AppSettings.KeyFailureCooldown)
	}
	if req.KeyMaxConsecutiveFailures != nil {
		AppSettings.KeyMaxConsecutiveFailures = *req.KeyMaxConsecutiveFailures
		Log.Infof("配置热更新: KeyMaxConsecutiveFailures -> %d", AppSettings.KeyMaxConsecutiveFailures)
	}
	if req.RetryWithNewKeyCount != nil {
		AppSettings.RetryWithNewKeyCount = *req.RetryWithNewKeyCount
		Log.Infof("配置热更新: RetryWithNewKeyCount -> %d", AppSettings.RetryWithNewKeyCount)
	}
	if req.HealthCheckIntervalSeconds != nil {
		AppSettings.HealthCheckInterval = time.Duration(*req.HealthCheckIntervalSeconds) * time.Second
		Log.Infof("配置热更新: HealthCheckInterval -> %v", AppSettings.HealthCheckInterval)
		// 注意：健康检查的间隔热更新不会立即生效，因为它是在一个独立的 goroutine 中使用固定的 Ticker。
		// 真正的热更新需要更复杂的通道或重启 goroutine 机制。此处仅更新配置值。
		Log.Warn("HealthCheckInterval 已更新，但需要重启服务才能使新的检查间隔生效。")
	}
	if req.LogLevel != nil {
		if level, err := logrus.ParseLevel(*req.LogLevel); err == nil {
			AppSettings.LogLevel = *req.LogLevel
			Log.SetLevel(level)
			Log.Infof("配置热更新: LogLevel -> %s", AppSettings.LogLevel)
		} else {
			Log.Warnf("尝试热更新为无效的日志级别 '%s'，忽略此项更改。", *req.LogLevel)
		}
	}
	if req.AppAPIKey != nil {
		AppSettings.AppAPIKey = *req.AppAPIKey
		Log.Infof("配置热更新: AppAPIKey 已更新。")
	}
	if req.AdminPassword != nil {
		AppSettings.AdminPassword = *req.AdminPassword
		Log.Infof("配置热更新: AdminPassword 已更新。")
	}
}

// loadConfig 从环境变量加载配置
func loadConfig() Settings {
	return Settings{
		AppAPIKey:                 os.Getenv("APP_API_KEY"),
		OpenRouterAPIKeys:         os.Getenv("OPENROUTER_API_KEYS"),
		DefaultModel:              getStringEnv("DEFAULT_MODEL", DefaultModel),
		OpenRouterAPIURL:          getStringEnv("OPENROUTER_API_URL", DefaultOpenRouterAPIURL),
		OpenRouterModelsURL:       getStringEnv("OPENROUTER_MODELS_URL", DefaultOpenRouterModelsURL),
		RequestTimeout:            getDurationEnv("REQUEST_TIMEOUT_SECONDS", DefaultRequestTimeoutSeconds),
		KeyFailureCooldown:        getDurationEnv("KEY_FAILURE_COOLDOWN_SECONDS", DefaultKeyFailureCooldownSeconds),
		KeyMaxConsecutiveFailures: getIntEnv("KEY_MAX_CONSECUTIVE_FAILURES", DefaultKeyMaxConsecutiveFailures),
		RetryWithNewKeyCount:      getIntEnv("RETRY_WITH_NEW_KEY_COUNT", DefaultRetryWithNewKeyCount),
		HealthCheckInterval:       getDurationEnv("HEALTH_CHECK_INTERVAL_SECONDS", DefaultHealthCheckIntervalSeconds),
		HTTPReferer:               getStringEnv("HTTP_REFERER", DefaultHTTPReferer),
		XTitle:                    getStringEnv("X_TITLE", DefaultXTitle),
		Port:                      getStringEnv("PORT", DefaultPort),
		LogLevel:                  getStringEnv("LOG_LEVEL", DefaultLogLevel),
		GinMode:                   getStringEnv("GIN_MODE", DefaultGinMode),
		AdminPassword:             getStringEnv("ADMIN_PASSWORD", DefaultAdminPassword),
		DBType:                    getStringEnv("DB_TYPE", DefaultDBType),
		DBConnectionStringSqlite:  getStringEnv("DB_CONNECTION_STRING_SQLITE", DefaultDBConnectionStringSqlite),
		MySQLHost:                 getStringEnv("MYSQL_HOST", DefaultMySQLHost),
		MySQLPort:                 getStringEnv("MYSQL_PORT", DefaultMySQLPort),
		MySQLDBName:               getStringEnv("MYSQL_DBNAME", DefaultMySQLDBName),
		MySQLUser:                 getStringEnv("MYSQL_USER", DefaultMySQLUser),
		MySQLPassword:             os.Getenv("MYSQL_PASSWORD"), // 密码可以为空
	}
}

func getStringEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func getIntEnv(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

func getDurationEnv(key string, defaultValueInSeconds int) time.Duration {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return time.Duration(defaultValueInSeconds) * time.Second
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil || value < 0 {
		return time.Duration(defaultValueInSeconds) * time.Second
	}
	return time.Duration(value) * time.Second
}
