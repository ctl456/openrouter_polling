package storage

import (
	"errors"
	"gorm.io/gorm"
	"time"
)

var (
	ErrKeyNotFound      = errors.New("API key not found in the database")
	ErrKeyAlreadyExists = errors.New("API key already exists in the database")
)

// KeyStore 提供了与数据库中 APIKey 表交互的所有方法。
type KeyStore struct {
	db *gorm.DB
}

// NewKeyStore 创建一个新的 KeyStore 实例。
func NewKeyStore(db *gorm.DB) *KeyStore {
	return &KeyStore{db: db}
}

// AddKey 向数据库中添加一个新的 APIKey。
func (s *KeyStore) AddKey(key *APIKey) error {
	// 使用 FirstOrCreate 来避免重复添加。
	// Where 条件用于查找，Attrs 用于在创建时设置额外属性。
	result := s.db.Where(APIKey{Key: key.Key}).Attrs(key).FirstOrCreate(key)
	if result.Error != nil {
		return result.Error
	}
	// 如果 result.RowsAffected == 0，说明记录已存在，FirstOrCreate 没有创建新记录。
	if result.RowsAffected == 0 {
		return ErrKeyAlreadyExists
	}
	return nil
}

// AddKeysInBatch 以事务方式向数据库批量添加多个 APIKey 记录。
func (s *KeyStore) AddKeysInBatch(keys []*APIKey) error {
	if len(keys) == 0 {
		return nil
	}
	return s.db.CreateInBatches(keys, 100).Error // 以 100 为一批次进行创建
}

// GetAllKeys 从数据库中获取所有未被软删除的 APIKey。
func (s *KeyStore) GetAllKeys() ([]*APIKey, error) {
	var keys []*APIKey
	if err := s.db.Order("created_at desc").Find(&keys).Error; err != nil {
		return nil, err
	}
	return keys, nil
}

// GetKeysPaginated 【新增】从数据库分页获取 APIKey 列表，并返回总记录数。
func (s *KeyStore) GetKeysPaginated(offset, limit int) ([]*APIKey, int64, error) {
	var keys []*APIKey
	var totalCount int64

	// 首先，获取总记录数
	if err := s.db.Model(&APIKey{}).Count(&totalCount).Error; err != nil {
		return nil, 0, err
	}

	// 然后，获取分页数据
	query := s.db.Order("created_at desc").Offset(offset).Limit(limit).Find(&keys)
	if query.Error != nil {
		return nil, 0, query.Error
	}

	return keys, totalCount, nil
}

// GetKeyByKeyString 通过密钥字符串获取一个 APIKey 的完整信息。
func (s *KeyStore) GetKeyByKeyString(keyStr string) (*APIKey, error) {
	var key APIKey
	result := s.db.Where("`key` = ?", keyStr).First(&key)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrKeyNotFound
		}
		return nil, result.Error
	}
	return &key, nil
}

// UpdateKeyFields 更新数据库中一个现有 APIKey 的特定字段。
func (s *KeyStore) UpdateKeyFields(keyStr string, updates map[string]interface{}) error {
	result := s.db.Model(&APIKey{}).Where("`key` = ?", keyStr).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrKeyNotFound
	}
	return nil
}

// DeleteKeyByKeyString 通过密钥字符串从数据库中软删除一个 APIKey。
func (s *KeyStore) DeleteKeyByKeyString(keyStr string) error {
	result := s.db.Where("`key` = ?", keyStr).Delete(&APIKey{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrKeyNotFound
	}
	return nil
}

// DeleteKeysByKeysInBatch 【修改】通过一组密钥字符串批量软删除 APIKey。
func (s *KeyStore) DeleteKeysByKeysInBatch(keyStrs []string) (int64, error) {
	if len(keyStrs) == 0 {
		return 0, nil
	}
	// 明确使用反引号 `key` 来处理 SQL 保留关键字问题。
	result := s.db.Where("`key` IN ?", keyStrs).Delete(&APIKey{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// RecordSuccessOrReactivate 更新密钥为成功/激活状态。
func (s *KeyStore) RecordSuccessOrReactivate(keyStr string) error {
	updates := map[string]interface{}{
		"is_active":         true,
		"failure_count":     0,
		"last_failure_time": nil,
		"cool_down_until":   nil,
	}
	return s.UpdateKeyFields(keyStr, updates)
}

// RecordFailure 更新密钥为失败状态，并计算冷却时间。
func (s *KeyStore) RecordFailure(keyStr string, failureCount int, cooldownDuration time.Duration) error {
	now := time.Now()
	coolDownUntil := now.Add(cooldownDuration)
	updates := map[string]interface{}{
		"is_active":         false,
		"failure_count":     gorm.Expr("failure_count + 1"),
		"last_failure_time": &now,
		"cool_down_until":   &coolDownUntil,
	}
	return s.UpdateKeyFields(keyStr, updates)
}

// UpdateLastUsedTime 更新密钥的最后使用时间。
func (s *KeyStore) UpdateLastUsedTime(keyStr string) error {
	return s.UpdateKeyFields(keyStr, map[string]interface{}{"last_used_time": time.Now()})
}

// DeleteAllKeys 从数据库中永久删除所有 APIKey 记录。
func (s *KeyStore) DeleteAllKeys() error {
	result := s.db.Unscoped().Where("1 = 1").Delete(&APIKey{})
	return result.Error
}
