package handlers

import (
	"errors"
	"net/http"
	"openrouter_polling/apimanager"
	"openrouter_polling/config"
	"openrouter_polling/models"

	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/sessions"
)

var Store *sessions.CookieStore

const (
	SessionKey    = "admin-session"
	IsLoggedInKey = "is_logged_in"
	UserIDKey     = "user_id"
	MaxAgeSeconds = 3600 * 24 * 7
	SessionPath   = "/admin"
)

type LoginRequest struct {
	Password string `json:"password" binding:"required"`
}

func LoginHandler(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Log.Warnf("LoginHandler: 无效的登录请求体: %v", err)
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "请求数据无效: " + err.Error(), Type: "invalid_request_error"}})
		return
	}

	configuredPassword := config.AppSettings.AdminPassword
	if configuredPassword == "" || configuredPassword == config.DefaultAdminPassword && os.Getenv("ADMIN_PASSWORD") == "" {
		Log.Error("LoginHandler: 管理员密码 (ADMIN_PASSWORD) 未在配置中安全设置或仍为默认值。登录功能禁用。")
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "管理员账户未正确配置或密码不安全，无法登录。", Type: "config_error"}})
		return
	}

	if req.Password == configuredPassword {
		session, _ := Store.Get(c.Request, SessionKey)
		session.Values[IsLoggedInKey] = true
		session.Options.MaxAge = MaxAgeSeconds
		session.Options.HttpOnly = true
		session.Options.Path = SessionPath
		session.Options.SameSite = http.SameSiteLaxMode

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
		Log.Warn("LoginHandler: 管理员登录失败，密码错误。")
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "密码错误。", Type: "authentication_error"}})
	}
}

func LogoutHandler(c *gin.Context) {
	session, _ := Store.Get(c.Request, SessionKey)
	session.Values[IsLoggedInKey] = false
	session.Options.MaxAge = -1

	err := session.Save(c.Request, c.Writer)
	if err != nil {
		Log.Errorf("LogoutHandler: 保存 session (使之过期) 失败: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "登出时发生内部错误。", Type: "internal_server_error"}})
		return
	}
	Log.Info("LogoutHandler: 管理员已登出。")
	c.JSON(http.StatusOK, gin.H{"message": "登出成功"})
}

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		session, err := Store.Get(c.Request, SessionKey)
		if err != nil {
			Log.Warnf("AuthMiddleware: 获取 session 失败: %v. 可能原因：store key 更改或 cookie 损坏。", err)
			if c.Request.Method == http.MethodGet && strings.HasPrefix(c.Request.URL.Path, "/admin/dashboard") {
				c.Redirect(http.StatusFound, "/admin/login?reason=session_error")
				c.Abort()
			} else {
				c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{Error: models.ErrorDetail{
					Message: "会话无效或已损坏，请重新登录。", Type: "authentication_error"}})
			}
			return
		}

		isLoggedIn, ok := session.Values[IsLoggedInKey].(bool)
		if !ok || !isLoggedIn {
			Log.Warnf("AuthMiddleware: 用户未登录或 session 无效。访问路径: %s", c.Request.URL.Path)
			if c.Request.Method == http.MethodGet && (strings.HasPrefix(c.Request.URL.Path, "/admin/dashboard") || c.Request.URL.Path == "/admin/") {
				c.Redirect(http.StatusFound, "/admin/login?reason=not_logged_in")
				c.Abort()
			} else {
				c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{Error: models.ErrorDetail{
					Message: "未授权访问。请先登录。", Type: "authentication_error"}})
			}
			return
		}
		Log.Debugf("AuthMiddleware: 用户已认证。继续访问路径: %s", c.Request.URL.Path)
		c.Next()
	}
}

// 【修改】AddKeysRequest 定义了添加一个或多个 OpenRouter API 密钥的请求体结构。
type AddKeysRequest struct {
	KeyData string `json:"key_data" binding:"required"`
}

// 【修改】AddKeysHandler 处理 `/admin/add-keys` POST 请求，用于向 ApiKeyManager 添加新的 OpenRouter API 密钥（单个或批量）。
func AddKeysHandler(c *gin.Context) {
	var req AddKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Log.Warnf("AddKeysHandler: 无效的添加密钥请求体: %v", err)
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "请求数据无效: " + err.Error(), Type: "invalid_request_error"}})
		return
	}

	Log.Infof("AddKeysHandler: 收到添加新密钥的请求。")

	result, err := ApiKeyMgr.AddKeysBatch(req.KeyData)
	if err != nil {
		Log.Errorf("AddKeysHandler: AddKeysBatch 方法返回严重错误: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "添加密钥时发生内部服务器错误。", Type: "internal_server_error"}})
		return
	}

	c.JSON(http.StatusOK, result)
}

// DeleteOpenRouterKeyHandler 处理 `/admin/delete-key/:suffix` DELETE 请求。
func DeleteOpenRouterKeyHandler(c *gin.Context) {
	keySuffix := c.Param("suffix")
	if keySuffix == "" {
		Log.Warn("DeleteOpenRouterKeyHandler: 密钥后缀参数为空。")
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "密钥后缀不能为空。", Type: "invalid_request_error", Param: "suffix"}})
		return
	}

	Log.Infof("DeleteOpenRouterKeyHandler: 收到删除后缀为 '%s' 的密钥的请求。", keySuffix)

	err := ApiKeyMgr.DeleteKeyBySuffix(keySuffix)
	if err != nil {
		Log.Errorf("DeleteOpenRouterKeyHandler: 删除后缀为 '%s' 的密钥失败: %v", keySuffix, err)
		statusCode := http.StatusInternalServerError
		errMsg := "删除密钥时发生未知错误。"
		errType := "operation_failed"

		if errors.Is(err, apimanager.ErrKeyNotFound) {
			statusCode = http.StatusNotFound
			errMsg = "无法删除密钥：未找到具有该后缀的密钥。"
			errType = "key_not_found"
		}
		c.JSON(statusCode, models.ErrorResponse{Error: models.ErrorDetail{Message: errMsg, Type: errType}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "后缀为 '" + keySuffix + "' 的密钥已成功删除。"})
}

// GetKeyStatusesHandler 处理 `/admin/key-status` GET 请求。
func GetKeyStatusesHandler(c *gin.Context) {
	Log.Debug("GetKeyStatusesHandler: 收到获取密钥状态请求。")
	statuses := ApiKeyMgr.GetAllKeyStatusesSafe()
	c.JSON(http.StatusOK, statuses)
}

// AppStatusHandler 处理 `/admin/app-status` GET 请求。
func AppStatusHandler(c *gin.Context) {
	Log.Debug("AppStatusHandler: 收到获取应用状态请求。")
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	var gcStats debug.GCStats
	debug.ReadGCStats(&gcStats)

	lastGCTime := gcStats.LastGC
	if lastGCTime.IsZero() && memStats.LastGC != 0 {
		lastGCTime = time.Unix(0, int64(memStats.LastGC))
	}

	uptime := time.Since(AppStartTime)

	status := models.AppStatusInfo{
		StartTime:                  AppStartTime,
		Uptime:                     uptime.Round(time.Second).String(),
		GoVersion:                  runtime.Version(),
		NumGoroutines:              runtime.NumGoroutine(),
		MemAllocatedMB:             float64(memStats.Alloc) / 1024 / 1024,
		MemTotalAllocatedMB:        float64(memStats.TotalAlloc) / 1024 / 1024,
		MemSysMB:                   float64(memStats.Sys) / 1024 / 1024,
		NumGC:                      memStats.NumGC,
		LastGC:                     lastGCTime,
		OpenRouterAPIKeysProvided:  config.AppSettings.OpenRouterAPIKeys != "",
		DefaultModel:               config.AppSettings.DefaultModel,
		OpenRouterAPIURL:           config.AppSettings.OpenRouterAPIURL,
		OpenRouterModelsURL:        config.AppSettings.OpenRouterModelsURL,
		RequestTimeoutSeconds:      config.AppSettings.RequestTimeout.Seconds(),
		KeyFailureCooldownSeconds:  config.AppSettings.KeyFailureCooldown.Seconds(),
		KeyMaxConsecutiveFailures:  config.AppSettings.KeyMaxConsecutiveFailures,
		RetryWithNewKeyCount:       config.AppSettings.RetryWithNewKeyCount,
		HealthCheckIntervalSeconds: config.AppSettings.HealthCheckInterval.Seconds(),
		Port:                       config.AppSettings.Port,
		AdminAPIKeyConfigured:      config.AppSettings.AdminAPIKey != "",
		AdminPasswordConfigured: config.AppSettings.AdminPassword != "" && config.AppSettings.AdminPassword != config.DefaultAdminPassword,
		LogLevel:                   config.AppSettings.LogLevel,
		GinMode:                    config.AppSettings.GinMode,
	}
	c.JSON(http.StatusOK, status)
}

// ReloadOpenRouterKeysHandler 处理 `/admin/reload-keys` POST 请求。
func ReloadOpenRouterKeysHandler(c *gin.Context) {
	var req models.ReloadKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Log.Warnf("ReloadOpenRouterKeysHandler: 无效的请求体: %v", err)
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "无效的请求体: " + err.Error(), Type: "invalid_request_error"}})
		return
	}

	Log.Info("ReloadOpenRouterKeysHandler: 收到管理员请求，使用提供的字符串批量重新加载 OpenRouter API 密钥。")
	ApiKeyMgr.LoadKeys(req.OpenRouterAPIKeysStr)
	c.JSON(http.StatusOK, gin.H{"message": "OpenRouter API 密钥已从提供的字符串重新加载。"})
}
