package storage

import (
	"fmt"
	"openrouter_polling/config"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var (
	DB  *gorm.DB
	Log *logrus.Logger
)

// InitDB 根据应用配置初始化数据库连接。
func InitDB(logger *logrus.Logger) (*gorm.DB, error) {
	Log = logger
	var err error
	var dsn string

	dbType := config.AppSettings.DBType
	Log.Infof("正在初始化数据库，类型: %s", dbType)

	// GORM 日志配置
	gormLogLevel := gormlogger.Silent
	if Log.GetLevel() >= logrus.DebugLevel {
		gormLogLevel = gormlogger.Info
	}
	newLogger := gormlogger.New(
		Log, // io writer
		gormlogger.Config{
			SlowThreshold:             time.Second,
			LogLevel:                  gormLogLevel,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)

	gormConfig := &gorm.Config{
		Logger: newLogger,
	}

	switch dbType {
	case "sqlite":
		dsn = config.AppSettings.DBConnectionStringSqlite
		DB, err = gorm.Open(sqlite.Open(dsn), gormConfig)
	case "mysql":
		// 从独立配置项构建 DSN
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			config.AppSettings.MySQLUser,
			config.AppSettings.MySQLPassword,
			config.AppSettings.MySQLHost,
			config.AppSettings.MySQLPort,
			config.AppSettings.MySQLDBName,
		)
		DB, err = gorm.Open(mysql.Open(dsn), gormConfig)
	default:
		return nil, fmt.Errorf("不支持的数据库类型: %s", dbType)
	}

	if err != nil {
		Log.Errorf("连接到数据库 %s 失败: %v", dbType, err)
		return nil, err
	}

	Log.Info("数据库连接成功。")

	// 自动迁移数据库模式
	err = migrateSchema()
	if err != nil {
		return nil, err
	}

	return DB, nil
}

// migrateSchema 负责自动迁移数据库表结构。
func migrateSchema() error {
	Log.Info("正在执行数据库模式自动迁移...")
	err := DB.AutoMigrate(&APIKey{})
	if err != nil {
		Log.Errorf("数据库模式迁移失败: %v", err)
		return err
	}
	Log.Info("数据库模式迁移完成。")
	return nil
}
