package model

import (
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var ErrAdminResourceConflict = errors.New("admin_resource_conflict")

type AdminResourceCacheError struct{ Cause error }

func (e *AdminResourceCacheError) Error() string { return "admin resource cache failure" }
func (e *AdminResourceCacheError) Unwrap() error { return e.Cause }
func adminCacheResult(err error) error {
	if err == nil {
		return nil
	}
	return &AdminResourceCacheError{Cause: err}
}

type AdminResourceOperation struct {
	ID          string `gorm:"size:32;primaryKey"`
	ActorID     int
	Kind        string `gorm:"size:16"`
	TargetID    int
	TokenID     int
	Fingerprint string `gorm:"size:64"`
	Cancelled   bool
}

// The operation row serializes creation and cancellation, including late HTTP requests.
func lockAdminOperation(tx *gorm.DB, id string, actor int) (*AdminResourceOperation, error) {
	op := AdminResourceOperation{ID: id, ActorID: actor}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&op).Error; err != nil {
		return nil, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&op, "id = ?", id).Error; err != nil {
		return nil, err
	}
	if op.ActorID != actor {
		return nil, ErrAdminResourceConflict
	}
	return &op, nil
}

func AdminCreateUser(operation string, actor int, fingerprint, username, password string, roles ...int) (int, error) {
	id := 0
	err := DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Transaction(func(tx *gorm.DB) error {
		op, err := lockAdminOperation(tx, operation, actor)
		if err != nil {
			return err
		}
		if op.Cancelled || (op.Kind != "" && (op.Kind != "user" || op.Fingerprint != fingerprint)) {
			return ErrAdminResourceConflict
		}
		if op.TargetID != 0 {
			var user User
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, op.TargetID).Error; err != nil {
				return err
			}
			role := common.RoleRootUser
			if len(roles) > 0 {
				role = roles[0]
			}
			if user.Role >= role || user.Status != common.UserStatusEnabled {
				return ErrAdminResourceConflict
			}
			id = op.TargetID
			return nil
		}
		user := User{Username: username, Password: password, DisplayName: username, Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
		if err := user.InsertWithTx(tx, 0); err != nil {
			return err
		}
		if err := tx.Model(&user).Update("quota", 0).Error; err != nil {
			return err
		}
		op.Kind = "user"
		op.TargetID = user.Id
		op.Fingerprint = fingerprint
		id = user.Id
		return tx.Save(op).Error
	})
	return id, err
}

func AdminCreateToken(operation string, actor, actorRole, target int, fingerprint, name, group string) (*Token, error) {
	var token Token
	err := DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Transaction(func(tx *gorm.DB) error {
		op, err := lockAdminOperation(tx, operation, actor)
		if err != nil {
			return err
		}
		if op.Cancelled || (op.Kind != "" && (op.Kind != "token" || op.TargetID != target || op.Fingerprint != fingerprint)) {
			return ErrAdminResourceConflict
		}
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, target).Error; err != nil {
			return err
		}
		if user.Role >= actorRole || user.Status != common.UserStatusEnabled {
			return ErrAdminResourceConflict
		}
		if op.TokenID != 0 {
			return tx.First(&token, op.TokenID).Error
		}
		var count int64
		if err := tx.Model(&Token{}).Where("user_id = ?", target).Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(operation_setting.GetMaxUserTokens()) {
			return ErrAdminResourceConflict
		}
		key, err := common.GenerateKey()
		if err != nil {
			return err
		}
		token = Token{UserId: target, Name: name, Group: group, Key: key, Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1, CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp()}
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		op.Kind = "token"
		op.TargetID = target
		op.TokenID = token.Id
		op.Fingerprint = fingerprint
		return tx.Save(op).Error
	})
	return &token, err
}

func AdminRevokeToken(target, id int) error {
	var token Token
	if err := DB.Unscoped().Where("id = ? AND user_id = ?", id, target).First(&token).Error; err != nil {
		return err
	}
	if !token.DeletedAt.Valid {
		if err := DB.Delete(&token).Error; err != nil {
			return err
		}
	}
	if common.RedisEnabled {
		return adminCacheResult(revokeTokenCache(token.Key))
	}
	return nil
}

func AdminRevokeTokenAs(target, id, actorRole int) error {
	var token Token
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, target).Error; err != nil {
			return err
		}
		if user.Role >= actorRole || user.Status != common.UserStatusEnabled {
			return ErrAdminResourceConflict
		}
		if err := tx.Unscoped().Where("id = ? AND user_id = ?", id, target).First(&token).Error; err != nil {
			return err
		}
		if !token.DeletedAt.Valid {
			return tx.Delete(&token).Error
		}
		return nil
	})
	if err != nil {
		return err
	}
	if common.RedisEnabled {
		return adminCacheResult(revokeTokenCache(token.Key))
	}
	return nil
}

// AdminReadTokenAs reads only an enabled token owned by a lower-role active target.
func AdminReadTokenAs(target, id, actorRole int) (*Token, error) {
	var token Token
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, target).Error; err != nil {
			return err
		}
		if user.Role >= actorRole || user.Status != common.UserStatusEnabled {
			return ErrAdminResourceConflict
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", id, target).First(&token).Error; err != nil {
			return err
		}
		if token.Status != common.TokenStatusEnabled || token.Key == "" || (token.ExpiredTime != -1 && token.ExpiredTime <= common.GetTimestamp()) {
			return ErrAdminResourceConflict
		}
		return nil
	})
	return &token, err
}

func AdminCleanupOperation(operation string, actor, actorRole int) error {
	var op *AdminResourceOperation
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		op, err = lockAdminOperation(tx, operation, actor)
		if err != nil {
			return err
		}
		if op.TargetID != 0 {
			var user User
			if err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, op.TargetID).Error; err != nil {
				return err
			}
			if user.Role >= actorRole {
				return ErrAdminResourceConflict
			}
			if op.Kind == "user" {
				if err := tx.Unscoped().Model(&user).Updates(map[string]interface{}{"quota": 0, "status": common.UserStatusDisabled}).Error; err != nil {
					return err
				}
				if err := tx.Where("user_id = ?", user.Id).Delete(&Token{}).Error; err != nil {
					return err
				}
				if err := tx.Delete(&user).Error; err != nil {
					return err
				}
			} else if op.Kind == "token" {
				if err := tx.Where("id = ? AND user_id = ?", op.TokenID, op.TargetID).Delete(&Token{}).Error; err != nil {
					return err
				}
			}
		}
		op.Cancelled = true
		return tx.Save(op).Error
	})
	if err != nil {
		return err
	}
	if op.Kind == "user" && op.TargetID != 0 {
		if err := InvalidateUserCache(op.TargetID); err != nil {
			return adminCacheResult(err)
		}
		if common.RedisEnabled {
			var tokens []Token
			if err := DB.Unscoped().Where("user_id = ?", op.TargetID).Find(&tokens).Error; err != nil {
				return err
			}
			for _, token := range tokens {
				if err := revokeTokenCache(token.Key); err != nil {
					return adminCacheResult(err)
				}
			}
		}
		return nil
	}
	if op.Kind == "token" && op.TokenID != 0 {
		return AdminRevokeToken(op.TargetID, op.TokenID)
	}
	return nil
}
