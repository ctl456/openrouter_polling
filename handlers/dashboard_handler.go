package handlers

import (
	"net/http"
	// "openrouter_polling/config" // 不再需要直接访问 config 来获取 AdminAPIKey

	"github.com/gin-gonic/gin"
	// Log (logrus.Logger) 由 main.go 或 api_handlers.go 设置和注入
	// Store (*sessions.CookieStore) 也在 main.go 初始化并赋给此包的 Store 变量
)

// DashboardHandler 服务于 `/admin/dashboard` GET 请求，提供密钥管理面板的 HTML 页面。
// 此处理器现在假设 AuthMiddleware (在 admin_handlers.go 中定义) 已经处理了用户认证。
// 如果请求能够到达这里，说明用户已经被认证并且其会话是有效的。
func DashboardHandler(c *gin.Context) {
	// AuthMiddleware 应该已经验证了 session。
	// 如果能执行到这里，说明用户已登录。
	Log.Debug("DashboardHandler: 正在为已认证用户提供仪表盘页面 (dashboard.html)。")
	// dashboard.html 页面本身不再需要从后端接收 AdminAPIKey 或其他认证相关信息，
	// 因为所有受保护的API调用都将依赖于浏览器发送的会话cookie。
	c.HTML(http.StatusOK, "dashboard.html", nil)
}

// LoginPageHandler 服务于 `/admin/login` GET 请求，提供登录页面的 HTML。
func LoginPageHandler(c *gin.Context) {
	Log.Debug("LoginPageHandler: 正在提供登录页面 (login.html)。")

	// 检查用户是否已经登录。如果已登录，则直接重定向到仪表盘页面。
	// 这可以防止已登录用户再次看到登录页面。
	session, err := Store.Get(c.Request, SessionKey)
	// Store.Get 对于 CookieStore 理论上不应返回错误，除非 session key 改变或 cookie 损坏。
	if err != nil {
		Log.Warnf("LoginPageHandler: 获取 session 失败: %v。这可能表示 cookieStore 密钥已更改或 cookie 已损坏。", err)
		// 作为一种恢复策略，可以尝试使可能损坏的 cookie 过期。
		session.Options.MaxAge = -1           // 使 cookie 立即过期
		_ = session.Save(c.Request, c.Writer) // 忽略保存错误，因为我们无论如何都要显示登录页
		// 即使出错，也继续提供登录页面。
	} else { // 成功获取 session (或创建了新 session)
		if isLoggedIn, ok := session.Values[IsLoggedInKey].(bool); ok && isLoggedIn {
			Log.Debug("LoginPageHandler: 用户已登录，重定向到仪表盘 (/admin/dashboard)。")
			c.Redirect(http.StatusFound, "/admin/dashboard")
			return // 终止此处理函数，因为已重定向。
		}
	}

	// 从 URL 查询参数中获取 "reason"，用于在登录页面上显示相应的提示消息。
	// 例如，如果因会话过期而被重定向到登录页，URL可能是 /admin/login?reason=session_expired。
	reason := c.Query("reason")
	var initialMessage string // 要在登录页面上显示的消息
	var messageType string    // 消息的类型 (例如 "info", "success", "error")，用于前端样式

	switch reason {
	case "session_expired":
		initialMessage = "您的会话已过期，请重新登录。"
		messageType = "info"
	case "not_logged_in":
		initialMessage = "请先登录以访问受保护的区域。"
		messageType = "info"
	case "logged_out":
		initialMessage = "您已成功退出登录。"
		messageType = "success"
	case "session_error":
		initialMessage = "会话处理时发生错误，请重新登录。"
		messageType = "error"
	default:
		// 如果没有特定原因，则不显示初始消息。
	}

	// 将消息和消息类型传递给 login.html 模板。
	// 模板可以使用这些变量来显示初始提示。
	c.HTML(http.StatusOK, "login.html", gin.H{
		"InitialMessage": initialMessage, // 传递给模板的消息内容
		"MessageType":    messageType,    // 消息类型，用于模板中的样式控制
	})
}

// FaviconHandler 【新增，可选】处理对 `/favicon.ico` 的请求。
// 许多浏览器会自动请求此文件。提供一个处理器可以避免在日志中看到不必要的 404 错误。
func FaviconHandler(c *gin.Context) {
	// 可以选择返回一个实际的 favicon.ico 文件，或者一个空的响应。
	// 返回 204 No Content 表示服务器没有 favicon，浏览器通常会停止请求此资源。
	// 如果有 favicon 文件，例如在 "static/favicon.ico"，可以使用:
	// c.File("./static/favicon.ico")
	c.Status(http.StatusNoContent) // 当前选择返回 204 No Content。
}
