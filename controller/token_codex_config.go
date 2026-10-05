package controller

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type codexConfigModel struct {
	ID                     string   `json:"id"`
	ReasoningEfforts       []string `json:"reasoning_efforts"`
	DefaultReasoningEffort string   `json:"default_reasoning_effort"`
}

type tokenCodexConfig struct {
	Client           string             `json:"client"`
	SchemaVersion    int                `json:"schema_version"`
	TemplateVersion  string             `json:"template_version"`
	TokenID          int                `json:"token_id"`
	Configurable     bool               `json:"configurable"`
	ReasonCode       *string            `json:"reason_code"`
	Reason           *string            `json:"reason"`
	BaseURL          string             `json:"base_url"`
	DefaultModel     *string            `json:"default_model"`
	Models           []codexConfigModel `json:"models"`
	SupportedTargets []string           `json:"supported_targets"`
	Notices          []string           `json:"notices"`
}

// Exact platform aliases supported by the existing installer; don't infer
// Responses support or reasoning capabilities from arbitrary model names.
var setupModelRegistry = []string{"gpt-5.6-sol", "gpt-6-sol", "gpt-6-astra", "gpt-6-luna", "gpt-6.1-sol"}
var setupURLCharacters = regexp.MustCompile(`^[A-Za-z0-9:/._~\[\]-]+$`)
var setupEndpointPattern = regexp.MustCompile(`^https://(?:[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?|\[[0-9A-Fa-f:]+\])(?::[0-9]{1,5})?(?:/[A-Za-z0-9._~-]+)*/v1$`)

func normalizeCodexBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	// The application's shipped localhost default is not a useful public URL.
	// Empty means the UI may fall back ONLY to its own HTTPS origin.
	if raw == "" || raw == "http://localhost:3000" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || !setupURLCharacters.MatchString(raw) || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("站点接入地址必须为不含账号、查询参数或片段的 HTTPS 地址")
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return "", errors.New("站点接入地址包含非法路径")
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	if !strings.HasSuffix(u.Path, "/v1") {
		u.Path += "/v1"
	}
	result := u.String()
	if port := u.Port(); port != "" {
		value, parseErr := strconv.Atoi(port)
		if parseErr != nil || value < 1 || value > 65535 {
			return "", errors.New("站点接入地址端口无效")
		}
	}
	if !setupEndpointPattern.MatchString(result) {
		return "", errors.New("站点接入地址包含不支持的字符")
	}
	return result, nil
}

func (r *tokenCodexConfig) block(code, message string) {
	r.Configurable = false
	r.ReasonCode, r.Reason = &code, &message
}

func GetTokenCodexConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store, private")
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "令牌 ID 无效"})
		return
	}
	token, err := model.GetTokenByIds(id, c.GetInt("id"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "令牌不存在"})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	result := tokenCodexConfig{Client: "codex", SchemaVersion: 1, TemplateVersion: "newapi-codex-v1", TokenID: token.Id,
		Models: []codexConfigModel{}, SupportedTargets: []string{"windows", "macos", "linux", "wsl"}, Notices: []string{}}
	finish := func() { common.ApiSuccess(c, result) }
	if token.Status != common.TokenStatusEnabled {
		result.block("token_disabled", "此令牌已禁用、到期或额度耗尽，请先恢复可用状态")
		finish()
		return
	}
	if token.ExpiredTime != -1 && token.ExpiredTime <= common.GetTimestamp() {
		result.block("token_expired", "此令牌已到期，请先调整有效期")
		finish()
		return
	}
	if !token.UnlimitedQuota && token.RemainQuota <= 0 {
		result.block("token_exhausted", "此令牌暂无可用额度")
		finish()
		return
	}
	user, err := model.GetUserById(token.UserId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if user.Status != common.UserStatusEnabled {
		result.block("user_disabled", "用户已停用，暂不能配置")
		finish()
		return
	}
	baseURL, err := normalizeCodexBaseURL(system_setting.ServerAddress)
	if err != nil {
		result.block("invalid_server_address", err.Error())
		finish()
		return
	}
	result.BaseURL = baseURL
	if baseURL == "" {
		result.Notices = append(result.Notices, "接入地址将使用当前站点的 HTTPS 地址。")
	}
	if len(token.GetIpLimits()) > 0 {
		result.Notices = append(result.Notices, "此令牌设有 IP 限制，目标电脑的出口 IP 必须在允许范围内。")
	}
	if user.Quota <= 0 {
		result.Notices = append(result.Notices, "用户余额为零，是否可调用还取决于有效订阅等计费来源，请以本机调用结果为准。")
	}
	group := user.Group
	if token.Group != "" {
		// Keep the same group authorization as TokenAuth. An old token can
		// outlive the subscription that granted its owner's previous group.
		if !service.GroupInUserUsableGroups(user.Group, token.Group) {
			result.block("group_forbidden", "此令牌指定的分组不在当前账号可用范围内，请编辑令牌选择可用分组，或恢复对应订阅后重试")
			finish()
			return
		}
		if token.Group != "auto" && !ratio_setting.ContainsGroupRatio(token.Group) {
			result.block("group_retired", "此令牌指定的分组已停用，请编辑令牌选择可用分组，或联系管理员恢复该分组")
			finish()
			return
		}
		group = token.Group
	}
	groups := []string{group}
	if group == "auto" {
		groups = service.GetUserAutoGroup(user.Group)
	}
	available, err := model.CodexSetupGroupModels(groups)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	allowed := make(map[string]bool, len(available))
	for _, name := range available {
		allowed[name] = true
	}
	limited := make(map[string]bool)
	if token.ModelLimitsEnabled {
		for _, name := range strings.Split(token.ModelLimits, ",") {
			limited[strings.TrimSpace(name)] = true
		}
	}
	acceptUnset := operation_setting.SelfUseModeEnabled || user.GetSetting().AcceptUnsetRatioModel
	for _, name := range setupModelRegistry {
		if !allowed[name] || (token.ModelLimitsEnabled && !limited[name]) || (!acceptUnset && !helper.HasModelBillingConfig(name)) {
			continue
		}
		result.Models = append(result.Models, codexConfigModel{ID: name, ReasoningEfforts: []string{"low"}, DefaultReasoningEffort: "low"})
		if name == "gpt-5.6-sol" {
			preferred := name
			result.DefaultModel = &preferred
		}
	}
	result.Configurable = len(result.Models) > 0
	if !result.Configurable {
		result.block("no_compatible_model", "此令牌的有效分组和模型限制下，没有可用于当前 Codex 模板的模型")
	}
	finish()
}
