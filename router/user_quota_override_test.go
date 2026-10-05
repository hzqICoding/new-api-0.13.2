package router

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type quotaOverrideHook struct {
	cacheKey   string
	failDelete atomic.Bool
	gate       chan struct{}
	reached    chan struct{}
	finished   chan struct{}
}

func (h *quotaOverrideHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if cmd.Name() == "del" && h.failDelete.CompareAndSwap(true, false) {
		return ctx, errors.New("injected Redis invalidation failure")
	}
	return ctx, nil
}
func (h *quotaOverrideHook) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (h *quotaOverrideHook) isFill(cmds []redis.Cmder) bool {
	for _, cmd := range cmds {
		if cmd.Name() == "hset" && len(cmd.Args()) > 1 && cmd.Args()[1] == h.cacheKey {
			return true
		}
	}
	return false
}
func (h *quotaOverrideHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	if h.isFill(cmds) {
		h.reached <- struct{}{}
		<-h.gate
	}
	return ctx, nil
}
func (h *quotaOverrideHook) AfterProcessPipeline(_ context.Context, cmds []redis.Cmder) error {
	if h.isFill(cmds) {
		h.finished <- struct{}{}
	}
	return nil
}

func TestManageUserOverrideImmediatelyInvalidatesRedisQuota(t *testing.T) {
	address := os.Getenv("CARPOOL_REVIEW_REDIS_ADDR")
	if address == "" {
		t.Skip("set CARPOOL_REVIEW_REDIS_ADDR to disposable loopback Redis")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("loopback Redis required")
	}
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/override.db"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	oldDB, oldLogDB, oldRDB, oldEnabled, oldSync := model.DB, model.LOG_DB, common.RDB, common.RedisEnabled, common.SyncFrequency
	defer func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RDB = oldRDB
		common.RedisEnabled = oldEnabled
		common.SyncFrequency = oldSync
	}()
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = true
	common.SyncFrequency = 60
	if err := db.AutoMigrate(&model.User{}, &model.Log{}); err != nil {
		t.Fatal(err)
	}
	adminToken := "quota-review-admin"
	admin := model.User{Id: 301, Username: "quota-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AccessToken: &adminToken, AffCode: "qa"}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(sessions.Sessions("quota-test", cookie.NewStore([]byte("01234567890123456789012345678901"))))
	SetApiRouter(r)
	call := func(id, value int) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "/api/user/manage", strings.NewReader(fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"override","value":%d}`, id, value)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+adminToken)
		request.Header.Set("New-Api-User", fmt.Sprint(admin.Id))
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		return response
	}
	for index, scenario := range []struct {
		name     string
		old, new int
		fail     bool
	}{{"zero_to_positive", 0, 1000, false}, {"positive_to_zero", 1000, 0, false}, {"cache_failure_and_retry", 0, 700, true}, {"same_value_retry", 700, 700, false}} {
		t.Run(scenario.name, func(t *testing.T) {
			client := redis.NewClient(&redis.Options{Addr: address, DB: 11})
			defer client.Close()
			common.RDB = client
			if err := client.Ping(context.Background()).Err(); err != nil {
				t.Fatal(err)
			}
			id := 310 + index
			cacheKey := fmt.Sprintf("user:%d", id)
			defer client.Del(context.Background(), cacheKey)
			user := model.User{Id: id, Username: fmt.Sprintf("quota-user-%d", id), Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: scenario.old, Group: "default", AffCode: fmt.Sprint(id)}
			if err := db.Create(&user).Error; err != nil {
				t.Fatal(err)
			}
			if err := common.RedisHSetObj(cacheKey, user.ToBaseUser(), time.Minute); err != nil {
				t.Fatal(err)
			}
			hook := &quotaOverrideHook{cacheKey: cacheKey, gate: make(chan struct{}), reached: make(chan struct{}, 8), finished: make(chan struct{}, 8)}
			client.AddHook(hook)
			if scenario.fail {
				hook.failDelete.Store(true)
				response := call(id, scenario.new)
				if strings.Contains(response.Body.String(), `"success":true`) {
					t.Fatal("cache failure reported success")
				}
				var persisted model.User
				db.First(&persisted, id)
				if persisted.Quota != scenario.new {
					t.Fatal("test did not reach committed DB update")
				}
				if client.HGet(context.Background(), cacheKey, "Quota").Val() != fmt.Sprint(scenario.old) {
					t.Fatal("test invalidation failure did not preserve old cache")
				}
				var logs int64
				db.Model(&model.Log{}).Where("user_id = ?", id).Count(&logs)
				if logs != 0 {
					t.Fatal("failed invalidation recorded success audit")
				}
			}
			response := call(id, scenario.new)
			if response.Code != 200 || !strings.Contains(response.Body.String(), `"success":true`) {
				close(hook.gate)
				t.Fatalf("override=%d %s", response.Code, response.Body.String())
			}
			// All asynchronous fills are deliberately paused; these reads must see
			// the committed DB quota immediately, without relying on refill or TTL.
			quota, quotaErr := model.GetUserQuota(id, false)
			cached, cacheErr := model.GetUserCache(id)
			if quotaErr != nil || cacheErr != nil || quota != scenario.new || cached.Quota != scenario.new {
				close(hook.gate)
				t.Fatalf("immediate quota=%d cache=%+v errors=%v/%v", quota, cached, quotaErr, cacheErr)
			}
			// One audit lookup and the two reads above each schedule a full fill.
			for i := 0; i < 3; i++ {
				select {
				case <-hook.reached:
				case <-time.After(3 * time.Second):
					close(hook.gate)
					t.Fatal("expected cache fill not scheduled")
				}
			}
			close(hook.gate)
			for i := 0; i < 3; i++ {
				select {
				case <-hook.finished:
				case <-time.After(3 * time.Second):
					t.Fatal("cache fill did not finish")
				}
			}
			quota, quotaErr = model.GetUserQuota(id, false)
			cached, cacheErr = model.GetUserCache(id)
			if quotaErr != nil || cacheErr != nil || quota != scenario.new || cached.Quota != scenario.new {
				t.Fatal("refilled cache quota incorrect")
			}
		})
	}
	// Lookup must not treat a nonzero request ID as proof that the row exists.
	for _, id := range []int{0, -1, 999999, admin.Id} {
		response := call(id, 123)
		if strings.Contains(response.Body.String(), `"success":true`) {
			t.Fatalf("invalid/peer target %d accepted", id)
		}
	}
}
