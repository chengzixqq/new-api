package types

import (
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
)

type ModelGroupPricing struct {
	Ratio                *float64 `json:"ratio,omitempty"`
	BillingMode          *string  `json:"billing_mode,omitempty"`
	BillingExpr          *string  `json:"billing_expr,omitempty"`
	ModelPrice           *float64 `json:"model_price,omitempty"`
	PromptPrice          *float64 `json:"prompt_price,omitempty"`
	CompletionPrice      *float64 `json:"completion_price,omitempty"`
	CachePrice           *float64 `json:"cache_price,omitempty"`
	CreateCachePrice     *float64 `json:"create_cache_price,omitempty"`
	ImagePrice           *float64 `json:"image_price,omitempty"`
	AudioPrice           *float64 `json:"audio_price,omitempty"`
	AudioCompletionPrice *float64 `json:"audio_completion_price,omitempty"`
	MinFee               *float64 `json:"min_fee,omitempty"`
}

// Group-level billing modes. nil BillingMode = inherit the model default.
const (
	GroupBillingModePerToken   = "per-token"
	GroupBillingModePerRequest = "per-request"
	GroupBillingModeTieredExpr = "tiered_expr"

	GroupRatioSourceGroup              = "group_ratio"
	GroupRatioSourceGroupSpecial       = "group_group_ratio"
	GroupRatioSourceUserOverride       = "user_group_ratio_override"
	GroupRatioSourceModelGroupOverride = "model_group_ratio"
)

func floatPtr(value float64) *float64 {
	return &value
}

func (p *ModelGroupPricing) UnmarshalJSON(data []byte) error {
	if strings.TrimSpace(string(data)) == "null" {
		*p = ModelGroupPricing{}
		return nil
	}

	var ratio float64
	if err := common.Unmarshal(data, &ratio); err == nil {
		p.Ratio = floatPtr(ratio)
		return nil
	}

	type alias ModelGroupPricing
	var decoded alias
	if err := common.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = ModelGroupPricing(decoded)
	return nil
}

func (p ModelGroupPricing) MarshalJSON() ([]byte, error) {
	if p.Ratio != nil && !p.HasPriceOverride() && !p.HasBillingMode() && !p.HasMinFee() {
		return common.Marshal(*p.Ratio)
	}
	type alias ModelGroupPricing
	return common.Marshal(alias(p))
}

func (p ModelGroupPricing) HasRatio() bool {
	return p.Ratio != nil
}

func (p ModelGroupPricing) HasBillingMode() bool {
	return p.BillingMode != nil
}

func (p ModelGroupPricing) HasPriceOverride() bool {
	return p.ModelPrice != nil ||
		p.PromptPrice != nil ||
		p.CompletionPrice != nil ||
		p.CachePrice != nil ||
		p.CreateCachePrice != nil ||
		p.ImagePrice != nil ||
		p.AudioPrice != nil ||
		p.AudioCompletionPrice != nil
}

func (p ModelGroupPricing) HasMinFee() bool {
	return p.MinFee != nil
}

func (p ModelGroupPricing) IsEmpty() bool {
	return !p.HasRatio() && !p.HasPriceOverride() && !p.HasBillingMode() && !p.HasMinFee()
}

type GroupRatioInfo struct {
	GroupRatio                float64
	GroupRatioSource          string
	GroupSpecialRatio         float64
	HasSpecialRatio           bool
	UserGroupRatioOverride    float64
	HasUserGroupRatioOverride bool
	ModelGroupRatio           float64
	HasModelGroupRatio        bool
	ModelGroupPricing         *ModelGroupPricing
	HasModelGroupPricing      bool
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
	otherRatios          map[string]float64
	UsePrice             bool
	Quota                int // 按次计费的最终额度（MJ / Task）
	QuotaToPreConsume    int // 按量计费的预消耗额度
	GroupRatioInfo       GroupRatioInfo
	GroupPriceOverride   *ModelGroupPricing
	MinQuota             int // 已折算成内部 quota 的最低额度下限；0 = 无最低费用
}

// PersonalGroupPriceMultiplier returns the final per-user multiplier that must
// also be applied to absolute model-group price overrides. Ratio-based pricing
// already consumes GroupRatioInfo.GroupRatio directly.
func (p PriceData) PersonalGroupPriceMultiplier() float64 {
	if p.GroupRatioInfo.HasUserGroupRatioOverride && isValidOtherRatio(p.GroupRatioInfo.UserGroupRatioOverride) {
		return p.GroupRatioInfo.UserGroupRatioOverride
	}
	return 1
}

func (p *PriceData) AddOtherRatio(key string, ratio float64) {
	if !isValidOtherRatio(ratio) {
		return
	}
	if p.otherRatios == nil {
		p.otherRatios = make(map[string]float64)
	}
	p.otherRatios[key] = ratio
}

func (p *PriceData) ReplaceOtherRatios(ratios map[string]float64) bool {
	p.otherRatios = nil
	for key, ratio := range ratios {
		p.AddOtherRatio(key, ratio)
	}
	return len(p.otherRatios) > 0
}

func (p *PriceData) HasOtherRatio(key string) bool {
	ratio, ok := p.otherRatios[key]
	return ok && isValidOtherRatio(ratio)
}

func (p *PriceData) OtherRatios() map[string]float64 {
	if len(p.otherRatios) == 0 {
		return nil
	}
	ratios := make(map[string]float64, len(p.otherRatios))
	for key, ratio := range p.otherRatios {
		if isValidOtherRatio(ratio) {
			ratios[key] = ratio
		}
	}
	if len(ratios) == 0 {
		return nil
	}
	return ratios
}

func (p *PriceData) OtherRatioMultiplier() float64 {
	multiplier := 1.0
	for _, ratio := range p.otherRatios {
		if isValidOtherRatio(ratio) && ratio != 1.0 {
			multiplier *= ratio
		}
	}
	return multiplier
}

func (p *PriceData) ApplyOtherRatiosToFloat(value float64) float64 {
	return value * p.OtherRatioMultiplier()
}

func (p *PriceData) ApplyOtherRatiosToDecimal(value decimal.Decimal) decimal.Decimal {
	for _, ratio := range p.otherRatios {
		if isValidOtherRatio(ratio) && ratio != 1.0 {
			value = value.Mul(decimal.NewFromFloat(ratio))
		}
	}
	return value
}

func (p *PriceData) RemoveOtherRatiosFromFloat(value float64) float64 {
	for _, ratio := range p.otherRatios {
		if isValidOtherRatio(ratio) && ratio != 1.0 {
			value /= ratio
		}
	}
	return value
}

func isValidOtherRatio(ratio float64) bool {
	// NaN/Inf would poison every downstream quota multiplication
	// (int(NaN * quota) wraps to a negative charge).
	return ratio > 0 && !math.IsInf(ratio, 1)
}

func (p *PriceData) ToSetting() string {
	return fmt.Sprintf("ModelPrice: %f, ModelRatio: %f, CompletionRatio: %f, CacheRatio: %f, GroupRatio: %f, UsePrice: %t, CacheCreationRatio: %f, CacheCreation5mRatio: %f, CacheCreation1hRatio: %f, QuotaToPreConsume: %d, ImageRatio: %f, AudioRatio: %f, AudioCompletionRatio: %f", p.ModelPrice, p.ModelRatio, p.CompletionRatio, p.CacheRatio, p.GroupRatioInfo.GroupRatio, p.UsePrice, p.CacheCreationRatio, p.CacheCreation5mRatio, p.CacheCreation1hRatio, p.QuotaToPreConsume, p.ImageRatio, p.AudioRatio, p.AudioCompletionRatio)
}
