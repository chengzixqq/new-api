package types

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ModelGroupPricing struct {
	Ratio                *float64 `json:"ratio,omitempty"`
	ModelPrice           *float64 `json:"model_price,omitempty"`
	PromptPrice          *float64 `json:"prompt_price,omitempty"`
	CompletionPrice      *float64 `json:"completion_price,omitempty"`
	CachePrice           *float64 `json:"cache_price,omitempty"`
	CreateCachePrice     *float64 `json:"create_cache_price,omitempty"`
	ImagePrice           *float64 `json:"image_price,omitempty"`
	AudioPrice           *float64 `json:"audio_price,omitempty"`
	AudioCompletionPrice *float64 `json:"audio_completion_price,omitempty"`
}

func floatPtr(value float64) *float64 {
	return &value
}

func (p *ModelGroupPricing) UnmarshalJSON(data []byte) error {
	if strings.TrimSpace(string(data)) == "null" {
		*p = ModelGroupPricing{}
		return nil
	}

	var ratio float64
	if err := json.Unmarshal(data, &ratio); err == nil {
		p.Ratio = floatPtr(ratio)
		return nil
	}

	type alias ModelGroupPricing
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = ModelGroupPricing(decoded)
	return nil
}

func (p ModelGroupPricing) MarshalJSON() ([]byte, error) {
	if p.Ratio != nil && !p.HasPriceOverride() {
		return json.Marshal(*p.Ratio)
	}
	type alias ModelGroupPricing
	return json.Marshal(alias(p))
}

func (p ModelGroupPricing) HasRatio() bool {
	return p.Ratio != nil
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

func (p ModelGroupPricing) IsEmpty() bool {
	return !p.HasRatio() && !p.HasPriceOverride()
}

type GroupRatioInfo struct {
	GroupRatio           float64
	GroupSpecialRatio    float64
	HasSpecialRatio      bool
	ModelGroupRatio      float64
	HasModelGroupRatio   bool
	ModelGroupPricing    *ModelGroupPricing
	HasModelGroupPricing bool
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
	GroupPriceOverride   *ModelGroupPricing
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
