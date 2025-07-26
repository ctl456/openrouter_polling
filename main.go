package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"openrouter_polling/apimanager"
	"openrouter_polling/config"
	"openrouter_polling/handlers"
	"openrouter_polling/healthcheck"
	"openrouter_polling/middleware"
	"openrouter_polling/storage"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/sessions"
	"github.com/sirupsen/logrus"
)

var (
	log          *logrus.Logger
	apiKeyMgr    *apimanager.ApiKeyManager
	httpClient   *http.Client
	appStartTime = time.Now()
)

func main() {
	// 1. 初始化日志记录器
	log = logrus.New()
	log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05.000",
	})
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)

	// 2. 加载应用程序配置
	config.Init(log)
	if level, err := logrus.ParseLevel(config.AppSettings.LogLevel); err == nil {
		log.SetLevel(level)
	} else {
		log.Warnf("无效的 LOG_LEVEL 配置 '%s', 将使用默认 Info 级别。", config.AppSettings.LogLevel)
	}
	log.Infof("日志级别已设置为: %s", log.GetLevel().String())

	if config.AppSettings.AdminPassword == "" {
		log.Error("严重警告: 管理员密码 (ADMIN_PASSWORD) 未设置或为空! 管理仪表盘登录功能将无法使用或极不安全。请立即配置一个强密码。")
	} else if config.AppSettings.AdminPassword == config.DefaultAdminPassword {
		log.Warnf("安全警告: 管理员密码 (ADMIN_PASSWORD) 当前为默认值 '%s'。强烈建议修改为一个强密码以保证安全!", config.DefaultAdminPassword)
	}

	// 3. 初始化数据库
	db, err := storage.InitDB(log)
	if err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	log.Info("数据库初始化成功。")

	// 4. 初始化 Session Store
	sessionKey := make([]byte, 64)
	_, err = rand.Read(sessionKey)
	if err != nil {
		log.Fatalf("无法生成安全的 session 密钥: %v", err)
	}
	log.Infof("已生成临时的 session 密钥: %s", hex.EncodeToString(sessionKey))

	handlers.Store = sessions.NewCookieStore(sessionKey)
	handlers.Store.Options = &sessions.Options{
		Path:     handlers.SessionPath,
		MaxAge:   handlers.SessionMaxAgeSeconds,
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	}
	log.Info("Session Store 初始化完成。")
	if !handlers.Store.Options.Secure {
		log.Warn("Session cookie 的 Secure 标志当前为 false。如果您的服务部署在 HTTPS 环境下，请务必在生产中将其配置为 true 以增强安全性。")
	}

	// 5. 初始化全局组件
	storage.Log = log
	apimanager.Log = log
	middleware.Log = log
	handlers.Log = log
	healthcheck.Log = log

	keyStore := storage.NewKeyStore(db)
	apiKeyMgr = apimanager.NewApiKeyManager(log, keyStore)
	handlers.ApiKeyMgr = apiKeyMgr
	healthcheck.ApiKeyMgr = apiKeyMgr

	httpClient = &http.Client{
		Timeout: config.AppSettings.RequestTimeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	handlers.HttpClient = httpClient
	handlers.AppStartTime = appStartTime

	// 6. 应用启动逻辑：植入和加载密钥
	log.Info("应用程序核心服务启动中...")
	if err := apiKeyMgr.SeedKeysFromConfig(config.AppSettings.OpenRouterAPIKeys); err != nil {
		log.Fatalf("从环境变量植入密钥失败: %v", err)
	}
	if err := apiKeyMgr.LoadKeysFromDB(); err != nil {
		log.Fatalf("从数据库加载密钥失败: %v", err)
	}

	// 7. 启动后台任务
	healthCheckCtx, healthCheckCancelFunc := context.WithCancel(context.Background())
	go healthcheck.PerformPeriodicHealthChecks(healthCheckCtx)
	log.Info("API 密钥管理器已初始化，定期健康检查任务已启动。")

	// 8. 设置 Gin 路由器
	if strings.ToLower(config.AppSettings.GinMode) == "release" {
		gin.SetMode(gin.ReleaseMode)
		log.Info("Gin 运行模式: release")
	} else {
		gin.SetMode(gin.DebugMode)
		log.Info("Gin 运行模式: debug")
	}

	router := gin.New()
	router.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		return fmt.Sprintf("%s | %s | %3d | %13v | %15s | %-7s %#v %s\n%s",
			param.TimeStamp.Format("2006/01/02 - 15:04:05"),
			param.Request.Proto,
			param.StatusCode,
			param.Latency,
			param.ClientIP,
			param.Method,
			param.Path,
			param.Request.UserAgent(),
			param.ErrorMessage,
		)
	}))
	router.Use(gin.Recovery())

	templatesPath := filepath.Join("static", "*.html")
	router.LoadHTMLGlob(templatesPath)
	log.Infof("已从路径 '%s' 加载 HTML 模板。", templatesPath)

	// --- API 路由 (/v1) ---
	v1Group := router.Group("/v1")
	if config.AppSettings.AppAPIKey != "" {
		v1Group.Use(middleware.VerifyAPIKey())
		log.Info("'/v1/*' 路由组已启用 API 密钥认证 (APP_API_KEY)。")
	} else {
		log.Warn("警告: '/v1/*' 路由组未配置 API 密钥认证 (APP_API_KEY 未设置)。任何客户端都可访问。")
	}
	{
		v1Group.GET("/models", handlers.ListModelsHandler)
		v1Group.POST("/chat/completions", handlers.ChatCompletionsHandler)
	}

	// --- 管理员路由 (/admin) ---
	adminGroup := router.Group("/admin")
	{
		adminGroup.GET("/login", handlers.LoginPageHandler)
		adminGroup.POST("/login", handlers.LoginHandler)

		authorizedAdminGroup := adminGroup.Group("/")
		authorizedAdminGroup.Use(handlers.AuthMiddleware())
		{
			authorizedAdminGroup.GET("/dashboard", handlers.DashboardHandler)
			authorizedAdminGroup.POST("/logout", handlers.LogoutHandler)
			authorizedAdminGroup.GET("/key-status", handlers.GetKeyStatusesHandler)
			authorizedAdminGroup.POST("/session/heartbeat", handlers.SessionHeartbeatHandler)
			authorizedAdminGroup.POST("/add-keys", handlers.AddKeysHandler)
			authorizedAdminGroup.DELETE("/delete-key/:suffix", handlers.DeleteOpenRouterKeyHandler)
			authorizedAdminGroup.POST("/delete-keys-batch", handlers.DeleteKeysBatchHandler) // 【新增】批量删除路由
			authorizedAdminGroup.POST("/reload-keys", handlers.ReloadOpenRouterKeysHandler)
			authorizedAdminGroup.GET("/app-status", handlers.AppStatusHandler)
			// 【新增】设置页面路由
			authorizedAdminGroup.GET("/settings-page", handlers.SettingsPageHandler)
			authorizedAdminGroup.GET("/settings", handlers.GetSettingsHandler)
			authorizedAdminGroup.POST("/settings", handlers.UpdateSettingsHandler)
		}
	}
	log.Info("所有应用路由已设置完成。")

	router.GET("/favicon.ico", handlers.FaviconHandler)
	router.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/admin/login")
	})

	// 9. 启动 HTTP 服务器
	serverAddr := ":" + config.AppSettings.Port
	log.Infof("服务即将启动，监听地址: http://localhost%s (或配置的域名/IP)", serverAddr)
	srv := &http.Server{
		Addr:         serverAddr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 服务器启动失败: %s\n", err)
		}
	}()
	log.Infof("服务器正在运行中... 按 CTRL+C 关闭。")

	// 10. 实现优雅关闭
	quitChannel := make(chan os.Signal, 1)
	signal.Notify(quitChannel, syscall.SIGINT, syscall.SIGTERM)
	<-quitChannel

	log.Println("收到关闭信号，服务器正在优雅关闭...")
	healthCheckCancelFunc()

	shutdownCtx, shutdownCancelFunc := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancelFunc()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("服务器优雅关闭失败: %v", err)
	}

	log.Println("服务器已成功优雅关闭。")
}
