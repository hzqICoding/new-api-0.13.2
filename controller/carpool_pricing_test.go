package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCarpoolBillingPolicyDoesNotChangePricingResponse(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.Channel{Id: 8601, Name: "carpool-pricing", Key: "unused", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "gpt-4", ChannelId: 8601, Enabled: true}).Error)
	model.InvalidatePricingCache()
	common.OptionMapRWMutex.Lock()
	saved := common.OptionMap[billing_setting.CarpoolBillingPolicyKey]
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	delete(common.OptionMap, billing_setting.CarpoolBillingPolicyKey)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap[billing_setting.CarpoolBillingPolicyKey] = saved
		common.OptionMapRWMutex.Unlock()
		model.InvalidatePricingCache()
	})
	r := gin.New()
	r.GET("/api/pricing", GetPricing)
	call := func() string {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/pricing", nil))
		require.Equal(t, 200, w.Code)
		return w.Body.String()
	}
	before := call()
	require.Contains(t, before, `"model_name":"gpt-4"`)
	raw, err := common.Marshal(map[string]any{"version": 1, "issued_at": time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z"), "groups": []string{"default"}, "model_multipliers": map[string]float64{"gpt-4": 2}})
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	common.OptionMap[billing_setting.CarpoolBillingPolicyKey] = string(raw)
	common.OptionMapRWMutex.Unlock()
	ratio, matched := billing_setting.CarpoolMultiplier("default", "gpt-4")
	require.True(t, matched)
	require.Equal(t, 2.0, ratio)
	require.Equal(t, before, call(), "entire /api/pricing body must remain byte-identical")
}
