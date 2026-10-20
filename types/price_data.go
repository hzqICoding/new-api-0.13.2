package types

import "fmt"

type GroupRatioInfo struct {
	GroupRatio        float64
	GroupSpecialRatio float64
	HasSpecialRatio   bool
	Carpool           *CarpoolBillingSnapshot
	CarpoolPricedAt   int64
}

// Presence, not a zero-valued multiplier, distinguishes newly frozen carpool
// billing from historical requests/tasks. This also lives in task private JSON.
type CarpoolBillingSnapshot struct {
	Group               string  `json:"group"`
	Model               string  `json:"model"`
	Ratio               float64 `json:"ratio"`
	PricedAt            int64   `json:"priced_at"`
	BaseGroupRatio      float64 `json:"base_group_ratio"`
	EffectiveGroupRatio float64 `json:"effective_group_ratio"`
	GroupSpecialRatio   float64 `json:"user_group_ratio"`
	HasSpecialRatio     bool    `json:"has_special_ratio"`
}

func (g GroupRatioInfo) LogGroupRatio() float64 {
	if g.Carpool != nil {
		return g.Carpool.BaseGroupRatio
	}
	return g.GroupRatio
}

func (s *CarpoolBillingSnapshot) AddLogFields(other map[string]interface{}) {
	if s == nil || other == nil {
		return
	}
	other["group_ratio"] = s.BaseGroupRatio
	other["carpool_ratio"] = s.Ratio
	other["carpool_priced_at"] = s.PricedAt
}

func (g GroupRatioInfo) AddCarpoolLogFields(other map[string]interface{}) {
	if other == nil {
		return
	}
	if g.CarpoolPricedAt > 0 {
		other["carpool_priced_at"] = g.CarpoolPricedAt
	}
	g.Carpool.AddLogFields(other)
}

type PriceData struct {
	FreeModel            bool
	ModelPrice           float64
	ModelRatio           float64
	CompletionRatio      float64
	CacheRatio           float64
	CacheCreationRatio   float64
	CacheCreation5mRatio float64
	CacheCreation1hRatio float64
	ImageRatio           float64
	AudioRatio           float64
	AudioCompletionRatio float64
	OtherRatios          map[string]float64
	UsePrice             bool
	Quota                int // 按次计费的最终额度（MJ / Task）
	QuotaToPreConsume    int // 按量计费的预消耗额度
	GroupRatioInfo       GroupRatioInfo
}

func (p *PriceData) AddOtherRatio(key string, ratio float64) {
	if p.OtherRatios == nil {
		p.OtherRatios = make(map[string]float64)
	}
	if ratio <= 0 {
		return
	}
	p.OtherRatios[key] = ratio
}

func (p *PriceData) ToSetting() string {
	return fmt.Sprintf("ModelPrice: %f, ModelRatio: %f, CompletionRatio: %f, CacheRatio: %f, GroupRatio: %f, UsePrice: %t, CacheCreationRatio: %f, CacheCreation5mRatio: %f, CacheCreation1hRatio: %f, QuotaToPreConsume: %d, ImageRatio: %f, AudioRatio: %f, AudioCompletionRatio: %f", p.ModelPrice, p.ModelRatio, p.CompletionRatio, p.CacheRatio, p.GroupRatioInfo.GroupRatio, p.UsePrice, p.CacheCreationRatio, p.CacheCreation5mRatio, p.CacheCreation1hRatio, p.QuotaToPreConsume, p.ImageRatio, p.AudioRatio, p.AudioCompletionRatio)
}
