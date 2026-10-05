package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ClampUserQuota atomically lowers quota to ceiling without ever increasing it.
func ClampUserQuota(userID int, ceiling int64, actorRole int) (int64, error) {
	if userID <= 0 || ceiling < 0 || actorRole < common.RoleAdminUser {
		return 0, ErrAdminResourceConflict
	}
	var quota int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			return err
		}
		// Recheck under the row lock: promotion/disable may race the controller lookup.
		if user.Role >= actorRole || user.Status != common.UserStatusEnabled {
			return ErrAdminResourceConflict
		}
		if err := tx.Model(&User{}).Where("id = ?", userID).UpdateColumn(
			"quota",
			gorm.Expr("CASE WHEN quota > ? THEN ? ELSE quota END", ceiling, ceiling),
		).Error; err != nil {
			return err
		}
		return tx.Model(&User{}).Where("id = ?", userID).Select("quota").Scan(&quota).Error
	})
	if err != nil {
		return 0, err
	}
	if common.RedisEnabled {
		if err := InvalidateUserCache(userID); err != nil {
			return quota, adminCacheResult(err)
		}
	}
	return quota, nil
}
