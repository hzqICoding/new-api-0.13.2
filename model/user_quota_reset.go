package model

import (
	"math"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AdminQuotaResetOperation is a durable receipt and a fence for late HTTP requests.
// Keep cancelled receipts: deleting them would allow a delayed request to grant again.
type AdminQuotaResetOperation struct {
	ID            string `gorm:"size:32;primaryKey"`
	ActorID       int
	TargetID      int
	DeltaQuota    int64
	CeilingQuota  int64
	ExpectedGroup string `gorm:"size:64"`
	ExpiresAt     int64
	Applied       bool
	Cancelled     bool
	CreatedAt     time.Time
}

type AdminQuotaResetInput struct {
	OperationID   string
	ActorID       int
	ActorRole     int
	TargetID      int
	DeltaQuota    int64
	CeilingQuota  int64
	ExpectedGroup string
	ExpiresAt     int64
}

type AdminQuotaResetResult struct {
	Quota     int64 `json:"quota"`
	Applied   bool  `json:"applied"`
	Cancelled bool  `json:"cancelled"`
}

func lockQuotaResetOperation(tx *gorm.DB, id string, actor, target int) (*AdminQuotaResetOperation, error) {
	op := AdminQuotaResetOperation{ID: id, ActorID: actor, TargetID: target}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&op).Error; err != nil {
		return nil, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&op, "id = ?", id).Error; err != nil {
		return nil, err
	}
	if op.ActorID != actor || op.TargetID != target {
		return nil, ErrAdminResourceConflict
	}
	return &op, nil
}

// ApplyAdminQuotaReset adds the signed entitlement change exactly once, capped at
// the current finite-window entitlement. It never replaces quota on a retry, so
// consumption between attempts remains debited, including negative balances.
func ApplyAdminQuotaReset(in AdminQuotaResetInput) (result AdminQuotaResetResult, err error) {
	// Legacy Redis refill jobs are not fenced against quota changes. Refuse this
	// new operation on that configuration rather than silently restoring stale
	// balances. The carpool deployment deliberately runs with Redis disabled.
	if common.RedisEnabled {
		return result, ErrAdminResourceConflict
	}
	if in.OperationID == "" || in.ActorID <= 0 || in.TargetID <= 0 || in.ActorRole < common.RoleAdminUser || in.CeilingQuota < 0 || in.ExpectedGroup == "" {
		return result, ErrAdminResourceConflict
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		op, err := lockQuotaResetOperation(tx, in.OperationID, in.ActorID, in.TargetID)
		if err != nil {
			return err
		}
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, in.TargetID).Error; err != nil {
			return err
		}
		if user.Role >= in.ActorRole {
			return ErrAdminResourceConflict
		}
		result = AdminQuotaResetResult{Quota: int64(user.Quota), Applied: op.Applied, Cancelled: op.Cancelled}
		if op.Cancelled {
			return nil
		}
		if op.Applied {
			if op.DeltaQuota != in.DeltaQuota || op.CeilingQuota != in.CeilingQuota || op.ExpectedGroup != in.ExpectedGroup || op.ExpiresAt != in.ExpiresAt {
				return ErrAdminResourceConflict
			}
			return nil
		}
		if user.Status != common.UserStatusEnabled || user.Group != in.ExpectedGroup || in.ExpiresAt <= time.Now().Unix() {
			return ErrAdminResourceConflict
		}
		current := int64(user.Quota)
		if in.DeltaQuota > 0 && current > math.MaxInt64-in.DeltaQuota || in.DeltaQuota < 0 && current < math.MinInt64-in.DeltaQuota {
			return ErrAdminResourceConflict
		}
		next := current + in.DeltaQuota
		if next > in.CeilingQuota {
			next = in.CeilingQuota
		}
		// User.Quota is an SQL INT on MySQL/PostgreSQL; keep SQLite behavior equal.
		if next < math.MinInt32 || next > math.MaxInt32 {
			return ErrAdminResourceConflict
		}
		if err := tx.Model(&user).UpdateColumn("quota", next).Error; err != nil {
			return err
		}
		op.DeltaQuota, op.CeilingQuota = in.DeltaQuota, in.CeilingQuota
		op.ExpectedGroup, op.ExpiresAt, op.Applied = in.ExpectedGroup, in.ExpiresAt, true
		if err := tx.Save(op).Error; err != nil {
			return err
		}
		result.Quota, result.Applied = next, true
		return nil
	})
	if err == nil && common.RedisEnabled {
		err = adminCacheResult(InvalidateUserCache(in.TargetID))
	}
	return result, err
}

// CancelAdminQuotaReset fences an operation even before the corresponding POST
// arrives. It does not reverse a completed grant: the caller reconciles against
// current ownership and entitlement instead of undoing real consumption.
func CancelAdminQuotaReset(id string, actor, role, target int) (result AdminQuotaResetResult, err error) {
	if id == "" || actor <= 0 || target <= 0 || role < common.RoleAdminUser {
		return result, ErrAdminResourceConflict
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		op, err := lockQuotaResetOperation(tx, id, actor, target)
		if err != nil {
			return err
		}
		// This only cancels the caller's own operation, never changes the user.
		// The target may have been deleted or promoted since submission. Requiring
		// its old state here would make a durable fence impossible and strand the
		// caller's local ownership lock forever. Do not expose its current quota.
		op.Cancelled = true
		if err := tx.Save(op).Error; err != nil {
			return err
		}
		result = AdminQuotaResetResult{Applied: op.Applied, Cancelled: true}
		return nil
	})
	return result, err
}

func UserUsageWatermark(userID int) (int64, error) {
	var id int64
	err := LOG_DB.Model(&Log{}).Where("user_id = ? AND type = ?", userID, LogTypeConsume).Select("COALESCE(MAX(id), 0)").Scan(&id).Error
	return id, err
}
