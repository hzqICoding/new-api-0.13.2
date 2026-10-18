package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func failureTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	oldDB, oldLogDB, oldRedis := model.DB, model.LOG_DB, common.RedisEnabled
	oldErrorLog := constant.ErrorLogEnabled
	model.DB, model.LOG_DB, common.RedisEnabled = db, db, false
	constant.ErrorLogEnabled = false
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.RedisEnabled = oldDB, oldLogDB, oldRedis
		constant.ErrorLogEnabled = oldErrorLog
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}))
	require.NoError(t, db.Create(&model.User{Id: 741, Username: "failure-test", Quota: 0,
		Setting: `{"record_ip_log":true}`, Group: "ordinary"}).Error)
	return db
}

func failureTestRouter(logHandler gin.HandlerFunc, handlers ...gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("id", 741)
		c.Set("username", "failure-test")
		c.Set("token_id", 12)
		c.Set("token_name", "test-key")
		c.Set("group", "ordinary")
		c.Set(common.RequestIdKey, "failure-test-request")
		common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{Language: "zh-CN"})
	})
	r.Use(logHandler)
	r.POST("/v1/responses", handlers...)
	return r
}

func failureRequest(r http.Handler, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses?key=must-not-be-logged", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.1:1234"
	r.ServeHTTP(w, req)
	return w
}

func failureLogs(t *testing.T, db *gorm.DB) []model.Log {
	t.Helper()
	var logs []model.Log
	require.NoError(t, db.Order("id").Find(&logs).Error)
	return logs
}

func assertFailureLog(t *testing.T, db *gorm.DB, response *httptest.ResponseRecorder, reason, code, message string) model.Log {
	t.Helper()
	logs := failureLogs(t, db)
	require.Len(t, logs, 1)
	l := logs[0]
	require.Equal(t, model.LogTypeError, l.Type)
	require.Equal(t, message, l.Content)
	require.Equal(t, 741, l.UserId)
	require.Equal(t, "failure-test", l.Username)
	require.Equal(t, 12, l.TokenId)
	require.Equal(t, "test-key", l.TokenName)
	require.Equal(t, "ordinary", l.Group)
	require.Equal(t, "failure-test-request", l.RequestId)
	require.Equal(t, "192.0.2.1", l.Ip)
	require.Zero(t, l.Quota)
	require.Zero(t, l.PromptTokens)
	require.Zero(t, l.CompletionTokens)
	expected, err := common.Marshal(gin.H{"failure": gin.H{
		"status": response.Code, "reason": reason, "code": code, "path": "/v1/responses", "suppressed": 0,
	}})
	require.NoError(t, err)
	require.JSONEq(t, string(expected), l.Other)
	var body struct {
		Error struct{ Message string } `json:"error"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, body.Error.Message, l.Content)
	return l
}

func TestRelayFailureConcurrency(t *testing.T) {
	for _, reason := range []string{"concurrency_timeout", "concurrency_queue_full"} {
		t.Run(reason, func(t *testing.T) {
			db := failureTestDB(t)
			h := newCarpoolTestRig(t)
			wait := 0
			if reason == "concurrency_queue_full" {
				wait = 10
			}
			h.set(t, "enforce", 1, wait)
			holder := h.l.acquire(context.Background(), 741, "/hold")
			defer h.l.release(holder.lease)
			if wait > 0 {
				ctx, cancel := context.WithCancel(context.Background())
				queued := h.l.acquire(ctx, 741, "/queue")
				require.NotNil(t, queued.waiter)
				defer func() { cancel(); h.l.resolve(queued.waiter) }()
			}
			r := failureTestRouter(relayFailureLog(newRelayFailureLimiter(), time.Now), h.l.middleware(), func(c *gin.Context) {
				t.Error("rejection reached downstream")
			})
			w := failureRequest(r, "")
			require.Equal(t, 429, w.Code)
			message := "当前座位最大并发为 1，已等待 0 秒仍无空位，请稍后重试"
			if wait > 0 {
				message = "当前座位最大并发为 1，排队已满，请稍后重试"
			}
			l := assertFailureLog(t, db, w, reason, "carpool_concurrency_limit", message+" (request id: failure-test-request)")
			require.Empty(t, l.ModelName)
			require.Zero(t, l.ChannelId)
		})
	}
}

var failureRateLimitTestUser atomic.Int64

func TestRelayFailureMemoryRateLimit(t *testing.T) {
	// Initialize once without a janitor; unique user IDs isolate limiter keys.
	inMemoryRateLimiter.Init(0)
	for _, limits := range [][2]int{{1, 100}, {0, 1}} {
		t.Run(fmt.Sprint(limits), func(t *testing.T) {
			db := failureTestDB(t)
			userID := 75410 + int(failureRateLimitTestUser.Add(1))
			oldDuration := setting.ModelRequestRateLimitDurationMinutes
			setting.ModelRequestRateLimitDurationMinutes = 2
			t.Cleanup(func() { setting.ModelRequestRateLimitDurationMinutes = oldDuration })
			var calls int
			r := failureTestRouter(relayFailureLog(newRelayFailureLimiter(), time.Now), func(c *gin.Context) {
				// Isolate the existing global frequency limiter, then restore log identity.
				c.Set("id", userID)
				memoryRateLimitHandler(120, limits[0], limits[1])(c)
				c.Set("id", 741)
			}, func(c *gin.Context) { calls++; c.Status(204) })
			require.Equal(t, 204, failureRequest(r, "").Code)
			w := failureRequest(r, "")
			require.Equal(t, 429, w.Code)
			require.Equal(t, 1, calls)
			assertFailureLog(t, db, w, "rate_limit", "model_rate_limit", "请求过于频繁：2 分钟内最多 1 次请求（含失败），请稍后重试 (request id: failure-test-request)")
		})
	}
}

func TestRelayFailureDistributor(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason string
		status             int
	}{
		{"no_channel", `{"model":"missing-model"}`, "model_unavailable", 503},
		{"missing_model", `{}`, "request_invalid", 400},
		{"bad_json", `{`, "request_invalid", 400},
		{"model_forbidden", `{"model":"missing-model"}`, "request_invalid", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := failureTestDB(t)
			oldCache := common.MemoryCacheEnabled
			common.MemoryCacheEnabled = true
			t.Cleanup(func() { common.MemoryCacheEnabled = oldCache })
			r := failureTestRouter(relayFailureLog(newRelayFailureLimiter(), time.Now), func(c *gin.Context) {
				if tc.name == "model_forbidden" {
					c.Set("token_model_limit_enabled", true)
				}
			}, Distribute(), func(c *gin.Context) { t.Error("rejection reached downstream") })
			w := failureRequest(r, tc.body)
			require.Equal(t, tc.status, w.Code)
			message, code, _ := relayFailureMessage(w.Body.Bytes())
			l := assertFailureLog(t, db, w, tc.reason, code, message)
			if tc.name == "no_channel" {
				require.Equal(t, "model_not_found", code)
				require.Equal(t, "分组 ordinary 下模型 missing-model 无可用渠道（distributor） (request id: failure-test-request)", message)
				require.Equal(t, "missing-model", l.ModelName)
			}
		})
	}
}

func TestRelayFailureQuotaAndUpstream(t *testing.T) {
	for _, quota := range []bool{true, false} {
		t.Run(fmt.Sprint(quota), func(t *testing.T) {
			db := failureTestDB(t)
			r := failureTestRouter(relayFailureLog(newRelayFailureLimiter(), time.Now), func(c *gin.Context) {
				c.Set("original_model", "test-model")
				c.Set("channel_id", 7)
				common.SetContextKey(c, constant.ContextKeyIsStream, true)
				common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now().Add(-3*time.Second))
				if quota {
					_, apiErr := service.NewBillingSession(c, &relaycommon.RelayInfo{UserId: 741,
						UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}}, 1)
					require.NotNil(t, apiErr)
					require.Equal(t, "quota_insufficient", c.GetString(relayFailureReasonKey))
					c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
				} else {
					c.Set("use_channel", []string{"7", "8", "7"}) // Three attempts, one final response.
					c.JSON(502, gin.H{"error": gin.H{"message": "上游服务暂不可用", "code": "bad_response_status_code"}})
				}
			})
			w := failureRequest(r, "")
			reason, code := "upstream_error", "bad_response_status_code"
			message := "上游服务暂不可用"
			if quota {
				require.Equal(t, 403, w.Code)
				reason, code = "quota_insufficient", "insufficient_user_quota"
				message, _, _ = relayFailureMessage(w.Body.Bytes())
				require.Contains(t, message, "用户额度不足")
			} else {
				require.Equal(t, 502, w.Code)
			}
			l := assertFailureLog(t, db, w, reason, code, message)
			require.Equal(t, "test-model", l.ModelName)
			require.Equal(t, 7, l.ChannelId)
			require.True(t, l.IsStream)
			require.GreaterOrEqual(t, l.UseTime, 3)
		})
	}
}

type failureFlushRecorder struct {
	*httptest.ResponseRecorder
	flushed []string
}

func (w *failureFlushRecorder) Flush() {
	w.flushed = append(w.flushed, w.Body.String())
	w.ResponseRecorder.Flush()
}

func TestRelayFailureSuccessPassThrough(t *testing.T) {
	db := failureTestDB(t)
	for _, stream := range []bool{false, true} {
		var baseline *failureFlushRecorder
		for _, wrapped := range []bool{false, true} {
			w := &failureFlushRecorder{ResponseRecorder: httptest.NewRecorder()}
			r := gin.New()
			if wrapped {
				r.Use(relayFailureLog(newRelayFailureLimiter(), time.Now))
			}
			r.GET("/", func(c *gin.Context) {
				c.Set("id", 741)
				c.Header("Content-Type", "text/event-stream")
				for i, chunk := range []string{"data: first\n\n", "data: second\n\n", "data: [DONE]\n\n"} {
					if i%2 == 0 {
						_, _ = c.Writer.WriteString(chunk)
					} else {
						_, _ = c.Writer.Write([]byte(chunk))
					}
					require.True(t, strings.HasSuffix(w.Body.String(), chunk), "chunk must reach underlying writer before Flush")
					if stream {
						c.Writer.Flush()
					}
					if wrapped {
						writer := c.Writer.(*relayFailureWriter)
						require.Nil(t, writer.body, "successful writes must not capture or allocate a body")
						require.Zero(t, cap(writer.body))
					}
				}
			})
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
			if !wrapped {
				baseline = w
			} else {
				require.Equal(t, baseline.Code, w.Code)
				require.Equal(t, baseline.Header(), w.Header())
				require.Equal(t, baseline.Body.String(), w.Body.String())
				require.Equal(t, baseline.flushed, w.flushed)
			}
		}
	}
	require.Empty(t, failureLogs(t, db))
}

func TestRelayFailureAuthAndUnknownUser(t *testing.T) {
	db := failureTestDB(t)
	r := gin.New()
	r.Use(TokenAuth(), relayFailureLog(newRelayFailureLimiter(), time.Now))
	r.POST("/v1/responses", func(c *gin.Context) { t.Error("unauthenticated request reached relay") })
	require.Equal(t, 401, failureRequest(r, "").Code)
	r = gin.New()
	r.Use(relayFailureLog(newRelayFailureLimiter(), time.Now))
	r.POST("/v1/responses", func(c *gin.Context) { c.JSON(500, gin.H{"error": gin.H{"message": "unknown user"}}) })
	require.Equal(t, 500, failureRequest(r, "").Code)
	require.Empty(t, failureLogs(t, db))
}

func TestRelayFailureBodyLimitAndContent(t *testing.T) {
	for _, stringWrite := range []bool{false, true} {
		t.Run(fmt.Sprint(stringWrite), func(t *testing.T) {
			db := failureTestDB(t)
			payload := strings.Repeat("错", 3000)
			r := failureTestRouter(relayFailureLog(newRelayFailureLimiter(), time.Now), func(c *gin.Context) {
				c.Status(500)
				if stringWrite {
					_, _ = c.Writer.WriteString(payload)
				} else {
					_, _ = c.Writer.Write([]byte(payload))
				}
				_, _ = c.Writer.WriteString("tail")
				writer := c.Writer.(*relayFailureWriter)
				require.Len(t, writer.body, 4096)
				require.Equal(t, 4096, cap(writer.body))
				require.Equal(t, payload[:4096], string(writer.body))
			})
			w := failureRequest(r, "")
			require.Equal(t, payload+"tail", w.Body.String())
			logs := failureLogs(t, db)
			require.Len(t, logs, 1)
			require.Equal(t, relayFailureFallback, logs[0].Content)
		})
	}
	message, code, _ := relayFailureMessage([]byte(`{"error":{"message":"` + strings.Repeat("错", 501) + `","code":123}}`))
	require.Equal(t, strings.Repeat("错", 500), message)
	require.Equal(t, "123", code)
	for _, body := range []string{"", "not json", "null", `{}`, `{"error":{"code":null}}`} {
		message, _, _ := relayFailureMessage([]byte(body))
		require.Equal(t, relayFailureFallback, message)
	}
}

func TestRelayFailureFinalStatusAndNestedMounts(t *testing.T) {
	db := failureTestDB(t)
	limiter := newRelayFailureLimiter()
	r := failureTestRouter(relayFailureLog(limiter, time.Now), relayFailureLog(limiter, time.Now), func(c *gin.Context) {
		c.Status(500) // Not committed: successful retry replaces the tentative status.
		c.Status(200)
		_, _ = c.Writer.WriteString("ok")
		c.Status(500) // Committed status cannot change.
	})
	w := failureRequest(r, "")
	require.Equal(t, 200, w.Code)
	require.Equal(t, "ok", w.Body.String())
	require.Empty(t, failureLogs(t, db))
	r = failureTestRouter(relayFailureLog(limiter, time.Now), relayFailureLog(limiter, time.Now), func(c *gin.Context) {
		c.JSON(503, gin.H{"error": gin.H{"message": "final failure"}})
	})
	require.Equal(t, 503, failureRequest(r, "").Code)
	require.Len(t, failureLogs(t, db), 1)
	require.Equal(t, 1, limiter.users[741].count)
}

func TestRelayFailureUserLimitAndSuppressed(t *testing.T) {
	db := failureTestDB(t)
	limiter := newRelayFailureLimiter()
	now := time.Unix(1800000000, 0)
	r := failureTestRouter(relayFailureLog(limiter, func() time.Time { return now }), func(c *gin.Context) {
		c.JSON(500, gin.H{"error": gin.H{"message": "failure"}})
	})
	for i := 0; i < 35; i++ {
		require.Equal(t, 500, failureRequest(r, "").Code)
	}
	require.Len(t, failureLogs(t, db), 30)
	now = now.Add(time.Minute)
	limiter.cleanup(now)
	require.Equal(t, 500, failureRequest(r, "").Code)
	logs := failureLogs(t, db)
	require.Len(t, logs, 31)
	require.JSONEq(t, `{"failure":{"status":500,"reason":"upstream_error","code":"","path":"/v1/responses","suppressed":5}}`, logs[30].Other)
	failureRequest(r, "")
	logs = failureLogs(t, db)
	require.Contains(t, logs[31].Other, `"suppressed":0`)
}

func TestRelayFailureCounterConcurrent(t *testing.T) {
	l := newRelayFailureLimiter()
	now := time.Unix(1800000000, 0)
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for i := 0; i < 128; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if allowed, _ := l.allow(741, now); allowed {
				accepted.Add(1)
			}
			l.cleanup(now)
		}()
	}
	wg.Wait()
	require.EqualValues(t, 30, accepted.Load())
	allowed, suppressed := l.allow(742, now)
	require.True(t, allowed)
	require.Zero(t, suppressed)
	l.cleanup(now.Add(24 * time.Hour))
	require.NotContains(t, l.users, 742)
	allowed, suppressed = l.allow(741, now.Add(24*time.Hour))
	require.True(t, allowed)
	require.Equal(t, 98, suppressed)
	_, suppressed = l.allow(741, now.Add(24*time.Hour))
	require.Zero(t, suppressed)
	// A caller scheduled late with an older timestamp cannot reopen a window.
	l.allow(741, now)
	require.Equal(t, now.Add(24*time.Hour).Unix()/60, l.users[741].minute)
	require.Equal(t, 3, l.users[741].count)
	l.cleanup(now.Add(25 * time.Hour))
	require.Empty(t, l.users)
}

func TestRelayFailureReasonFallback(t *testing.T) {
	for _, tc := range []struct {
		status                int
		code, errorType, want string
		attempted             bool
	}{
		{429, "model_rate_limit", "", "rate_limit", false},
		{403, "insufficient_user_quota", "", "quota_insufficient", false},
		{503, "model_not_found", "", "model_unavailable", false},
		{400, "bad_response_status_code", "", "upstream_error", false},
		{502, "", "", "upstream_error", false},
		{400, "", "upstream_error", "upstream_error", false},
		{429, "provider_limit", "", "upstream_error", true},
		{404, "model_not_found", "", "upstream_error", true},
		{418, "", "", "other", false},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		if tc.attempted {
			c.Set("use_channel", []string{"7"})
		}
		require.Equal(t, tc.want, relayFailureReason(c, tc.status, tc.code, tc.errorType))
	}
}
