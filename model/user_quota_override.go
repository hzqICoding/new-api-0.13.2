package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// OverrideUserQuota commits the absolute quota and synchronously invalidates the
// old user hash. A cache failure is returned even though the DB already committed.
// Existing asynchronous readers are not fenced by this operation.
func OverrideUserQuota(userID, quota int) (int, error) {
	if userID <= 0 {
		return 0, gorm.ErrRecordNotFound
	}
	oldQuota := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			return err
		}
		oldQuota = user.Quota
		return tx.Model(&user).Update("quota", quota).Error
	})
	if err != nil {
		return oldQuota, err
	}
	if common.RedisEnabled {
		if err := InvalidateUserCache(userID); err != nil {
			return oldQuota, adminCacheResult(err)
		}
	}
	return oldQuota, nil
}
