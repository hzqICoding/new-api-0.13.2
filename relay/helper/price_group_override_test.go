package helper

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLunaGroupOverridePreconsumeAndSettle(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(k, v string) error { saved[k] = v; return nil }))
	groups, special := ratio_setting.GroupRatio2JSONString(), ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groups))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(special))
	})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"discount":0.2,"plus":0.32,"free":0,"premium":1.35}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"vip":{"discount":0.05}}`))
	expr := `len <= 272000 ? tier("short", p * 0.13 + c * 0.65 + cr * 0.013 + cc * 0.1625) : tier("long", p * 0.26 + c * 0.975 + cr * 0.026 + cc * 0.325)`
	exprs, err := common.Marshal(map[string]string{"gpt-6-luna": expr})
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"gpt-6-luna":"tiered_expr"}`,
		"billing_setting.billing_expr":         string(exprs),
		"billing_setting.group_ratio_override": `{"gpt-6-luna":1}`,
	}))
	for _, group := range []string{"discount", "plus", "free", "premium"} {
		t.Run(group, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set("auto_group", group)
			info := &relaycommon.RelayInfo{OriginModelName: "gpt-6-luna", UserGroup: "vip", UsingGroup: "auto", BillingRequestInput: &billingexpr.RequestInput{}}
			price, err := ModelPriceHelper(ctx, info, 100000, &types.TokenCountMeta{MaxTokens: 10000})
			require.NoError(t, err)
			require.Equal(t, group, info.UsingGroup)
			require.Equal(t, 1.0, price.GroupRatioInfo.GroupRatio)
			require.False(t, price.GroupRatioInfo.HasSpecialRatio)
			require.False(t, price.FreeModel)
			require.Equal(t, 9750, price.QuotaToPreConsume)
			for _, tc := range []struct {
				length float64
				want   int
			}{{272000, 4566}, {272001, 7508}} {
				result, err := billingexpr.ComputeTieredQuota(info.TieredBillingSnapshot, billingexpr.TokenParams{P: 5000, CR: 90000, CC: 5000, C: 10000, Len: tc.length})
				require.NoError(t, err)
				require.Equal(t, tc.want, result.ActualQuotaAfterGroup)
			}
		})
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	other := &relaycommon.RelayInfo{OriginModelName: "gpt-6-sol", UserGroup: "vip", UsingGroup: "discount"}
	ratio := HandleGroupRatio(ctx, other)
	require.Equal(t, 0.05, ratio.GroupRatio)
	require.True(t, ratio.HasSpecialRatio)
	other.UserGroup = "regular"
	require.Equal(t, 0.2, HandleGroupRatio(ctx, other).GroupRatio)
}
