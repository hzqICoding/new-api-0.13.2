package router

import (
	"bytes"
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAdminResourceHTTPAuthIdempotencyAndRedis(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB, oldRedis, oldRDB, oldTurnstile := model.DB, common.RedisEnabled, common.RDB, common.TurnstileCheckEnabled
	defer func() {
		model.DB = oldDB
		common.RedisEnabled = oldRedis
		common.RDB = oldRDB
		common.TurnstileCheckEnabled = oldTurnstile
	}()
	db, err := gorm.Open(sqlite.Open("file:"+t.TempDir()+"/admin.db"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	oldPath, oldMaster := common.SQLitePath, common.IsMasterNode
	t.Setenv("SQL_DSN", "local")
	common.SQLitePath = t.TempDir() + "/columns.db"
	common.IsMasterNode = false
	if err := model.InitDB(); err != nil {
		t.Fatal(err)
	}
	initSQL, _ := model.DB.DB()
	initSQL.Close()
	common.SQLitePath = oldPath
	common.IsMasterNode = oldMaster
	model.DB = db
	common.RedisEnabled = false
	common.TurnstileCheckEnabled = true
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.AdminResourceOperation{}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []struct {
		id, role int
		token    string
	}{{1, common.RoleRootUser, "root-test"}, {2, common.RoleAdminUser, "admin-test"}, {3, common.RoleCommonUser, "ordinary-test"}, {4, common.RoleCommonUser, ""}, {5, common.RoleAdminUser, "peer-test"}} {
		user := model.User{Id: u.id, Username: fmt.Sprintf("user%d", u.id), Role: u.role, Status: common.UserStatusEnabled, Group: "default", Quota: 10000, AffCode: fmt.Sprint(u.id)}
		if u.token != "" {
			token := u.token
			user.AccessToken = &token
		}
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := gin.New()
	r.Use(sessions.Sessions("test", cookie.NewStore([]byte("01234567890123456789012345678901"))))
	SetApiRouter(r)
	r.GET("/test-relay", middleware.TokenAuth(), func(c *gin.Context) { c.Status(204) })
	call := func(method, path, body, auth string, user int) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
			req.Header.Set("New-Api-User", fmt.Sprint(user))
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	success := func(w *httptest.ResponseRecorder) bool {
		return w.Code >= 200 && w.Code < 300 && bytes.Contains(w.Body.Bytes(), []byte(`"success":true`))
	}
	t.Run("turnstile", func(t *testing.T) {
		w := call("POST", "/api/user/login", `{"username":"user4","password":"password"}`, "", 0)
		if success(w) || !strings.Contains(w.Body.String(), "Turnstile") {
			t.Fatalf("login=%d %s", w.Code, w.Body.String())
		}
	})
	body := `{"operation_id":"` + strings.Repeat("a", 32) + `","name":"laptop","group":"default"}`
	t.Run("negative_auth_no_mutation", func(t *testing.T) {
		for _, v := range []struct {
			auth          string
			actor, target int
		}{{"", 0, 4}, {"ordinary-test", 3, 4}, {"admin-test", 2, 5}, {"admin-test", 2, 1}} {
			w := call("POST", fmt.Sprintf("/api/user/%d/tokens", v.target), body, v.auth, v.actor)
			if success(w) {
				t.Fatalf("unexpected authorized result: %s", w.Body.String())
			}
		}
		var count int64
		db.Model(&model.Token{}).Count(&count)
		if count != 0 {
			t.Fatalf("tokens=%d", count)
		}
	})
	w := call("POST", "/api/user/4/tokens", body, "admin-test", 2)
	if !success(w) {
		t.Fatalf("create=%d %s", w.Code, w.Body.String())
	}
	var response struct {
		Data struct {
			ID  int    `json:"id"`
			Key string `json:"key"`
		} `json:"data"`
	}
	if err := common.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	id, key := response.Data.ID, response.Data.Key
	t.Run("reveal_key_is_scoped_and_uncached", func(t *testing.T) {
		path := fmt.Sprintf("/api/user/4/tokens/%d/key", id)
		shown := call("POST", path, `{}`, "admin-test", 2)
		if !success(shown) || !strings.Contains(shown.Body.String(), key) || !strings.Contains(shown.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("authorized key reveal failed")
		}
		for _, v := range []struct {
			actor  int
			auth   string
			target int
		}{{0, "", 4}, {3, "ordinary-test", 4}, {2, "admin-test", 3}, {2, "admin-test", 5}, {2, "admin-test", 1}} {
			if success(call("POST", fmt.Sprintf("/api/user/%d/tokens/%d/key", v.target, id), `{}`, v.auth, v.actor)) {
				t.Fatal("key reveal authorization/ownership bypass")
			}
		}
		if success(call("POST", path, `{"user_id":"3"}`, "admin-test", 2)) {
			t.Fatal("unknown reveal field accepted")
		}
		if err := db.Model(&model.Token{}).Where("id = ?", id).Update("status", common.TokenStatusDisabled).Error; err != nil {
			t.Fatal(err)
		}
		if success(call("POST", path, `{}`, "admin-test", 2)) {
			t.Fatal("disabled token revealed")
		}
		db.Model(&model.Token{}).Where("id = ?", id).Update("status", common.TokenStatusEnabled)
		db.Model(&model.Token{}).Where("id = ?", id).Update("expired_time", 0)
		if success(call("POST", path, `{}`, "admin-test", 2)) {
			t.Fatal("expired token revealed")
		}
		db.Model(&model.Token{}).Where("id = ?", id).Update("expired_time", -1)
		meta := call("GET", "/api/user/4/tokens", "", "admin-test", 2)
		if !strings.Contains(meta.Body.String(), `"key_masked":"sk-`) || strings.Contains(meta.Body.String(), key) {
			t.Fatal("metadata must expose only masked key")
		}
	})
	t.Run("null_access_token_replay_and_metadata", func(t *testing.T) {
		replay := call("POST", "/api/user/4/tokens", body, "admin-test", 2)
		if !success(replay) || replay.Body.String() != w.Body.String() {
			t.Fatalf("replay=%s", replay.Body.String())
		}
		var user model.User
		db.First(&user, 4)
		if user.AccessToken != nil {
			t.Fatal("target access token was populated")
		}
		meta := call("GET", "/api/user/4/tokens?p=1&page_size=100", "", "admin-test", 2)
		if !success(meta) || strings.Contains(meta.Body.String(), key) || strings.Contains(meta.Body.String(), `"key"`) {
			t.Fatalf("metadata=%s", meta.Body.String())
		}
		for _, query := range []string{"p=0", "page_size=101", "p=1000001"} {
			if success(call("GET", "/api/user/4/tokens?"+query, "", "admin-test", 2)) {
				t.Fatal("bad pagination accepted")
			}
		}
		injected := strings.Replace(body, `"name":"laptop"`, `"name":"laptop","user_id":"3"`, 1)
		if success(call("POST", "/api/user/4/tokens", injected, "admin-test", 2)) {
			t.Fatal("unknown field accepted")
		}
	})
	t.Run("wrong_owner", func(t *testing.T) {
		if success(call("DELETE", fmt.Sprintf("/api/user/3/tokens/%d", id), "", "admin-test", 2)) {
			t.Fatal("wrong owner accepted")
		}
		var count int64
		db.Model(&model.Token{}).Where("id = ?", id).Count(&count)
		if count != 1 {
			t.Fatal("wrong owner mutated token")
		}
	})
	t.Run("redis_cached_key_revoked", func(t *testing.T) {
		address := os.Getenv("NEWAPI_TEST_REDIS_ADDR")
		if address == "" {
			t.Skip("set NEWAPI_TEST_REDIS_ADDR to isolated Redis")
		}
		common.RDB = redis.NewClient(&redis.Options{Addr: address, DB: 15})
		defer common.RDB.Close()
		common.RedisEnabled = true
		defer func() { common.RedisEnabled = false }()
		if err := common.RDB.Ping(context.Background()).Err(); err != nil {
			t.Fatal(err)
		}
		cacheKey := "token:" + common.GenerateHMAC(strings.TrimPrefix(key, "sk-"))
		var token model.Token
		if err := db.First(&token, id).Error; err != nil {
			t.Fatal(err)
		}
		token.Clean()
		if err := common.RedisHSetObj(cacheKey, &token, time.Minute); err != nil {
			t.Fatal(err)
		}
		defer common.RDB.Del(context.Background(), cacheKey, "user:4")
		req := httptest.NewRequest("GET", "/test-relay", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		valid := httptest.NewRecorder()
		r.ServeHTTP(valid, req)
		if valid.Code != 204 {
			t.Fatalf("cached key rejected before revoke: %d %s", valid.Code, valid.Body.String())
		}
		if !success(call("DELETE", fmt.Sprintf("/api/user/4/tokens/%d", id), "", "admin-test", 2)) {
			t.Fatal("revoke failed")
		}
		if common.RDB.HGet(context.Background(), cacheKey, "_admin_revoked").Val() != "1" {
			t.Fatal("token revocation marker missing")
		}
		invalid := httptest.NewRecorder()
		r.ServeHTTP(invalid, req)
		if invalid.Code != 401 {
			t.Fatalf("revoked key accepted: %d %s", invalid.Code, invalid.Body.String())
		}
		if !success(call("DELETE", fmt.Sprintf("/api/user/4/tokens/%d", id), "", "admin-test", 2)) {
			t.Fatal("repeat revoke failed")
		}
	})
	t.Run("cancel_before_create_and_soft_cleanup", func(t *testing.T) {
		cancelled := strings.Repeat("b", 32)
		if !success(call("DELETE", "/api/user/provision/"+cancelled, "", "admin-test", 2)) {
			t.Fatal("cancel failed")
		}
		if success(call("POST", "/api/user/provision/"+cancelled, `{"username":"late-user","password":"password123"}`, "admin-test", 2)) {
			t.Fatal("cancelled request created user")
		}
		op := strings.Repeat("c", 32)
		created := call("POST", "/api/user/provision/"+op, `{"username":"new-user","password":"password123"}`, "admin-test", 2)
		if !success(created) {
			t.Fatalf("create user=%s", created.Body.String())
		}
		var record struct {
			Data struct {
				ID int `json:"id"`
			} `json:"data"`
		}
		common.Unmarshal(created.Body.Bytes(), &record)
		if !success(call("DELETE", "/api/user/provision/"+op, "", "admin-test", 2)) {
			t.Fatal("cleanup failed")
		}
		var user model.User
		db.Unscoped().First(&user, record.Data.ID)
		if !user.DeletedAt.Valid || user.Quota != 0 || user.Status != common.UserStatusDisabled {
			t.Fatal("user not soft cleaned")
		}
	})
}
