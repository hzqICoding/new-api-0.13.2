package router

import (
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
)

func TestUsageAggregateRouteUsesExistingAdminAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:"+t.TempDir()+"/auth.db"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	oldDB, oldLogs, oldRedis := model.DB, model.LOG_DB, common.RedisEnabled
	defer func() { model.DB, model.LOG_DB, common.RedisEnabled = oldDB, oldLogs, oldRedis }()
	model.DB, model.LOG_DB, common.RedisEnabled = db, db, false
	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatal(err)
	}
	for _, role := range []int{common.RoleAdminUser, common.RoleCommonUser} {
		token := fmt.Sprintf("usage-aggregate-auth-%d", role)
		user := model.User{Id: role, Username: fmt.Sprintf("aggregate-%d", role), Role: role, Status: common.UserStatusEnabled, AffCode: fmt.Sprint(role), AccessToken: &token}
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := gin.New()
	r.Use(sessions.Sessions("aggregate-test", cookie.NewStore([]byte("01234567890123456789012345678901"))))
	SetApiRouter(r)
	t.Setenv("CARPOOL_USAGE_AGGREGATE_ENABLED", "true")
	for _, actor := range []int{0, common.RoleCommonUser, common.RoleAdminUser} {
		request := httptest.NewRequest(http.MethodPost, "/api/log/usage/aggregate", strings.NewReader(`{"version":1,"tokens":[]}`))
		request.Header.Set("Content-Type", "application/json")
		if actor != 0 {
			request.Header.Set("Authorization", fmt.Sprintf("Bearer usage-aggregate-auth-%d", actor))
			request.Header.Set("New-Api-User", fmt.Sprint(actor))
		}
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		if actor == 0 && response.Code != 401 {
			t.Fatalf("anonymous=%d %s", response.Code, response.Body.String())
		}
		// Existing AdminAuth uses HTTP 200/success:false for insufficient role.
		if actor == common.RoleCommonUser && (response.Code != 200 || !strings.Contains(response.Body.String(), `"success":false`)) {
			t.Fatalf("ordinary role=%d %s", response.Code, response.Body.String())
		}
		if actor == common.RoleAdminUser && (response.Code != 200 || !strings.Contains(response.Body.String(), `"complete":true`)) {
			t.Fatalf("admin=%d %s", response.Code, response.Body.String())
		}
	}
}
