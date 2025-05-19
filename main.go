// main.go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath" // 用于构建平台无关的文件路径
	"strings"
	"syscall"
	"time"

	"openrouter_polling/apimanager"
	"openrouter_polling/config"
	"openrouter_polling/handlers"
	"openrouter_polling/healthcheck"
	"openrouter_polling/middleware" // middleware.VerifyAPIKey 仍然使用

	"github.com/gin-gonic/gin"
	"github.com/gorilla/sessions" // 【新增】导入 gorilla/sessions 用于会话管理
	"github.com/sirupsen/logrus"  // Logrus 日志库
)

// 全局变量声明
var (
	log          *logrus.Logger            // 全局日志记录器实例
	apiKeyMgr    *apimanager.ApiKeyManager // API 密钥管理器实例
	httpClient   *http.Client              // 全局 HTTP 客户端，用于向上游发出请求
	appStartTime = time.Now()              // 记录应用程序启动时间
)

func main() {
	// 1. 初始化日志记录器
	log = logrus.New()
	log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,                      // 显示完整时间戳
		TimestampFormat: "2006-01-02 15:04:05.000", // 时间戳格式
	})
	log.SetOutput(os.Stdout)       // 日志输出到标准输出
	log.SetLevel(logrus.InfoLevel) // 默认日志级别

	// 2. 加载应用程序配置
	config.Init() // 从环境变量或 .env 文件加载配置
	// 根据配置设置日志级别
	if level, err := logrus.ParseLevel(config.AppSettings.LogLevel); err == nil {
		log.SetLevel(level)
	} else {
		log.Warnf("无效的 LOG_LEVEL 配置 '%s', 将使用默认 Info 级别。", config.AppSettings.LogLevel)
	}
	log.Infof("日志级别已设置为: %s", log.GetLevel().String())

	// 【新增】关键安全配置检查和警告
	if config.AppSettings.AdminPassword == "" {
		log.Error("严重警告: 管理员密码 (ADMIN_PASSWORD) 未设置或为空! 管理仪表盘登录功能将无法使用或极不安全。请立即配置一个强密码。")
	} else if config.AppSettings.AdminPassword == config.DefaultAdminPassword {
		log.Warnf("安全警告: 管理员密码 (ADMIN_PASSWORD) 当前为默认值 '%s'。强烈建议修改为一个强密码以保证安全!", config.DefaultAdminPassword)
	}

	if config.AppSettings.SessionSecretKey == config.DefaultSessionSecretKey || config.AppSettings.SessionSecretKey == "" {
		log.Warnf("安全警告: Session 密钥 (SESSION_SECRET_KEY) 为默认值或未设置，这非常不安全! 请在生产环境中设置一个长且随机的密钥。")
		if config.AppSettings.SessionSecretKey == "" { // 如果为空，则强制使用一个默认的临时密钥以避免程序崩溃
			config.AppSettings.SessionSecretKey = config.DefaultSessionSecretKey // 但这仍然不安全
			log.Error("Session 密钥为空，已临时设置为默认值。这极不安全，请立即配置 SESSION_SECRET_KEY。")
		}
	}

	// 3. 初始化 Session Store 【新增】
	// sessionKey 用于签名和加密 cookie。它应该是随机的、保密的，并且足够长（建议32或64字节）。
	var sessionKeyBytes = []byte(config.AppSettings.SessionSecretKey)
	// handlers.Store 是在 handlers 包中定义的全局变量
	handlers.Store = sessions.NewCookieStore(sessionKeyBytes)
	handlers.Store.Options = &sessions.Options{
		Path:     handlers.SessionPath,   // 限制 cookie 只对 /admin 路径有效 (来自 handlers 常量)
		MaxAge:   handlers.MaxAgeSeconds, // Session 有效期 (来自 handlers 常量)
		HttpOnly: true,                   // JS无法访问 cookie，增强安全性
		Secure:   false,                  // 【重要】生产环境如果是 HTTPS，这里应该为 true。可通过配置控制。
		SameSite: http.SameSiteLaxMode,   // SameSite 设置，有助于 CSRF 防护。
	}
	log.Info("Session Store 初始化完成。")
	if !handlers.Store.Options.Secure { // 根据实际部署情况调整
		log.Warn("Session cookie 的 Secure 标志当前为 false。如果您的服务部署在 HTTPS 环境下，请务必在生产中将其配置为 true 以增强安全性。")
	}

	// 4. 初始化全局组件并将日志记录器传递给需要的包
	// 将主日志实例传递给其他包，以便它们可以使用相同的日志配置。
	apimanager.Log = log  // 为 apimanager 包设置日志记录器
	middleware.Log = log  // 为 middleware 包设置日志记录器
	handlers.Log = log    // 为 handlers 包设置日志记录器
	healthcheck.Log = log // 为 healthcheck 包设置日志记录器

	// 初始化 API 密钥管理器
	apiKeyMgr = apimanager.NewApiKeyManager(log)
	handlers.ApiKeyMgr = apiKeyMgr    // 将 apiKeyMgr 实例注入到 handlers 包
	healthcheck.ApiKeyMgr = apiKeyMgr // 将 apiKeyMgr 实例注入到 healthcheck 包

	// 初始化 HTTP 客户端
	// 配置合理的 Transport 参数对于性能和资源使用很重要。
	httpClient = &http.Client{
		Timeout: config.AppSettings.RequestTimeout, // 全局请求超时
		Transport: &http.Transport{
			MaxIdleConns:        100,              // 最大空闲连接数
			MaxIdleConnsPerHost: 20,               // 每个主机的最大空闲连接数
			IdleConnTimeout:     90 * time.Second, // 空闲连接超时时间
			// TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // 如果需要跳过TLS验证（不推荐用于生产）
		},
	}
	handlers.HttpClient = httpClient     // 将 httpClient 实例注入到 handlers 包
	handlers.AppStartTime = appStartTime // 将应用启动时间注入 handlers 包，用于状态报告

	// 5. 应用启动逻辑
	log.Info("应用程序核心服务启动中...")
	if config.AppSettings.OpenRouterAPIKeys == "" {
		log.Error("严重配置错误: OPENROUTER_API_KEYS 环境变量未设置或为空。服务可能无法正常代理请求。")
	} else {
		apiKeyMgr.LoadKeys(config.AppSettings.OpenRouterAPIKeys) // 从配置加载初始API密钥
	}

	// 6. 启动后台任务 (如健康检查)
	// 使用 context 控制健康检查 goroutine 的生命周期，以便在应用关闭时能够优雅停止。
	healthCheckCtx, healthCheckCancelFunc := context.WithCancel(context.Background())
	go healthcheck.PerformPeriodicHealthChecks(healthCheckCtx) // 在新的 goroutine 中运行健康检查
	log.Info("API 密钥管理器已初始化，定期健康检查任务已启动。")

	// 7. 设置 Gin 路由器
	// 根据配置设置 Gin 的运行模式 (debug 或 release)。
	// Release 模式性能更好，日志更少。Debug 模式输出更详细。
	if strings.ToLower(config.AppSettings.GinMode) == "release" {
		gin.SetMode(gin.ReleaseMode)
		log.Info("Gin 运行模式: release")
	} else {
		gin.SetMode(gin.DebugMode) // 默认为 debug 模式
		log.Info("Gin 运行模式: debug")
	}

	router := gin.New() // 创建一个新的 Gin 引擎，不带默认中间件 (gin.Default() 会带 Logger 和 Recovery)
	// 自定义日志中间件
	router.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		// 自定义日志格式
		return fmt.Sprintf("%s | %s | %3d | %13v | %15s | %-7s %#v %s\n%s",
			param.TimeStamp.Format("2006/01/02 - 15:04:05"), // 时间戳
			param.Request.Proto,                             // HTTP协议版本
			param.StatusCode,                                // HTTP状态码
			param.Latency,                                   // 请求处理延迟
			param.ClientIP,                                  // 客户端IP
			param.Method,                                    // HTTP方法
			param.Path,                                      // 请求路径
			param.Request.UserAgent(),                       // User-Agent
			param.ErrorMessage,                              // 错误信息（如果有）
		)
	}))
	router.Use(gin.Recovery()) // 使用 Gin 的 Recovery 中间件来捕获 panic 并恢复

	// 加载 HTML 模板 【修改】
	// 从 "static" 目录下加载所有 .html 文件作为模板。
	templatesPath := filepath.Join("static", "*.html")
	router.LoadHTMLGlob(templatesPath)
	log.Infof("已从路径 '%s' 加载 HTML 模板。", templatesPath)

	// --- 静态文件服务 (可选) ---
	// 如果 HTML 文件引用了位于 "static" 目录下的 CSS, JS 或图片文件，则需要此配置。
	// 例如，如果有一个 static/css/style.css 文件，可以通过 /static/css/style.css 访问。
	// router.Static("/static", "./static") // URL前缀 /static 映射到 ./static 目录

	// --- API 路由 (/v1) ---
	// 这些是代理到 OpenRouter 的主要 API 端点。
	v1Group := router.Group("/v1")
	// 如果配置了 AppAPIKey，则使用 VerifyAPIKey 中间件保护 /v1 路由组。
	if config.AppSettings.AppAPIKey != "" {
		v1Group.Use(middleware.VerifyAPIKey())
		log.Info("'/v1/*' 路由组已启用 API 密钥认证 (APP_API_KEY)。")
	} else {
		log.Warn("警告: '/v1/*' 路由组未配置 API 密钥认证 (APP_API_KEY 未设置)。任何客户端都可访问。")
	}
	{
		v1Group.GET("/models", handlers.ListModelsHandler)                 // 获取模型列表
		v1Group.POST("/chat/completions", handlers.ChatCompletionsHandler) // 处理聊天请求
	}

	// --- 管理员路由 (/admin) 【修改】 ---
	// 这个路由组用于管理仪表盘和相关操作。
	adminGroup := router.Group("/admin")
	{
		// 登录页面 (GET) 和登录处理 (POST) 本身不需要认证。
		adminGroup.GET("/login", handlers.LoginPageHandler) // 提供登录页面 HTML
		adminGroup.POST("/login", handlers.LoginHandler)    // 处理登录表单提交

		// 以下是需要认证才能访问的管理接口和页面。
		// 创建一个新的子路由组，并对其应用 AuthMiddleware。
		authorizedAdminGroup := adminGroup.Group("/")
		authorizedAdminGroup.Use(handlers.AuthMiddleware()) // 应用会话认证中间件
		{
			authorizedAdminGroup.GET("/dashboard", handlers.DashboardHandler)                       // 提供仪表盘页面 HTML
			authorizedAdminGroup.POST("/logout", handlers.LogoutHandler)                            // 处理登出请求
			authorizedAdminGroup.GET("/key-status", handlers.GetKeyStatusesHandler)                 // 获取所有API密钥的状态
			authorizedAdminGroup.POST("/add-key", handlers.AddOpenRouterKeyHandler)                 // 添加新的API密钥
			authorizedAdminGroup.DELETE("/delete-key/:suffix", handlers.DeleteOpenRouterKeyHandler) // 根据后缀删除API密钥

			// 如果仍需保留旧的批量重载和应用状态接口，并将它们置于会话保护下：
			authorizedAdminGroup.POST("/reload-keys", handlers.ReloadOpenRouterKeysHandler) // 批量重载密钥
			authorizedAdminGroup.GET("/app-status", handlers.AppStatusHandler)              // 获取应用状态信息
		}
	}
	log.Info("所有应用路由已设置完成。")

	// 【新增】处理对 /favicon.ico 的请求，避免日志中出现404。
	router.GET("/favicon.ico", handlers.FaviconHandler)
	// 可选：根路径重定向到登录页或仪表盘（如果已登录）
	router.GET("/", func(c *gin.Context) {
		// 简单地重定向到 /admin/login。更复杂的逻辑可以检查是否已登录并重定向到 /admin/dashboard。
		c.Redirect(http.StatusMovedPermanently, "/admin/login")
	})

	// 8. 启动 HTTP 服务器
	serverAddr := ":" + config.AppSettings.Port
	log.Infof("服务即将启动，监听地址: http://localhost%s (或配置的域名/IP)", serverAddr)
	srv := &http.Server{
		Addr:         serverAddr,
		Handler:      router,            // 使用配置好的 Gin 引擎作为处理器
		ReadTimeout:  15 * time.Second,  // 读取超时
		WriteTimeout: 300 * time.Second, // 写入超时（对于流式响应可能需要较长时间）
		IdleTimeout:  120 * time.Second, // Keep-Alive 空闲连接超时
	}

	// 在 goroutine 中启动服务器，以便非阻塞地处理关闭信号。
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 服务器启动失败: %s\n", err)
		}
	}()
	log.Infof("服务器正在运行中... 按 CTRL+C 关闭。")

	// 9. 实现优雅关闭
	// 等待中断信号 (SIGINT) 或终止信号 (SIGTERM)。
	quitChannel := make(chan os.Signal, 1) // 创建一个缓冲通道，大小为1
	signal.Notify(quitChannel, syscall.SIGINT, syscall.SIGTERM)
	<-quitChannel // 阻塞，直到收到信号

	log.Println("收到关闭信号，服务器正在优雅关闭...")

	// 取消健康检查等后台任务的上下文。
	healthCheckCancelFunc() // 调用之前保存的取消函数

	// 创建一个带超时的上下文，用于服务器关闭。
	// 例如，给服务器10秒钟来完成当前正在处理的请求。
	shutdownCtx, shutdownCancelFunc := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancelFunc()

	// 调用服务器的 Shutdown 方法。
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("服务器优雅关闭失败: %v", err)
	}

	log.Println("服务器已成功优雅关闭。")
}
