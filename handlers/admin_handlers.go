package handlers

import (
	"errors"
	"net/http"
	"openrouter_polling/apimanager" // 项目API密钥管理模块
	"openrouter_polling/config"     // 项目配置模块
	"openrouter_polling/models"     // 项目数据模型模块
	"openrouter_polling/utils"      // 项目工具函数模块
	"os"
	"runtime"       // 用于获取运行时信息 (goroutine, mem stats)
	"runtime/debug" // 用于获取GC统计信息
	"strings"       // 用于字符串操作 (例如，检查URL前缀)
	"time"          // 用于时间操作

	"github.com/gin-gonic/gin"    // Gin Web框架
	"github.com/gorilla/sessions" // Gorilla Sessions库，用于会话管理
	// Log (logrus.Logger) 和 ApiKeyMgr (apimanager.ApiKeyManager) 由 main.go 注入
	// AppStartTime 也是
)

// Store 是一个包级变量，用于存储 session 的 CookieStore 实例。
// 它将在 main.go 中初始化并配置。
var Store *sessions.CookieStore

const (
	SessionKey    = "admin-session" // Session cookie 在浏览器中存储的名称。
	IsLoggedInKey = "is_logged_in"  // 在 session 数据中标记用户登录状态的键。
	UserIDKey     = "user_id"       // (可选) 如果将来需要存储用户ID，可以使用此键。
	MaxAgeSeconds = 3600 * 24 * 7   // Session cookie 的最大有效期（例如7天）。
	SessionPath   = "/admin"        // Session cookie 的作用路径，限制为 /admin 及其子路径。
)

// LoginRequest 定义了登录请求的JSON结构体。
// `binding:"required"` 标签指示 Gin 在绑定时此字段为必需。
type LoginRequest struct {
	Password string `json:"password" binding:"required"` // 用户提交的密码。
}

// LoginHandler 处理 `/admin/login` POST 请求，用于管理员登录。
func LoginHandler(c *gin.Context) {
	var req LoginRequest
	// 解析JSON请求体到 LoginRequest 结构。
	if err := c.ShouldBindJSON(&req); err != nil {
		Log.Warnf("LoginHandler: 无效的登录请求体: %v", err)
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "请求数据无效: " + err.Error(), Type: "invalid_request_error"}})
		return
	}

	// 从配置中获取管理员密码。
	// 【重要安全提示】: 在实际生产环境中，密码不应明文存储或直接比较。
	// 应使用安全的哈希算法（如 bcrypt 或 Argon2）存储密码哈希，并比较哈希值。
	// 此处为简化示例，直接比较明文密码。
	configuredPassword := config.AppSettings.AdminPassword
	if configuredPassword == "" || configuredPassword == config.DefaultAdminPassword && os.Getenv("ADMIN_PASSWORD") == "" {
		// 如果密码未配置或仍为不安全的默认值（且环境变量也未覆盖），则拒绝登录。
		// 这是一种安全措施，强制用户设置一个安全的密码。
		// os.Getenv("ADMIN_PASSWORD") == "" 检查是为了允许用户特意将密码设置回默认值（尽管不推荐）。
		Log.Error("LoginHandler: 管理员密码 (ADMIN_PASSWORD) 未在配置中安全设置或仍为默认值。登录功能禁用。")
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "管理员账户未正确配置或密码不安全，无法登录。", Type: "config_error"}})
		return
	}

	if req.Password == configuredPassword {
		// 密码匹配成功。
		session, _ := Store.Get(c.Request, SessionKey) // 获取或创建新的 session。
		session.Values[IsLoggedInKey] = true           // 在 session 中设置登录状态为 true。
		// session.Values[UserIDKey] = "admin"         // (可选) 如果需要，可以存储用户标识。

		// 配置 session cookie 的选项。
		session.Options.MaxAge = MaxAgeSeconds // 设置 cookie 有效期。
		session.Options.HttpOnly = true        // 防止客户端 JavaScript 访问 cookie，增强安全性。
		// session.Options.Secure = true          // 【重要】在生产环境 (HTTPS) 下应设置为 true。
		// 这需要检查当前请求是否通过 HTTPS，或通过配置决定。
		// 为简单起见，此处暂不动态设置，依赖 main.go 中的全局配置。
		session.Options.Path = SessionPath              // 限制 cookie 的作用路径。
		session.Options.SameSite = http.SameSiteLaxMode // CSRF 保护。Lax 对多数情况适用。

		// 保存 session。这会将 session 数据编码并设置到客户端的 cookie 中。
		err := session.Save(c.Request, c.Writer)
		if err != nil {
			Log.Errorf("LoginHandler: 保存 session 失败: %v", err)
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: models.ErrorDetail{
				Message: "登录时发生内部错误 (无法保存会话)。", Type: "internal_server_error"}})
			return
		}
		Log.Info("LoginHandler: 管理员登录成功。")
		c.JSON(http.StatusOK, gin.H{"message": "登录成功"})
	} else {
		// 密码不匹配。
		Log.Warn("LoginHandler: 管理员登录失败，密码错误。")
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "密码错误。", Type: "authentication_error"}})
	}
}

// LogoutHandler 处理 `/admin/logout` POST 请求，用于管理员登出。
func LogoutHandler(c *gin.Context) {
	session, _ := Store.Get(c.Request, SessionKey)
	session.Values[IsLoggedInKey] = false // 清除 session 中的登录状态。
	session.Options.MaxAge = -1           // 使 cookie 立即过期，从而删除它。
	// Path 和 HttpOnly 等其他选项在 Get 时已从 Store.Options 继承，无需重设。

	err := session.Save(c.Request, c.Writer) // 保存更改（即删除 cookie）。
	if err != nil {
		Log.Errorf("LogoutHandler: 保存 session (使之过期) 失败: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "登出时发生内部错误。", Type: "internal_server_error"}})
		return
	}
	Log.Info("LogoutHandler: 管理员已登出。")
	c.JSON(http.StatusOK, gin.H{"message": "登出成功"})
}

// AuthMiddleware 是一个 Gin 中间件，用于验证需要管理员权限的路由。
// 它检查 session 中是否存在有效的登录标记。
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		session, err := Store.Get(c.Request, SessionKey)
		// Gorilla Store.Get 对于 CookieStore 通常不会返回错误，除非密钥更改或 cookie 格式损坏。
		if err != nil {
			Log.Warnf("AuthMiddleware: 获取 session 失败: %v. 可能原因：store key 更改或 cookie 损坏。", err)
			// 对于 API 请求（通常非 GET），返回 JSON 错误。
			// 对于页面请求（通常 GET 且期望 HTML），重定向到登录页。
			if c.Request.Method == http.MethodGet && strings.HasPrefix(c.Request.URL.Path, "/admin/dashboard") {
				c.Redirect(http.StatusFound, "/admin/login?reason=session_error")
				c.Abort() // 终止请求链
			} else {
				c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{Error: models.ErrorDetail{
					Message: "会话无效或已损坏，请重新登录。", Type: "authentication_error"}})
			}
			return
		}

		// 从 session 中获取登录状态。
		isLoggedIn, ok := session.Values[IsLoggedInKey].(bool)
		if !ok || !isLoggedIn {
			Log.Warnf("AuthMiddleware: 用户未登录或 session 无效。访问路径: %s", c.Request.URL.Path)
			if c.Request.Method == http.MethodGet && (strings.HasPrefix(c.Request.URL.Path, "/admin/dashboard") || c.Request.URL.Path == "/admin/") {
				c.Redirect(http.StatusFound, "/admin/login?reason=not_logged_in")
				c.Abort()
			} else {
				// 对所有其他受保护的 /admin API 端点返回 401 Unauthorized。
				c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{Error: models.ErrorDetail{
					Message: "未授权访问。请先登录。", Type: "authentication_error"}})
			}
			return
		}

		// 如果用户已登录，记录调试信息并继续处理请求链。
		Log.Debugf("AuthMiddleware: 用户已认证。继续访问路径: %s", c.Request.URL.Path)
		c.Next()
	}
}

// AddOpenRouterKeyRequest 定义了添加单个 OpenRouter API 密钥的请求体结构。
type AddOpenRouterKeyRequest struct {
	// OpenRouterAPIKey 字段包含要添加的密钥字符串，可以带权重 (例如 "sk-abc:5")。
	// `binding:"required"` 确保此字段在请求中存在。
	OpenRouterAPIKey string `json:"openrouter_api_key" binding:"required"`
}

// AddOpenRouterKeyHandler 处理 `/admin/add-key` POST 请求，用于向 ApiKeyManager 添加新的 OpenRouter API 密钥。
// 此端点受 AuthMiddleware 保护。
func AddOpenRouterKeyHandler(c *gin.Context) {
	var req AddOpenRouterKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Log.Warnf("AddOpenRouterKeyHandler: 无效的添加密钥请求体: %v", err)
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "请求数据无效: " + err.Error(), Type: "invalid_request_error"}})
		return
	}

	Log.Infof("AddOpenRouterKeyHandler: 收到添加新密钥的请求 (密钥后缀: %s)", utils.SafeSuffix(req.OpenRouterAPIKey))

	// 调用 ApiKeyManager 的 AddKey 方法。
	err := ApiKeyMgr.AddKey(req.OpenRouterAPIKey)
	if err != nil {
		Log.Errorf("AddOpenRouterKeyHandler: 添加密钥 '%s' (后缀: %s) 失败: %v",
			req.OpenRouterAPIKey, utils.SafeSuffix(req.OpenRouterAPIKey), err)

		statusCode := http.StatusInternalServerError // 默认错误状态码
		errMsg := "添加密钥时发生未知错误。"
		errType := "operation_failed"

		// 根据 ApiKeyManager 返回的特定错误类型，定制响应。
		if errors.Is(err, apimanager.ErrKeyAlreadyExists) {
			statusCode = http.StatusConflict // 409 Conflict
			errMsg = "无法添加密钥：该密钥已存在。"
			errType = "key_already_exists"
		} else if errors.Is(err, apimanager.ErrInvalidKeyFormat) {
			statusCode = http.StatusBadRequest // 400 Bad Request
			errMsg = "无法添加密钥：密钥格式无效 (例如，权重解析失败或密钥为空)。"
			errType = "invalid_key_format"
		}
		c.JSON(statusCode, models.ErrorResponse{Error: models.ErrorDetail{Message: errMsg, Type: errType}})
		return
	}

	// 添加成功。
	c.JSON(http.StatusOK, gin.H{"message": "密钥 '" + utils.SafeSuffix(req.OpenRouterAPIKey) + "' 添加成功。"})
}

// DeleteOpenRouterKeyHandler 处理 `/admin/delete-key/:suffix` DELETE 请求。
// 它根据提供的密钥后缀从 ApiKeyManager 中删除密钥。
// `:suffix` 是 URL 路径参数。此端点受 AuthMiddleware 保护。
func DeleteOpenRouterKeyHandler(c *gin.Context) {
	keySuffix := c.Param("suffix") // 从 URL 路径参数中获取 "suffix"。
	if keySuffix == "" {
		Log.Warn("DeleteOpenRouterKeyHandler: 密钥后缀参数为空。")
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "密钥后缀不能为空。", Type: "invalid_request_error", Param: "suffix"}})
		return
	}

	Log.Infof("DeleteOpenRouterKeyHandler: 收到删除后缀为 '%s' 的密钥的请求。", keySuffix)

	// 调用 ApiKeyManager 的 DeleteKeyBySuffix 方法。
	err := ApiKeyMgr.DeleteKeyBySuffix(keySuffix)
	if err != nil {
		Log.Errorf("DeleteOpenRouterKeyHandler: 删除后缀为 '%s' 的密钥失败: %v", keySuffix, err)
		statusCode := http.StatusInternalServerError // 默认错误状态码
		errMsg := "删除密钥时发生未知错误。"
		errType := "operation_failed"

		// 根据错误类型定制响应。
		if errors.Is(err, apimanager.ErrKeyNotFound) {
			statusCode = http.StatusNotFound // 404 Not Found
			errMsg = "无法删除密钥：未找到具有该后缀的密钥。"
			errType = "key_not_found"
		}
		c.JSON(statusCode, models.ErrorResponse{Error: models.ErrorDetail{Message: errMsg, Type: errType}})
		return
	}

	// 删除成功。
	c.JSON(http.StatusOK, gin.H{"message": "后缀为 '" + keySuffix + "' 的密钥已成功删除。"})
}

// GetKeyStatusesHandler 处理 `/admin/key-status` GET 请求。
// 返回当前所有 OpenRouter API 密钥的安全状态列表 (ApiKeyStatusSafe)。
// 此端点受 AuthMiddleware 保护。
func GetKeyStatusesHandler(c *gin.Context) {
	Log.Debug("GetKeyStatusesHandler: 收到获取密钥状态请求。")
	statuses := ApiKeyMgr.GetAllKeyStatusesSafe() // 从 ApiKeyManager 获取安全状态列表。
	c.JSON(http.StatusOK, statuses)
}

// AppStatusHandler 处理 `/admin/app-status` GET 请求。
// 返回应用程序的各种运行时状态和配置信息 (models.AppStatusInfo)。
// 此端点受 AuthMiddleware 保护。
func AppStatusHandler(c *gin.Context) {
	Log.Debug("AppStatusHandler: 收到获取应用状态请求。")
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats) // 读取当前内存统计信息。

	var gcStats debug.GCStats
	debug.ReadGCStats(&gcStats) // 读取垃圾回收统计信息。

	// 确定上次 GC 时间。
	lastGCTime := gcStats.LastGC                     // time.Time from debug.ReadGCStats
	if lastGCTime.IsZero() && memStats.LastGC != 0 { // 如果 debug.GCStats 未提供，尝试从 runtime.MemStats 获取
		lastGCTime = time.Unix(0, int64(memStats.LastGC)) // memStats.LastGC 是 Unix 纳秒时间戳
	}

	// 计算应用运行时长。确保 AppStartTime 已被正确设置 (在 main.go 中)。
	uptime := time.Since(AppStartTime)

	// 填充应用状态信息结构体。
	status := models.AppStatusInfo{
		StartTime:                  AppStartTime,
		Uptime:                     uptime.Round(time.Second).String(), // 四舍五入到秒，并转为字符串。
		GoVersion:                  runtime.Version(),
		NumGoroutines:              runtime.NumGoroutine(),
		MemAllocatedMB:             float64(memStats.Alloc) / 1024 / 1024,      // 当前堆内存分配
		MemTotalAllocatedMB:        float64(memStats.TotalAlloc) / 1024 / 1024, // 累计堆内存分配
		MemSysMB:                   float64(memStats.Sys) / 1024 / 1024,        // 系统总内存占用
		NumGC:                      memStats.NumGC,                             // GC 次数
		LastGC:                     lastGCTime,                                 // 上次GC时间
		OpenRouterAPIKeysProvided:  config.AppSettings.OpenRouterAPIKeys != "", // 是否配置了密钥
		DefaultModel:               config.AppSettings.DefaultModel,
		OpenRouterAPIURL:           config.AppSettings.OpenRouterAPIURL,
		OpenRouterModelsURL:        config.AppSettings.OpenRouterModelsURL,
		RequestTimeoutSeconds:      config.AppSettings.RequestTimeout.Seconds(),
		KeyFailureCooldownSeconds:  config.AppSettings.KeyFailureCooldown.Seconds(),
		KeyMaxConsecutiveFailures:  config.AppSettings.KeyMaxConsecutiveFailures,
		RetryWithNewKeyCount:       config.AppSettings.RetryWithNewKeyCount,
		HealthCheckIntervalSeconds: config.AppSettings.HealthCheckInterval.Seconds(),
		Port:                       config.AppSettings.Port,
		AdminAPIKeyConfigured:      config.AppSettings.AdminAPIKey != "", // 旧的 Header Key 方式是否配置
		// 检查 AdminPassword 是否已配置且不是不安全的默认值。
		// 这有助于管理员了解密码配置的安全性。
		AdminPasswordConfigured: config.AppSettings.AdminPassword != "" && config.AppSettings.AdminPassword != config.DefaultAdminPassword,
		LogLevel:                config.AppSettings.LogLevel,
		GinMode:                 config.AppSettings.GinMode,
	}
	c.JSON(http.StatusOK, status)
}

// ReloadOpenRouterKeysHandler 处理 `/admin/reload-keys` POST 请求。
// 使用提供的密钥字符串批量重新加载 ApiKeyManager 中的所有 OpenRouter API 密钥。
// 【注意】此功能会覆盖所有现有密钥，包括通过 AddKey 添加的密钥。
// 此端点受 AuthMiddleware 保护。
func ReloadOpenRouterKeysHandler(c *gin.Context) {
	var req models.ReloadKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Log.Warnf("ReloadOpenRouterKeysHandler: 无效的请求体: %v", err)
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "无效的请求体: " + err.Error(), Type: "invalid_request_error"}})
		return
	}

	Log.Info("ReloadOpenRouterKeysHandler: 收到管理员请求，使用提供的字符串批量重新加载 OpenRouter API 密钥。")
	ApiKeyMgr.LoadKeys(req.OpenRouterAPIKeysStr) // 调用 ApiKeyManager 的 LoadKeys 方法。
	// LoadKeys 方法内部会记录加载的密钥数量。
	c.JSON(http.StatusOK, gin.H{"message": "OpenRouter API 密钥已从提供的字符串重新加载。"})
}
