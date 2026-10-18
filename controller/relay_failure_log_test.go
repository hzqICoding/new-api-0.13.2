package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Exercise the real channel-error side effects at each simulated relay attempt.
// The existing opt-in per-attempt log behavior is intentionally kept unchanged.
func TestRelayFailureWithLegacyChannelErrorLogging(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, enabled := range []bool{false, true} {
		for _, finalStatus := range []int{200, 502} {
			t.Run(fmt.Sprintf("enabled=%t/final=%d", enabled, finalStatus), func(t *testing.T) {
				db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
				require.NoError(t, err)
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(1)
				oldDB, oldLogDB := model.DB, model.LOG_DB
				oldEnabled, oldRedis := constant.ErrorLogEnabled, common.RedisEnabled
				model.DB, model.LOG_DB = db, db
				constant.ErrorLogEnabled, common.RedisEnabled = enabled, false
				t.Cleanup(func() {
					model.DB, model.LOG_DB = oldDB, oldLogDB
					constant.ErrorLogEnabled, common.RedisEnabled = oldEnabled, oldRedis
					require.NoError(t, sqlDB.Close())
				})
				require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}))
				r := gin.New()
				r.Use(func(c *gin.Context) {
					c.Set("id", 75741)
					c.Set("group", "ordinary")
					c.Set("token_name", "test-key")
					c.Set(common.RequestIdKey, "retry-final-result")
				}, middleware.RelayFailureLog())
				r.POST("/v1/responses", func(c *gin.Context) {
					for attempt := 1; attempt <= 3; attempt++ {
						c.Set("channel_id", attempt)
						addUsedChannel(c, attempt)
						apiErr := types.NewErrorWithStatusCode(fmt.Errorf("attempt %d failed", attempt), types.ErrorCodeBadResponseStatusCode, 502)
						processChannelError(c, types.ChannelError{ChannelId: attempt}, apiErr)
					}
					if finalStatus == 200 {
						c.String(200, "recovered")
					} else {
						c.JSON(502, gin.H{"error": gin.H{"message": "final upstream failure", "code": "bad_response_status_code"}})
					}
				})
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
				require.Equal(t, finalStatus, w.Code)
				var logs []model.Log
				require.NoError(t, db.Order("id").Find(&logs).Error)
				var finalLogs, attemptLogs int
				for _, entry := range logs {
					require.Equal(t, model.LogTypeError, entry.Type)
					var other map[string]interface{}
					require.NoError(t, common.UnmarshalJsonStr(entry.Other, &other))
					if _, exists := other["failure"]; exists {
						finalLogs++
						require.Equal(t, "final upstream failure", entry.Content)
						require.Equal(t, 3, entry.ChannelId)
						require.JSONEq(t, `{"failure":{"status":502,"reason":"upstream_error","code":"bad_response_status_code","path":"/v1/responses","suppressed":0}}`, entry.Other)
					} else {
						attemptLogs++
					}
				}
				if finalStatus == 200 {
					require.Zero(t, finalLogs)
				} else {
					require.Equal(t, 1, finalLogs)
				}
				if enabled {
					require.Equal(t, 3, attemptLogs)
				} else {
					require.Zero(t, attemptLogs)
				}
			})
		}
	}
}
