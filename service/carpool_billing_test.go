package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var testLunaAliases = []string{"gpt-6-luna", "gpt-5.6-luna", "openai-gpt-56-luna", "openai.gpt-5.6-luna", "openai/gpt-5.6-luna", "openai-gpt-56-luna-pro", "databricks-gpt-5-6-luna", "openai/gpt-5.6-luna-pro"}

type carpoolPricingFixture struct {
	Models []string `json:"models"`
	Groups []struct {
		Group string `json:"group"`
	} `json:"carpool_groups"`
	Pricing map[string]json.RawMessage `json:"pricing"`
}

var testPolicyStamp atomic.Int64

func setTestCarpoolPolicy(t *testing.T, groups []string, rules map[string]float64) {
	t.Helper()
	var next int64
	for {
		previous := testPolicyStamp.Load()
		next = max(time.Now().UnixNano(), previous+1)
		if testPolicyStamp.CompareAndSwap(previous, next) {
			break
		}
	}
	raw, err := common.Marshal(map[string]any{"version": 1, "issued_at": time.Unix(0, next).UTC().Format("2006-01-02T15:04:05.000000000Z"), "groups": groups, "model_multipliers": rules})
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMap[billing_setting.CarpoolBillingPolicyKey] = string(raw)
	common.OptionMapRWMutex.Unlock()
	billing_setting.CarpoolMultiplier("", "")
}

func loadCarpoolPricingFixture(t *testing.T) (carpoolPricingFixture, map[string]string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	data, err := os.ReadFile("testdata/carpool-inputs-20261020.json")
	require.NoError(t, err)
	var f carpoolPricingFixture
	require.NoError(t, common.Unmarshal(data, &f))
	oldData, err := os.ReadFile("testdata/carpool-pricing-20261020.json")
	require.NoError(t, err)
	var old map[string]json.RawMessage
	require.NoError(t, common.Unmarshal(oldData, &old))
	savedConfig := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(k, v string) error { savedConfig[k] = v; return nil }))
	updates := map[string]struct {
		read  func() string
		write func(string) error
	}{
		"ModelRatio":           {ratio_setting.ModelRatio2JSONString, ratio_setting.UpdateModelRatioByJSONString},
		"ModelPrice":           {ratio_setting.ModelPrice2JSONString, ratio_setting.UpdateModelPriceByJSONString},
		"CompletionRatio":      {ratio_setting.CompletionRatio2JSONString, ratio_setting.UpdateCompletionRatioByJSONString},
		"CacheRatio":           {ratio_setting.CacheRatio2JSONString, ratio_setting.UpdateCacheRatioByJSONString},
		"CreateCacheRatio":     {ratio_setting.CreateCacheRatio2JSONString, ratio_setting.UpdateCreateCacheRatioByJSONString},
		"ImageRatio":           {ratio_setting.ImageRatio2JSONString, ratio_setting.UpdateImageRatioByJSONString},
		"AudioRatio":           {ratio_setting.AudioRatio2JSONString, ratio_setting.UpdateAudioRatioByJSONString},
		"AudioCompletionRatio": {ratio_setting.AudioCompletionRatio2JSONString, ratio_setting.UpdateAudioCompletionRatioByJSONString},
		"GroupRatio":           {ratio_setting.GroupRatio2JSONString, ratio_setting.UpdateGroupRatioByJSONString},
		"GroupGroupRatio":      {ratio_setting.GroupGroupRatio2JSONString, ratio_setting.UpdateGroupGroupRatioByJSONString},
	}
	for name, io := range updates {
		saved := io.read()
		write := io.write
		t.Cleanup(func() { require.NoError(t, write(saved)) })
		raw := f.Pricing[name]
		if len(raw) == 0 || string(raw) == "null" {
			raw = old[name]
		}
		if len(raw) == 0 || string(raw) == "null" {
			raw = []byte(`{}`)
		}
		require.NoError(t, write(string(raw)), name)
	}
	configValues := map[string]string{}
	for _, key := range []string{"billing_setting.billing_mode", "billing_setting.billing_expr", "billing_setting.group_ratio_override"} {
		configValues[key] = string(f.Pricing[key])
	}
	require.NoError(t, config.GlobalConfig.LoadFromDB(configValues))
	var completionMeta map[string]ratio_setting.CompletionRatioInfo
	require.NoError(t, common.Unmarshal(f.Pricing["CompletionRatioMeta"], &completionMeta))
	for _, name := range f.Models {
		if expected, ok := completionMeta[name]; ok {
			require.Equal(t, expected, ratio_setting.GetCompletionRatioInfo(name), "production completion ratio metadata: "+name)
		}
	}
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(savedConfig))
		setTestCarpoolPolicy(t, []string{}, map[string]float64{})
	})
	setTestCarpoolPolicy(t, []string{}, map[string]float64{})
	return f, configValues
}

func carpoolTestInfo(t *testing.T, name, group, userGroup string, usage *dto.Usage) (*gin.Context, *relaycommon.RelayInfo, error) {
	t.Helper()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	now := time.Now()
	info := &relaycommon.RelayInfo{OriginModelName: name, UsingGroup: group, UserGroup: userGroup, StartTime: now, FirstResponseTime: now, BillingRequestInput: &billingexpr.RequestInput{}}
	info.RelayFormat = types.RelayFormatOpenAI
	info.InitRequestConversionChain() // Same initialization as GenRelayInfo.
	_, err := helper.ModelPriceHelper(ctx, info, usage.PromptTokens, &types.TokenCountMeta{MaxTokens: usage.CompletionTokens})
	return ctx, info, err
}

type carpoolMatrixValue struct {
	Pre, Final int
	Rejected   bool
}

func carpoolMatrixQuote(t *testing.T, name, group string, usage *dto.Usage) carpoolMatrixValue {
	t.Helper()
	ctx, info, err := carpoolTestInfo(t, name, group, "regular", usage)
	if name == "gpt-5-openai-compact" {
		require.Error(t, err, "production snapshot has no compact price or mapping")
		require.Contains(t, err.Error(), "has not been priced by the administrator")
		return carpoolMatrixValue{Rejected: true}
	}
	require.NoError(t, err, name)
	summary := calculateTextQuotaSummary(ctx, info, usage)
	if snap := info.TieredBillingSnapshot; snap != nil {
		ok, quota, result := TryTieredSettle(info, BuildTieredTokenParams(usage, false, billingexpr.UsedVars(snap.ExprString)))
		require.True(t, ok)
		require.NotNil(t, result)
		summary.Quota = composeTieredTextQuota(info, summary, quota, result)
	}
	return carpoolMatrixValue{Pre: info.PriceData.QuotaToPreConsume, Final: summary.Quota}
}

func officialLunaConfig(t *testing.T, original map[string]string) map[string]string {
	t.Helper()
	var exprs map[string]string
	require.NoError(t, common.UnmarshalJsonStr(original["billing_setting.billing_expr"], &exprs))
	for _, name := range testLunaAliases {
		if name == "gpt-6-luna" {
			exprs[name] = `len <= 272000 ? tier("0_272k", p * 0.1 + cr * 0.01 + cc * 0.125 + c * 0.5) : tier("272k_plus", p * 0.2 + cr * 0.02 + cc * 0.25 + c * 0.75)`
		} else {
			exprs[name] = `len <= 272000 ? tier("0_272k", p * 0.2 + cr * 0.02 + cc * 0.25 + c * 1.2) : tier("272k_plus", p * 0.4 + cr * 0.04 + cc * 0.5 + c * 1.8)`
		}
	}
	raw, err := common.Marshal(exprs)
	require.NoError(t, err)
	return map[string]string{"billing_setting.billing_expr": string(raw)}
}

func TestCarpoolProductionPriceMatrix(t *testing.T) {
	f, configuration := loadCarpoolPricingFixture(t)
	require.Len(t, f.Models, 38)
	names := append([]string{}, f.Models...)
	names = append(names, testLunaAliases[2:]...)
	groups := []string{}
	for _, g := range f.Groups {
		groups = append(groups, g.Group)
	}
	groups = append(groups, "default", "Pressure test", "GPT-daycard", "GPT-plus/team")
	usages := []dto.Usage{
		{PromptTokens: 10000, CompletionTokens: 1000},
		{PromptTokens: 300001, CompletionTokens: 12345},
		{PromptTokens: 100000, CompletionTokens: 10000, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 90000}},
		{PromptTokens: 100000, CompletionTokens: 10000, PromptTokensDetails: dto.InputTokenDetails{CachedCreationTokens: 5000}},
		{PromptTokens: 3, CompletionTokens: 1, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 1}},
	}
	quoteAll := func() map[string]carpoolMatrixValue {
		out := map[string]carpoolMatrixValue{}
		for _, name := range names {
			for _, group := range groups {
				for i := range usages {
					out[fmt.Sprintf("%s/%s/%d", name, group, i)] = carpoolMatrixQuote(t, name, group, &usages[i])
				}
			}
		}
		return out
	}
	baseline := quoteAll()
	carpoolGroups := groups[:4]
	setTestCarpoolPolicy(t, carpoolGroups, map[string]float64{})
	require.Equal(t, baseline, quoteAll(), "empty policy must preserve every preconsume and final rounding result")
	require.NoError(t, config.GlobalConfig.LoadFromDB(officialLunaConfig(t, configuration)))
	official := quoteAll()
	lunaRules := map[string]float64{}
	for _, name := range testLunaAliases {
		lunaRules[name] = 2
	}
	setTestCarpoolPolicy(t, carpoolGroups, lunaRules)
	migrated := quoteAll()
	for _, name := range names {
		for gi, group := range groups {
			for i := range usages {
				key := fmt.Sprintf("%s/%s/%d", name, group, i)
				want := baseline[key]
				if gi >= 4 && lunaRules[name] == 2 {
					want = official[key]
				}
				require.Equal(t, want, migrated[key], key)
			}
		}
	}
	require.NoError(t, config.GlobalConfig.LoadFromDB(configuration))
	setTestCarpoolPolicy(t, carpoolGroups, map[string]float64{"gpt-6.1-sol": 1.5})
	marked := quoteAll()
	setTestCarpoolPolicy(t, []string{}, map[string]float64{})
	groupValues := ratio_setting.GetGroupRatioCopy()
	for _, group := range carpoolGroups {
		groupValues[group] *= 1.5
	}
	raw, err := common.Marshal(groupValues)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(string(raw)))
	for _, name := range names {
		for gi, group := range groups {
			for i := range usages {
				key := fmt.Sprintf("%s/%s/%d", name, group, i)
				want := baseline[key]
				if gi < 4 && name == "gpt-6.1-sol" {
					want = carpoolMatrixQuote(t, name, group, &usages[i])
				}
				require.Equal(t, want, marked[key], key)
			}
		}
	}
	t.Logf("matrix: %d models (38 enabled + 6 aliases) x %d groups x %d token vectors x 3 assertions = %d; numeric comparisons %d, same unpriced rejection %d, mismatches 0", len(names), len(groups), len(usages), len(baseline)*3, (len(baseline)-40)*3, 40*3)
}

func TestCarpoolActualPreconsumeSettlementFixtures(t *testing.T) {
	f, configuration := loadCarpoolPricingFixture(t)
	group := f.Groups[0].Group
	groups := []string{}
	for _, g := range f.Groups {
		groups = append(groups, g.Group)
	}
	var fixtures []map[string]any
	for index, tc := range []struct {
		name, model, group, user string
		ratio                    float64
		official, freeze         bool
	}{
		{"expr_carpool", "gpt-6.1-sol", group, "regular", 1.5, false, false},
		{"expr_regular", "gpt-6.1-sol", "default", "regular", 1.5, false, false},
		{"ratio_carpool", "gpt-5.2", group, "regular", 1.5, false, false},
		{"ratio_special", "gpt-5.2", group, "vip", 1.5, false, false},
		{"ratio_special_regular", "gpt-5.2", "default", "vip", 1.5, false, false},
		{"per_call_carpool", "gpt-image-2", group, "regular", 1.5, false, false},
		{"per_call_regular", "gpt-image-2", "default", "regular", 1.5, false, false},
		{"luna_before", "gpt-6-luna", group, "vip", 0, false, false},
		{"luna_after", "gpt-6-luna", group, "vip", 2, true, false},
		{"luna_regular_after", "gpt-6-luna", "GPT-daycard", "vip", 2, true, false},
		{"frozen_request", "gpt-6.1-sol", group, "regular", 1.5, false, true},
		{"no_rule_then_x2", "gpt-6.1-sol", group, "regular", 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truncate(t)
			require.NoError(t, config.GlobalConfig.LoadFromDB(configuration))
			if tc.official {
				require.NoError(t, config.GlobalConfig.LoadFromDB(officialLunaConfig(t, configuration)))
			}
			special, err := common.Marshal(map[string]map[string]float64{"vip": {group: 0.2, "default": 0.32, "GPT-daycard": 0.01}})
			require.NoError(t, err)
			require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(string(special)))
			rules := map[string]float64{}
			if tc.ratio != 0 {
				rules[tc.model] = tc.ratio
			}
			setTestCarpoolPolicy(t, groups, rules)
			usage := &dto.Usage{PromptTokens: 100000, CompletionTokens: 10000, TotalTokens: 110000, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 90000, CachedCreationTokens: 5000}}
			ctx, info, err := carpoolTestInfo(t, tc.model, tc.group, tc.user, usage)
			require.NoError(t, err)
			id := 8800 + index
			const quota = 100000000
			seedUser(t, id, quota)
			seedChannel(t, id)
			info.UserId = id
			info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: id}
			info.UserQuota = quota
			info.IsPlayground = true
			info.ForcePreConsume = true
			info.UserSetting.BillingPreference = "wallet_only"
			require.Nil(t, PreConsumeBilling(ctx, info.PriceData.QuotaToPreConsume, info))
			require.Equal(t, quota-info.PriceData.QuotaToPreConsume, getUserQuota(t, id), "real wallet preconsume")
			if tc.freeze {
				if tc.ratio == 0 {
					require.Positive(t, info.PriceData.GroupRatioInfo.CarpoolPricedAt)
					setTestCarpoolPolicy(t, groups, map[string]float64{tc.model: 2})
					require.Nil(t, info.PriceData.GroupRatioInfo.Carpool)
				} else {
					setTestCarpoolPolicy(t, groups, map[string]float64{tc.model: 3})
					require.Equal(t, 1.5, helper.HandleGroupRatio(ctx, info).Carpool.Ratio)
				}
			}
			PostTextConsumeQuota(ctx, info, usage, nil)
			row := getLastLog(t)
			require.NotNil(t, row)
			require.Equal(t, quota-row.Quota, getUserQuota(t, id), "real final wallet settlement")
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(row.Other, &other))
			if info.PriceData.GroupRatioInfo.Carpool != nil {
				require.Equal(t, tc.ratio, other["carpool_ratio"])
				require.Equal(t, info.PriceData.GroupRatioInfo.LogGroupRatio(), other["group_ratio"])
				require.Equal(t, float64(info.PriceData.GroupRatioInfo.Carpool.PricedAt), other["carpool_priced_at"])
			} else {
				require.NotContains(t, other, "carpool_ratio")
				if info.PriceData.GroupRatioInfo.CarpoolPricedAt > 0 {
					require.Equal(t, float64(info.PriceData.GroupRatioInfo.CarpoolPricedAt), other["carpool_priced_at"])
				} else {
					require.NotContains(t, other, "carpool_priced_at")
				}
			}
			fixtures = append(fixtures, map[string]any{"case": tc.name, "model": row.ModelName, "group": row.Group, "created_at": row.CreatedAt, "quota": row.Quota, "prompt_tokens": row.PromptTokens, "completion_tokens": row.CompletionTokens, "other": other})
		})
	}
	if path := os.Getenv("CARPOOL_BILLING_FIXTURES_OUT"); path != "" {
		raw, err := common.Marshal(fixtures)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, raw, 0600))
	}
}

func TestCarpoolFrozenTaskAndLegacyTokenRecalculation(t *testing.T) {
	f, _ := loadCarpoolPricingFixture(t)
	group := f.Groups[0].Group
	for _, scenario := range []string{"frozen", "historical", "timestamp_only"} {
		t.Run(scenario, func(t *testing.T) {
			frozen := scenario == "frozen"
			truncate(t)
			seedUser(t, 8890, 10000000)
			seedChannel(t, 8890)
			task := makeTask(8890, 8890, 100, 0, BillingSourceWallet, 0)
			task.Group = group
			bc := task.PrivateData.BillingContext
			bc.OriginModelName = "gpt-5.2"
			bc.ModelRatio = 2
			bc.OtherRatios = map[string]float64{"duration": 2}
			if frozen {
				bc.CarpoolSnapshot = &types.CarpoolBillingSnapshot{Group: group, Model: "gpt-5.2", Ratio: 1.5, PricedAt: 123456, BaseGroupRatio: 0.2, EffectiveGroupRatio: 0.3, GroupSpecialRatio: 0.2, HasSpecialRatio: true}
			}
			if scenario == "timestamp_only" {
				bc.CarpoolPricedAt = 123456
			}
			raw, err := common.Marshal(task.PrivateData)
			require.NoError(t, err)
			task.PrivateData = model.TaskPrivateData{}
			require.NoError(t, common.Unmarshal(raw, &task.PrivateData))
			liveModel, _, _ := ratio_setting.GetModelRatio("gpt-5.2")
			want := int(1000 * liveModel * ratio_setting.GetGroupRatio(group) * 2)
			if frozen {
				want = 1200
			}
			RecalculateTaskQuotaByTokens(t.Context(), task, 1000)
			require.Equal(t, want, task.Quota)
			require.Equal(t, 10000000-(want-100), getUserQuota(t, 8890))
			row := getLastLog(t)
			require.NotNil(t, row)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(row.Other, &other))
			if frozen {
				require.Equal(t, 1.5, other["carpool_ratio"])
				require.Equal(t, 0.2, other["group_ratio"])
				require.Equal(t, 0.2, other["user_group_ratio"])
			} else {
				require.NotContains(t, other, "carpool_ratio")
				require.Nil(t, task.PrivateData.BillingContext.CarpoolSnapshot, "timestamp evidence must not switch legacy billing")
				if scenario == "timestamp_only" {
					require.Equal(t, float64(123456), other["carpool_priced_at"])
				} else {
					require.NotContains(t, other, "carpool_priced_at")
				}
			}
		})
	}
}

func TestCarpoolRealtimeUsesFrozenEffectiveRatioOnlyWhenPresent(t *testing.T) {
	f, _ := loadCarpoolPricingFixture(t)
	group := f.Groups[0].Group
	setTestCarpoolPolicy(t, []string{group}, map[string]float64{"gpt-5.2": 1.5})
	ctx, info, err := carpoolTestInfo(t, "gpt-5.2", group, "regular", &dto.Usage{PromptTokens: 1000})
	require.NoError(t, err)
	setTestCarpoolPolicy(t, []string{group}, map[string]float64{"gpt-5.2": 5})
	ctx.Set("auto_group", "GPT-daycard")
	_, got := realtimePreconsumeRatios(ctx, info)
	require.Equal(t, 1.5, got)
	require.Equal(t, group, info.UsingGroup)
	info.PriceData.GroupRatioInfo.Carpool = nil
	_, got = realtimePreconsumeRatios(ctx, info)
	require.Equal(t, 0.2, got)
	require.Equal(t, "GPT-daycard", info.UsingGroup)
}

func TestCarpoolRealtimeActualSegmentPreconsume(t *testing.T) {
	f, _ := loadCarpoolPricingFixture(t)
	group := f.Groups[0].Group
	truncate(t)
	const id = 8891
	const wallet = 1000000
	seedUser(t, id, wallet)
	seedToken(t, id, id, "carpool-realtime-test", wallet)
	// TestMain uses SQLite without model.InitDB, so supply the private quoting
	// helper's token predicate in this fixture only, as existing billing tests do.
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register("carpool:token_lookup", func(q *gorm.DB) {
		if q.Statement.Table == "tokens" {
			q.Statement.Clauses["WHERE"] = clause.Clause{}
			q.Statement.AddClause(clause.Where{Exprs: []clause.Expression{clause.Eq{Column: "key", Value: "carpool-realtime-test"}}})
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove("carpool:token_lookup")) })
	setTestCarpoolPolicy(t, []string{group}, map[string]float64{"gpt-5.2": 1.5})
	ctx, info, err := carpoolTestInfo(t, "gpt-5.2", group, "regular", &dto.Usage{PromptTokens: 1000})
	require.NoError(t, err)
	info.UserId = id
	info.TokenId = id
	info.TokenKey = "carpool-realtime-test"
	setTestCarpoolPolicy(t, []string{group}, map[string]float64{"gpt-5.2": 5})
	usage := &dto.RealtimeUsage{InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100}
	usage.InputTokenDetails.TextTokens = 1000
	usage.OutputTokenDetails.TextTokens = 100
	modelRatio, _, _ := ratio_setting.GetModelRatio("gpt-5.2")
	want := calculateAudioQuota(QuotaInfo{ModelName: "gpt-5.2", ModelRatio: modelRatio, GroupRatio: 1.5, InputDetails: TokenDetails{TextTokens: 1000}, OutputDetails: TokenDetails{TextTokens: 100}})
	for n := 1; n <= 2; n++ {
		require.NoError(t, PreWssConsumeQuota(ctx, info, usage))
		require.Equal(t, wallet-n*want, getUserQuota(t, id))
		require.Equal(t, wallet-n*want, getTokenRemainQuota(t, id))
	}
}
