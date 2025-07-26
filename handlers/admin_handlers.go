package handlers

import (
	"errors"
	"net/http"
	"openrouter_polling/apimanager"
	"openrouter_polling/config"
	"openrouter_polling/models"
	"strconv"

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
	SessionKey           = "admin-session"
	IsLoggedInKey        = "is_logged_in"
	UserIDKey            = "user_id"
	SessionMaxAgeSeconds = 60 * 5 // 延长会话时间到5分钟
	SessionPath          = "/admin"
)

type LoginRequest struct {
	Password string `json:"password" binding:"required"`
}

// 【新增】批量删除密钥的请求体
type BatchDeleteRequest struct {
	Suffixes []string `json:"suffixes" binding:"required"`
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
		session.Options.MaxAge = SessionMaxAgeSeconds
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

// SessionHeartbeatHandler handles the session refresh request from the dashboard.
func SessionHeartbeatHandler(c *gin.Context) {
	// The AuthMiddleware already handles session validation and refreshing.
	// If the request reaches here, the session is valid and has been refreshed.
	c.JSON(http.StatusOK, gin.H{"status": "session_refreshed"})
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

		// Refresh the session on each authenticated request.
		session.Options.MaxAge = SessionMaxAgeSeconds // Reset the expiration time
		if err := session.Save(c.Request, c.Writer); err != nil {
			// If saving fails, it's a server error, but we might not want to kill the request.
			// Log it and continue, as the user is already authenticated for this request.
			Log.Errorf("AuthMiddleware: 刷新 session 失败: %v", err)
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

// DeleteKeysBatchHandler 【新增】处理批量删除密钥的请求
func DeleteKeysBatchHandler(c *gin.Context) {
	var req BatchDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Log.Warnf("DeleteKeysBatchHandler: 无效的批量删除请求体: %v", err)
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "请求数据无效: " + err.Error(), Type: "invalid_request_error"}})
		return
	}

	if len(req.Suffixes) == 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "要删除的密钥后缀列表不能为空。", Type: "invalid_request_error"}})
		return
	}

	Log.Infof("DeleteKeysBatchHandler: 收到批量删除 %d 个密钥的请求。", len(req.Suffixes))

	deletedCount, err := ApiKeyMgr.DeleteKeysBySuffixBatch(req.Suffixes)
	if err != nil {
		Log.Errorf("DeleteKeysBatchHandler: 批量删除密钥失败: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "批量删除密钥时发生内部服务器错误。", Type: "internal_server_error"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "批量删除操作完成。",
		"deleted_count": deletedCount,
		"requested_count": len(req.Suffixes),
	})
}


// GetKeyStatusesHandler 【修改】处理 `/admin/key-status` GET 请求，支持分页
func GetKeyStatusesHandler(c *gin.Context) {
	pageStr := c.DefaultQuery("page", "1")
	limitStr := c.DefaultQuery("limit", "10")

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 1 {
		limit = 10
	}
	if limit > 100 { // 防止一次请求过多数据
		limit = 100
	}

	Log.Debugf("GetKeyStatusesHandler: 收到获取密钥状态请求 (Page: %d, Limit: %d)。", page, limit)
	
	paginatedResult, err := ApiKeyMgr.GetAllKeyStatusesSafePaginated(page, limit)
	if err != nil {
		Log.Errorf("GetKeyStatusesHandler: 获取分页密钥状态失败: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "获取密钥状态时发生内部错误。", Type: "internal_server_error"}})
		return
	}

	c.JSON(http.StatusOK, paginatedResult)
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
		AdminPasswordConfigured:    config.AppSettings.AdminPassword != "" && config.AppSettings.AdminPassword != config.DefaultAdminPassword,
		LogLevel:                   config.AppSettings.LogLevel,
		GinMode:                    config.AppSettings.GinMode,
	}
	c.JSON(http.StatusOK, status)
}

// ReloadOpenRouterKeysHandler 处理 `/admin/reload-keys` POST 请求。
// 这是一个破坏性操作，会删除所有现有密钥并从提供的字符串中重新加载。
func ReloadOpenRouterKeysHandler(c *gin.Context) {
	var req models.ReloadKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Log.Warnf("ReloadOpenRouterKeysHandler: 无效的请求体: %v", err)
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "无效的请求体: " + err.Error(), Type: "invalid_request_error"}})
		return
	}

	Log.Warn("ReloadOpenRouterKeysHandler: 收到管理员请求，将从提供的字符串中进行破坏性重新加载。")

	result, err := ApiKeyMgr.ReloadKeysFromString(req.OpenRouterAPIKeysStr)
	if err != nil {
		Log.Errorf("ReloadOpenRouterKeysHandler: 重新加载密钥失败: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: models.ErrorDetail{
			Message: "重新加载密钥时发生内部服务器错误。", Type: "internal_server_error"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":         "密钥已成功重新加载。",
		"added_count":     result.AddedCount,
		"invalid_count":   result.InvalidCount,
		"error_messages":  result.ErrorMessages,
	})
}