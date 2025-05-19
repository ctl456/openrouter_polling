package middleware

import (
	"net/http"
	"openrouter_polling/config" // 项目配置包
	"openrouter_polling/models" // 项目模型包，包含 ErrorResponse 等
	"strings"                   // 用于字符串操作

	"github.com/gin-gonic/gin"   // Gin Web 框架
	"github.com/sirupsen/logrus" // Logrus 日志库
)

// Log 是一个包级变量，用于日志记录。它应该由外部（例如 main.go）设置。
var Log *logrus.Logger

// VerifyAPIKey 是一个 Gin 中间件，用于验证访问 `/v1/*` API 端点的客户端请求。
// 它检查 Authorization 头部是否包含有效的 Bearer Token，该 Token 必须与配置中的 `AppAPIKey` 匹配。
// 如果 `config.AppSettings.AppAPIKey` 为空，则此中间件应被视为禁用（在 `main.go` 中控制是否应用）。
func VerifyAPIKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		// AppAPIKey 的配置检查应在 main.go 中决定是否实际应用此中间件。
		// 如果 AppAPIKey 未配置，此中间件根本不应该被注册到路由上。
		// 因此，这里假设如果中间件被调用，AppAPIKey 就是被配置了且需要验证。
		if config.AppSettings.AppAPIKey == "" {
			// 此情况理论上不应发生，因为 main.go 中会根据 AppAPIKey 是否配置来决定是否使用此中间件。
			// 但作为防御性编程，如果意外执行到这里且 AppAPIKey 为空，则认为配置错误，允许请求通过并记录警告。
			// 或者，更严格的做法是直接拒绝请求。为了安全，我们选择拒绝。
			Log.Error("VerifyAPIKey 中间件被调用，但 AppAPIKey 未配置。拒绝请求。")
			c.AbortWithStatusJSON(http.StatusInternalServerError, models.ErrorResponse{
				Error: models.ErrorDetail{Message: "服务配置错误，无法验证API密钥", Type: "server_error", Code: "config_error_app_api_key_missing"},
			})
			return
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			Log.Warn("VerifyAPIKey: 请求缺少 Authorization 头部。")
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
				Error: models.ErrorDetail{Message: "需要提供 API 密钥才能访问此服务。", Type: "authentication_error", Code: "missing_api_key"},
			})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2) // 按空格分割，最多两部分
		// 检查格式是否为 "Bearer <token>"
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
			Log.Warnf("VerifyAPIKey: Authorization 头部格式无效。收到: '%s'", authHeader)
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
				Error: models.ErrorDetail{Message: "无效的授权方案或令牌缺失。请使用 'Bearer <token>' 格式。", Type: "authentication_error", Code: "invalid_auth_scheme"},
			})
			return
		}

		// 比较提供的 token 和配置的 AppAPIKey
		if parts[1] != config.AppSettings.AppAPIKey {
			Log.Warnf("VerifyAPIKey: 无效的服务 API 密钥。收到 token 后缀: ...%s", safeSuffixForLog(parts[1], 6)) // 日志中显示密钥末尾几位
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
				Error: models.ErrorDetail{Message: "提供的 API 密钥无效。", Type: "authentication_error", Code: "invalid_api_key"},
			})
			return
		}

		Log.Debug("VerifyAPIKey: 服务 API 密钥验证成功。")
		c.Next() // 验证通过，继续处理请求
	}
}

// safeSuffixForLog 是一个内部辅助函数，用于安全地获取字符串的末尾部分以用于日志记录。
// s: 输入字符串。
// length: 要显示的末尾字符数。
// 返回: 形如 "...suffix" 的字符串，或在特殊情况下返回 "[EMPTY]" 或原样返回（如果字符串很短）。
func safeSuffixForLog(s string, length int) string {
	if length <= 0 { // 如果请求长度不合法，返回空字符串或错误提示
		return "[INVALID_LENGTH_FOR_LOG]"
	}
	strLen := len(s)
	if strLen == 0 {
		return "[EMPTY]"
	}
	if strLen > length {
		return s[strLen-length:] // 只返回末尾 'length' 个字符（前面不加 "..."，以便于日志grep）
		// 如果希望和 utils.SafeSuffix 一致，使用: return "..." + s[strLen-length:]
	}
	// 如果字符串本身就比请求的 length 短或相等
	return s // 返回完整字符串（前面不加 "..."）
	// 如果希望和 utils.SafeSuffix 一致，使用: return "..." + s
}
