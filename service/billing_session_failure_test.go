package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestBillingSessionFinalFailureReason(t *testing.T) {
	for _, tc := range []struct {
		name, preference string
		wallet, preQuota int
		subscription     bool
		tokenFailure     bool
		wantCode         types.ErrorCode
	}{
		{"wallet_empty", "wallet_only", 0, 1, false, false, types.ErrorCodeInsufficientUserQuota},
		{"wallet_short", "wallet_only", 1, 2, false, false, types.ErrorCodeInsufficientUserQuota},
		{"token_short", "wallet_only", 10, 2, false, true, types.ErrorCodePreConsumeTokenQuotaFailed},
		{"subscription_missing", "subscription_only", 0, 1, false, false, types.ErrorCodeInsufficientUserQuota},
		{"wallet_fallback_recovers", "wallet_first", 0, 1, true, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			oldDB, oldRedis := model.DB, common.RedisEnabled
			model.DB, common.RedisEnabled = db, false
			t.Cleanup(func() {
				model.DB, common.RedisEnabled = oldDB, oldRedis
				require.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.SubscriptionPlan{},
				&model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}))
			require.NoError(t, db.Create(&model.User{Id: 741, Username: "billing-failure", Quota: tc.wallet}).Error)
			require.NoError(t, db.Create(&model.Token{Id: 12, UserId: 741, Key: "billing-failure-token", RemainQuota: 0}).Error)
			if tc.tokenFailure {
				// This package's TestMain does not initialize model's private SQL
				// quoting helpers. Supply the token lookup predicate in the fixture.
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("failure:token_lookup", func(tx *gorm.DB) {
					if tx.Statement.Table == "tokens" {
						tx.Statement.Clauses["WHERE"] = clause.Clause{}
						tx.Statement.AddClause(clause.Where{Exprs: []clause.Expression{clause.Eq{Column: "key", Value: "billing-failure-token"}}})
					}
				}))
			}
			if tc.subscription {
				require.NoError(t, db.Create(&model.SubscriptionPlan{Id: 75741, Title: "failure-test"}).Error)
				require.NoError(t, db.Create(&model.UserSubscription{UserId: 741, PlanId: 75741,
					AmountTotal: 100, Status: "active", EndTime: time.Now().Add(time.Hour).Unix()}).Error)
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{UserId: 741, TokenId: 12, TokenKey: "billing-failure-token",
				RequestId: "billing-failure-" + tc.name, IsPlayground: !tc.tokenFailure, ForcePreConsume: true,
				UserSetting: dto.UserSetting{BillingPreference: tc.preference}}
			session, apiErr := NewBillingSession(c, info, tc.preQuota)
			if tc.wantCode == "" {
				require.Nil(t, apiErr)
				require.NotNil(t, session)
				require.Empty(t, c.GetString("relay_failure_reason"), "recovered funding failure must not mark a later upstream error as quota insufficient")
			} else {
				require.Nil(t, session)
				require.NotNil(t, apiErr)
				require.Equal(t, tc.wantCode, apiErr.GetErrorCode())
				if tc.tokenFailure {
					require.Contains(t, apiErr.Error(), "token quota is not enough")
				}
				require.Equal(t, 403, apiErr.StatusCode)
				require.Equal(t, "quota_insufficient", c.GetString("relay_failure_reason"))
				require.False(t, types.IsRecordErrorLog(apiErr), "legacy NoRecordErrorLog behavior must remain unchanged")
			}
		})
	}
}
