package helper

import (
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var helperPolicyStamp atomic.Int64

func helperCarpoolPolicy(t *testing.T, groups []string, multipliers map[string]float64) {
	t.Helper()
	var next int64
	for {
		previous := helperPolicyStamp.Load()
		next = max(time.Now().UnixNano(), previous+1)
		if helperPolicyStamp.CompareAndSwap(previous, next) {
			break
		}
	}
	raw, err := common.Marshal(map[string]any{"version": 1, "issued_at": time.Unix(0, next).UTC().Format("2006-01-02T15:04:05.000000000Z"), "groups": groups, "model_multipliers": multipliers})
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMap[billing_setting.CarpoolBillingPolicyKey] = string(raw)
	common.OptionMapRWMutex.Unlock()
	billing_setting.CarpoolMultiplier("", "")
}

func TestCarpoolGroupRatioAllLegacyBranchesAndExactMatching(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(k, v string) error { saved[k] = v; return nil }))
	groups, special := ratio_setting.GroupRatio2JSONString(), ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groups))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(special))
		helperCarpoolPolicy(t, []string{}, map[string]float64{})
	})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"carpool-A":0.32,"carpool-AB":0.32,"Carpool-A":0.32,"carpool-A ":0.32,"regular":0.32}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"vip":{"carpool-A":0.2,"regular":0.2}}`))
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"billing_setting.group_ratio_override": `{"override":1}`}))
	helperCarpoolPolicy(t, []string{"carpool-A"}, map[string]float64{"normal": 1.5, "override": 2})
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	for _, tc := range []struct {
		group, model, user string
		want, base         float64
		special, carpool   bool
	}{
		{"carpool-A", "normal", "regular", 0.48, 0.32, false, true},
		{"carpool-A", "normal", "vip", 0.3, 0.2, true, true},
		{"carpool-A", "override", "vip", 2, 1, false, true},
		{"regular", "override", "vip", 1, 1, false, false},
		{"regular", "normal", "vip", 0.2, 0.2, true, false},
		{"regular", "normal", "regular", 0.32, 0.32, false, false},
		{"carpool-A", "unknown", "regular", 0.32, 0.32, false, false},
		{"carpool-AB", "normal", "regular", 0.32, 0.32, false, false},
		{"Carpool-A", "normal", "regular", 0.32, 0.32, false, false},
		{"carpool-A ", "normal", "regular", 0.32, 0.32, false, false},
	} {
		info := &relaycommon.RelayInfo{UsingGroup: tc.group, UserGroup: tc.user, OriginModelName: tc.model}
		got := HandleGroupRatio(ctx, info)
		require.InDelta(t, tc.want, got.GroupRatio, 1e-12, tc)
		require.Equal(t, tc.base, got.LogGroupRatio(), tc)
		require.Equal(t, tc.special, got.HasSpecialRatio, tc)
		require.Equal(t, tc.carpool, got.Carpool != nil, tc)
		if tc.group == "carpool-A" {
			require.Positive(t, got.CarpoolPricedAt, "known group needs pricing time even with no model rule")
		} else {
			require.Zero(t, got.CarpoolPricedAt)
		}
	}
	info := &relaycommon.RelayInfo{UsingGroup: "carpool-A", UserGroup: "regular", OriginModelName: "normal"}
	first := HandleGroupRatio(ctx, info)
	helperCarpoolPolicy(t, []string{"carpool-A"}, map[string]float64{"normal": 4})
	require.Equal(t, first, HandleGroupRatio(ctx, info), "retry must not reprice an already frozen scope")
	info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{GroupRatio: first.GroupRatio, EstimatedQuotaBeforeGroup: 100}
	ctx.Set("auto_group", "regular")
	next := HandleGroupRatio(ctx, info)
	require.Nil(t, next.Carpool)
	require.Equal(t, 0.32, next.GroupRatio)
	require.Equal(t, next.GroupRatio, info.TieredBillingSnapshot.GroupRatio, "auto-group retry must keep settlement and log in the same scope")
}
