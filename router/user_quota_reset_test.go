package router

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAdminQuotaResetExactlyOnceAndFence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:"+t.TempDir()+"/quota-reset.db"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	connection, _ := db.DB()
	defer connection.Close()
	oldDB, oldLogs, oldRedis := model.DB, model.LOG_DB, common.RedisEnabled
	defer func() { model.DB, model.LOG_DB, common.RedisEnabled = oldDB, oldLogs, oldRedis }()
	model.DB, model.LOG_DB, common.RedisEnabled = db, db, false
	if err := db.AutoMigrate(&model.User{}, &model.Log{}, &model.AdminQuotaResetOperation{}); err != nil {
		t.Fatal(err)
	}
	users := []model.User{
		{Id: 1, Username: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Quota: 60, AffCode: "reset-root"},
		{Id: 2, Username: "admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Quota: 60, AffCode: "reset-admin"},
		{Id: 3, Username: "ordinary", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 60, AffCode: "reset-ordinary"},
		{Id: 4, Username: "seat", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 60, Group: "carpool-test", AffCode: "reset-seat"},
		{Id: 5, Username: "disabled", Role: common.RoleCommonUser, Status: common.UserStatusDisabled, Quota: 60, Group: "carpool-test", AffCode: "reset-disabled"},
	}
	for i := range users {
		token := fmt.Sprintf("reset-token-%d", users[i].Id)
		users[i].AccessToken = &token
		if err := db.Create(&users[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := gin.New()
	r.Use(sessions.Sessions("quota-reset-test", cookie.NewStore([]byte("01234567890123456789012345678901"))))
	SetApiRouter(r)
	call := func(method, path, body string, actor int) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if actor > 0 {
			req.Header.Set("Authorization", fmt.Sprintf("Bearer reset-token-%d", actor))
			req.Header.Set("New-Api-User", fmt.Sprint(actor))
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	readQuota := func() int64 {
		var value int64
		if err := db.Model(&model.User{}).Where("id = ?", 4).Select("quota").Scan(&value).Error; err != nil {
			t.Fatal(err)
		}
		return value
	}
	op := strings.Repeat("a", 32)
	expires := time.Now().Add(time.Hour).Unix()
	body := fmt.Sprintf(`{"operation_id":%q,"delta_quota":40,"ceiling_quota":100,"expected_group":"carpool-test","expires_at":%d}`, op, expires)
	w := call("POST", "/api/user/4/quota/reset", body, 2)
	if w.Code != 200 || readQuota() != 100 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("initial reset: %d %s", w.Code, w.Body.String())
	}
	if err := db.Model(&model.User{}).Where("id = ?", 4).UpdateColumn("quota", gorm.Expr("quota - ?", 17)).Error; err != nil {
		t.Fatal(err)
	}
	w = call("POST", "/api/user/4/quota/reset", body, 2)
	if w.Code != 200 || readQuota() != 83 {
		t.Fatalf("replayed credit: %d %s quota=%d", w.Code, w.Body.String(), readQuota())
	}
	w = call("POST", "/api/user/4/quota/reset", strings.Replace(body, `"delta_quota":40`, `"delta_quota":41`, 1), 2)
	if w.Code != 409 || readQuota() != 83 {
		t.Fatal("changed payload accepted")
	}
	w = call("POST", "/api/user/4/quota/reset", body, 1)
	if w.Code != 409 || readQuota() != 83 {
		t.Fatal("another actor reused operation")
	}
	op2 := strings.Repeat("b", 32)
	w = call("DELETE", "/api/user/4/quota/reset/"+op2, "", 2)
	if w.Code != 200 {
		t.Fatalf("fence failed: %s", w.Body.String())
	}
	w = call("POST", "/api/user/4/quota/reset", strings.Replace(body, op, op2, 1), 2)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"cancelled":true`) || readQuota() != 83 {
		t.Fatal("late grant crossed cancellation fence")
	}
	w = call("DELETE", "/api/user/4/quota/reset/"+op, "", 2)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"applied":true`) || readQuota() != 83 {
		t.Fatal("cancellation undid consumption")
	}
	for _, actor := range []int{0, 3} {
		w := call("POST", "/api/user/4/quota/reset", body, actor)
		if w.Code == http.StatusOK && strings.Contains(w.Body.String(), `"success":true`) {
			t.Fatal("unauthorized reset")
		}
	}
	for _, target := range []int{1, 2, 5} {
		w := call("POST", fmt.Sprintf("/api/user/%d/quota/reset", target), strings.Replace(body, op, strings.Repeat("c", 32), 1), 2)
		if w.Code >= 200 && w.Code < 300 {
			t.Fatalf("protected user reset %d", target)
		}
	}
	for _, invalid := range []string{`{}`, strings.Replace(body, `"delta_quota":40`, `"delta_quota":1.5`, 1), strings.Replace(body, `"ceiling_quota":100`, `"ceiling_quota":null`, 1), strings.Replace(body, `"delta_quota":40`, `"delta_quota":9223372036854775808`, 1)} {
		w := call("POST", "/api/user/4/quota/reset", invalid, 2)
		if w.Code != 400 {
			t.Fatalf("invalid body accepted: %s", w.Body.String())
		}
	}
	in := model.AdminQuotaResetInput{OperationID: strings.Repeat("d", 32), ActorID: 2, ActorRole: common.RoleAdminUser, TargetID: 4, DeltaQuota: 40, CeilingQuota: 100, ExpectedGroup: "carpool-test", ExpiresAt: time.Now().Unix() - 1}
	if _, err := model.ApplyAdminQuotaReset(in); !errors.Is(err, model.ErrAdminResourceConflict) || readQuota() != 83 {
		t.Fatal("expired operation applied")
	}
	in.ExpiresAt = expires
	in.ExpectedGroup = "other"
	if _, err := model.ApplyAdminQuotaReset(in); !errors.Is(err, model.ErrAdminResourceConflict) {
		t.Fatal("group mismatch applied")
	}
	in.ExpectedGroup = "carpool-test"
	in.DeltaQuota = -90
	common.RedisEnabled = true
	if _, err := model.ApplyAdminQuotaReset(in); !errors.Is(err, model.ErrAdminResourceConflict) || readQuota() != 83 {
		t.Fatal("unfenced Redis configuration accepted reset")
	}
	common.RedisEnabled = false
	if result, err := model.ApplyAdminQuotaReset(in); err != nil || result.Quota != -7 {
		t.Fatalf("signed debt lost: %+v %v", result, err)
	}
	if err := db.Create(&[]model.Log{{Id: 10, UserId: 4, Type: 2}, {Id: 20, UserId: 3, Type: 2}, {Id: 30, UserId: 4, Type: 1}}).Error; err != nil {
		t.Fatal(err)
	}
	w = call("GET", "/api/user/4/usage-watermark", "", 2)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"log_id":10`) {
		t.Fatalf("watermark leaked other user/type: %s", w.Body.String())
	}
	// Fencing only changes this actor's operation receipt. It must remain
	// possible after an external administrator changes or removes the target.
	if err := db.Model(&model.User{}).Where("id = ?", 4).UpdateColumn("role", common.RoleAdminUser).Error; err != nil {
		t.Fatal(err)
	}
	w = call("DELETE", "/api/user/4/quota/reset/"+in.OperationID, "", 2)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"applied":true`) || readQuota() != -7 {
		t.Fatal("promoted target prevented harmless operation fence")
	}
	if err := db.Delete(&model.User{}, 4).Error; err != nil {
		t.Fatal(err)
	}
	w = call("DELETE", "/api/user/4/quota/reset/"+in.OperationID, "", 2)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"cancelled":true`) {
		t.Fatal("deleted target prevented cancellation receipt")
	}
	w = call("DELETE", "/api/user/4/quota/reset/"+in.OperationID, "", 1)
	if w.Code != 409 {
		t.Fatal("different actor could cancel another actor's receipt")
	}
}
