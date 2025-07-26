package storage

import (
	"time"

	"gorm.io/gorm"
)

// APIKey 定义了存储在数据库中的 API 密钥的结构。
// 这个模型是 API 密钥持久化状态的唯一真实来源。
type APIKey struct {
	ID              uint           `gorm:"primarykey"` // gorm 默认的自增主键
	CreatedAt       time.Time      // 记录创建时间
	UpdatedAt       time.Time      // 记录最后更新时间
	DeletedAt       gorm.DeletedAt `gorm:"index"`      // gorm 的软删除字段

	Key             string     `gorm:"type:varchar(255);uniqueIndex;not null"` // API 密钥字符串，必须唯一且非空
	Weight          int        `gorm:"default:1"`            // 密钥权重，默认为 1
	IsActive        bool       `gorm:"default:true"`         // 密钥是否激活
	FailureCount    int        `gorm:"default:0"`            // 连续失败次数
	LastFailureTime *time.Time // 上次失败的时间戳，可以为 null
	CoolDownUntil   *time.Time // 冷却截止时间，可以为 null
	LastUsedTime    *time.Time // 上次使用时间，可以为 null
}

// TableName 自定义 APIKey 模型的表名
func (APIKey) TableName() string {
	return "openrouter_api_keys"
}
