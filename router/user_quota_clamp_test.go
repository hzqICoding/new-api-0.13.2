package router

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAdminClampUserQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:"+t.TempDir()+"/quota-clamp.db"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	oldDB, oldRedis := model.DB, common.RedisEnabled
	defer func() {
		model.DB = oldDB
		common.RedisEnabled = oldRedis
	}()
	model.DB = db
	common.RedisEnabled = false
	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatal(err)
	}
	users := []model.User{
		{Id: 1, Username: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Quota: 100, AffCode: "clamp-root"},
		{Id: 2, Username: "admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Quota: 100, AffCode: "clamp-admin"},
		{Id: 3, Username: "ordinary", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 100, AffCode: "clamp-ordinary"},
		{Id: 4, Username: "target", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 100, AffCode: "clamp-target"},
		{Id: 5, Username: "disabled", Role: common.RoleCommonUser, Status: common.UserStatusDisabled, Quota: 100, AffCode: "clamp-disabled"},
	}
	for index := range users {
		token := fmt.Sprintf("clamp-token-%d", users[index].Id)
		users[index].AccessToken = &token
		if err := db.Create(&users[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := gin.New()
	r.Use(sessions.Sessions("quota-clamp-test", cookie.NewStore([]byte("01234567890123456789012345678901"))))
	SetApiRouter(r)
	call := func(target int, body, token string, actor int) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/user/%d/quota/clamp", target), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("New-Api-User", fmt.Sprint(actor))
		}
		response := httptest.NewRecorder()
		r.ServeHTTP(response, req)
		return response
	}
	quota := func(id int) int64 {
		t.Helper()
		var value int64
		if err := db.Model(&model.User{}).Where("id = ?", id).Select("quota").Scan(&value).Error; err != nil {
			t.Fatal(err)
		}
		return value
	}

	down := call(4, `{"quota":60}`, "clamp-token-2", 2)
	if down.Code != http.StatusOK || !strings.Contains(down.Body.String(), `"quota":60`) || quota(4) != 60 || down.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("atomic lower failed: status=%d body=%s quota=%d cache=%q", down.Code, down.Body.String(), quota(4), down.Header().Get("Cache-Control"))
	}
	if err := db.Model(&model.User{}).Where("id = ?", 4).Update("quota", 40).Error; err != nil {
		t.Fatal(err)
	}
	lowerAlready := call(4, `{"quota":60}`, "clamp-token-2", 2)
	if lowerAlready.Code != http.StatusOK || !strings.Contains(lowerAlready.Body.String(), `"quota":40`) || quota(4) != 40 {
		t.Fatalf("lower quota was increased: status=%d body=%s quota=%d", lowerAlready.Code, lowerAlready.Body.String(), quota(4))
	}
	if err := db.Model(&model.User{}).Where("id = ?", 4).Update("quota", -7).Error; err != nil {
		t.Fatal(err)
	}
	negative := call(4, `{"quota":60}`, "clamp-token-2", 2)
	if negative.Code != http.StatusOK || !strings.Contains(negative.Body.String(), `"quota":-7`) || quota(4) != -7 {
		t.Fatalf("negative quota was increased: status=%d body=%s quota=%d", negative.Code, negative.Body.String(), quota(4))
	}

	for _, body := range []string{
		`{"quota":-1}`,
		`{"quota":1.5}`,
		`{"quota":"1"}`,
		`{"quota":9223372036854775808}`,
		`{"quota":1,"quota":2}`,
		`{"quota":1,"user_id":4}`,
	} {
		response := call(4, body, "clamp-token-2", 2)
		if response.Code != http.StatusBadRequest || quota(4) != -7 {
			t.Errorf("invalid body accepted: body=%s status=%d response=%s quota=%d", body, response.Code, response.Body.String(), quota(4))
		}
	}

	for _, request := range []struct {
		target int
		token  string
		actor  int
	}{
		{target: 4},
		{target: 4, token: "clamp-token-3", actor: 3},
		{target: 2, token: "clamp-token-2", actor: 2},
		{target: 1, token: "clamp-token-2", actor: 2},
		{target: 5, token: "clamp-token-2", actor: 2},
	} {
		response := call(request.target, `{"quota":0}`, request.token, request.actor)
		if response.Code >= 200 && response.Code < 300 && strings.Contains(response.Body.String(), `"success":true`) {
			t.Errorf("unauthorized clamp succeeded: %+v body=%s", request, response.Body.String())
		}
	}
	// The model must repeat target protection after acquiring its own row lock.
	for _, target := range []int{1, 2, 5} {
		before := quota(target)
		if _, err := model.ClampUserQuota(target, 0, common.RoleAdminUser); !errors.Is(err, model.ErrAdminResourceConflict) {
			t.Fatalf("locked target check missing for %d: %v", target, err)
		}
		if quota(target) != before {
			t.Fatalf("protected target quota changed: %d", target)
		}
	}
}
