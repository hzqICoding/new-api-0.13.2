package controller

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func setupCodexConfigTest(t *testing.T) (*gorm.DB, *model.Token) {
	t.Helper()
	db := setupTokenControllerTestDB(t)
	if err := db.AutoMigrate(&model.User{}, &model.Ability{}); err != nil {
		t.Fatal(err)
	}
	user := model.User{Id: 1, Username: "setup-owner", Status: common.UserStatusEnabled, Group: "default", Quota: 1000}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	oldAddress, oldSelfUse := system_setting.ServerAddress, operation_setting.SelfUseModeEnabled
	oldGroups, oldAuto, oldRatios := setting.UserUsableGroups2JSONString(), setting.AutoGroups2JsonString(), ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		system_setting.ServerAddress = oldAddress
		operation_setting.SelfUseModeEnabled = oldSelfUse
		_ = setting.UpdateUserUsableGroupsByJSONString(oldGroups)
		_ = setting.UpdateAutoGroupsByJsonString(oldAuto)
		_ = ratio_setting.UpdateGroupRatioByJSONString(oldRatios)
	})
	system_setting.ServerAddress = "https://gateway.example.com/"
	operation_setting.SelfUseModeEnabled = true
	if err := setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","auto":"Auto"}`); err != nil {
		t.Fatal(err)
	}
	if err := setting.UpdateAutoGroupsByJsonString(`["default","vip","forbidden"]`); err != nil {
		t.Fatal(err)
	}
	if err := ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1}`); err != nil {
		t.Fatal(err)
	}
	for i, entry := range []struct {
		group, model string
		enabled      bool
	}{
		{"default", "gpt-5.6-sol", true}, {"vip", "gpt-6-astra", true}, {"forbidden", "gpt-6-luna", true}, {"default", "gpt-6-sol", false}, {"default", "claude-opus", true},
	} {
		if err := db.Create(&model.Ability{Group: entry.group, Model: entry.model, ChannelId: i + 1, Enabled: entry.enabled}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, seedToken(t, db, 1, "setup-token", "this-is-a-test-key-not-a-real-secret")
}

func getCodexTestConfig(t *testing.T, id, userID int) (int, tokenCodexConfig, string) {
	t.Helper()
	c, r := newAuthenticatedContext(t, http.MethodGet, "/api/token/"+strconv.Itoa(id)+"/codex-config", nil, userID)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(id)}}
	GetTokenCodexConfig(c)
	if r.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatal("missing no-store")
	}
	var envelope struct {
		Success bool             `json:"success"`
		Data    tokenCodexConfig `json:"data"`
	}
	if err := common.Unmarshal(r.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return r.Code, envelope.Data, r.Body.String()
}

func TestTokenCodexConfigOwnershipAndGroupIntersection(t *testing.T) {
	db, token := setupCodexConfigTest(t)
	code, result, raw := getCodexTestConfig(t, token.Id, 1)
	if code != 200 || !result.Configurable || len(result.Models) != 1 || result.Models[0].ID != "gpt-5.6-sol" || result.DefaultModel == nil || *result.DefaultModel != "gpt-5.6-sol" {
		t.Fatalf("%d %+v", code, result)
	}
	if strings.Contains(raw, token.Key) || strings.Contains(raw, "remain_quota") || strings.Contains(raw, "user_id") {
		t.Fatal("private fields leaked")
	}
	if result.BaseURL != "https://gateway.example.com/v1" || result.TemplateVersion != "newapi-codex-v1" {
		t.Fatalf("%+v", result)
	}
	if code, _, _ := getCodexTestConfig(t, token.Id, 2); code != 404 {
		t.Fatalf("other owner status %d", code)
	}
	if err := db.Model(token).Updates(map[string]interface{}{"group": "vip", "model_limits_enabled": true, "model_limits": "gpt-5.6-sol,gpt-6-astra,gpt-6-luna"}).Error; err != nil {
		t.Fatal(err)
	}
	_, result, _ = getCodexTestConfig(t, token.Id, 1)
	if !result.Configurable || len(result.Models) != 1 || result.Models[0].ID != "gpt-6-astra" || result.DefaultModel != nil {
		t.Fatalf("token group/filter mismatch %+v", result)
	}
	if err := db.Model(token).Update("group", "auto").Error; err != nil {
		t.Fatal(err)
	}
	_, result, _ = getCodexTestConfig(t, token.Id, 1)
	if len(result.Models) != 2 {
		t.Fatalf("auto included forbidden group or missed allowed groups %+v", result)
	}
	if err := db.Model(token).Update("group", "forbidden").Error; err != nil {
		t.Fatal(err)
	}
	_, result, _ = getCodexTestConfig(t, token.Id, 1)
	if result.Configurable || result.ReasonCode == nil || *result.ReasonCode != "group_forbidden" {
		t.Fatalf("%+v", result)
	}
}

// Regression for token #490: unlimited quota with a negative balance is valid,
// but a token does not retain a subscription-only group after the owner reverts.
func TestTokenCodexConfigSubscriptionGroupReverted(t *testing.T) {
	db, token := setupCodexConfigTest(t)
	if err := db.Model(token).Updates(map[string]interface{}{
		"group": "daycard", "unlimited_quota": true, "remain_quota": -588,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.User{}).Where("id = ?", 1).Update("group", "daycard").Error; err != nil {
		t.Fatal(err)
	}
	if err := ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1,"daycard":0.2}`); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Ability{Group: "daycard", Model: "gpt-5.6-sol", ChannelId: 50, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	_, active, _ := getCodexTestConfig(t, token.Id, 1)
	if !active.Configurable || len(active.Models) != 1 {
		t.Fatalf("own subscription group must remain usable: %+v", active)
	}
	if err := db.Model(&model.User{}).Where("id = ?", 1).Update("group", "default").Error; err != nil {
		t.Fatal(err)
	}
	_, expired, _ := getCodexTestConfig(t, token.Id, 1)
	if expired.Configurable || expired.ReasonCode == nil || *expired.ReasonCode != "group_forbidden" {
		t.Fatalf("old token must not bypass reverted group: %+v", expired)
	}
	if err := db.Model(token).Update("group", "default").Error; err != nil {
		t.Fatal(err)
	}
	_, changed, _ := getCodexTestConfig(t, token.Id, 1)
	if !changed.Configurable {
		t.Fatalf("available group and unlimited quota must work: %+v", changed)
	}
	if err := ratio_setting.UpdateGroupRatioByJSONString(`{"vip":1}`); err != nil {
		t.Fatal(err)
	}
	_, retired, _ := getCodexTestConfig(t, token.Id, 1)
	if retired.Configurable || retired.ReasonCode == nil || *retired.ReasonCode != "group_retired" {
		t.Fatalf("retired group needs a distinct reason: %+v", retired)
	}
}

func TestTokenCodexConfigStateAndAccessNotices(t *testing.T) {
	for _, tc := range []struct {
		name    string
		changes map[string]interface{}
		reason  string
	}{
		{"disabled", map[string]interface{}{"status": common.TokenStatusDisabled}, "token_disabled"},
		{"expired", map[string]interface{}{"expired_time": 1}, "token_expired"},
		{"empty", map[string]interface{}{"unlimited_quota": false, "remain_quota": 0}, "token_exhausted"},
		{"empty_allowlist", map[string]interface{}{"model_limits_enabled": true, "model_limits": ""}, "no_compatible_model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, token := setupCodexConfigTest(t)
			if err := db.Model(token).Updates(tc.changes).Error; err != nil {
				t.Fatal(err)
			}
			_, r, _ := getCodexTestConfig(t, token.Id, 1)
			if r.Configurable || r.ReasonCode == nil || *r.ReasonCode != tc.reason {
				t.Fatalf("%+v", r)
			}
		})
	}
	db, token := setupCodexConfigTest(t)
	if err := db.Model(token).Update("allow_ips", "192.0.2.0/24").Error; err != nil {
		t.Fatal(err)
	}
	_, r, _ := getCodexTestConfig(t, token.Id, 1)
	if !r.Configurable || len(r.Notices) == 0 {
		t.Fatalf("IP restriction not surfaced %+v", r)
	}
	if err := db.Model(&model.User{}).Where("id = ?", 1).Update("status", common.UserStatusDisabled).Error; err != nil {
		t.Fatal(err)
	}
	_, r, _ = getCodexTestConfig(t, token.Id, 1)
	if r.Configurable || r.ReasonCode == nil || *r.ReasonCode != "user_disabled" {
		t.Fatalf("%+v", r)
	}
}

func TestCodexSetupBaseURL(t *testing.T) {
	for in, want := range map[string]string{"": "", "http://localhost:3000": "", "https://api.example.com": "https://api.example.com/v1", "https://api.example.com/v1/": "https://api.example.com/v1", "https://api.example.com/proxy": "https://api.example.com/proxy/v1"} {
		got, err := normalizeCodexBaseURL(in)
		if err != nil || got != want {
			t.Fatalf("%q -> %q %v", in, got, err)
		}
	}
	for _, in := range []string{"http://api.example.com", "https://user:pass@api.example.com", "https://api.example.com?key=x", "https://api.example.com/#x", "https://api.example.com/../x", "https://api.example.com/$(id)", "https://api.example.com/\nfoo"} {
		if _, err := normalizeCodexBaseURL(in); err == nil {
			t.Fatalf("accepted %q", in)
		}
	}
}

func TestAddTokenReturnsExactCreatedIDWithoutSecret(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	c, r := newAuthenticatedContext(t, http.MethodPost, "/api/token/", map[string]interface{}{"name": "new-setup-key", "expired_time": -1, "unlimited_quota": true}, 1)
	AddToken(c)
	response := decodeAPIResponse(t, r)
	var created tokenResponseItem
	if !response.Success {
		t.Fatalf("create: %s", response.Message)
	}
	if err := common.Unmarshal(response.Data, &created); err != nil {
		t.Fatal(err)
	}
	var stored model.Token
	if err := db.First(&stored, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if created.ID <= 0 || created.Name != "new-setup-key" || created.Key != stored.GetMaskedKey() || strings.Contains(r.Body.String(), stored.Key) {
		t.Fatal("created ID/masked-key contract failed")
	}
}
