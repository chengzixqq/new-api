package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type textQuotaSummary struct {
	PromptTokens             int
	CompletionTokens         int
	TotalTokens              int
	ChargeableTokens         int
	CacheTokens              int
	CacheCreationTokens      int
	CacheCreationTokens5m    int
	CacheCreationTokens1h    int
	ImageTokens              int
	AudioTokens              int
	ModelName                string
	TokenName                string
	UseTimeSeconds           int64
	CompletionRatio          float64
	CacheRatio               float64
	ImageRatio               float64
	ModelRatio               float64
	GroupRatio               float64
	ModelPrice               float64
	CacheCreationRatio       float64
	CacheCreationRatio5m     float64
	CacheCreationRatio1h     float64
	Quota                    int
	IsClaudeUsageSemantic    bool
	UsageSemantic            string
	WebSearchPrice           float64
	WebSearchCallCount       int
	ClaudeWebSearchPrice     float64
	ClaudeWebSearchCallCount int
	FileSearchPrice          float64
	FileSearchCallCount      int
	AudioInputPrice          float64
	ImageGenerationCallPrice float64
	ToolCallSurchargeQuota   decimal.Decimal
}

func priceOverrideToQuota(price *float64, tokens decimal.Decimal) (decimal.Decimal, bool) {
	if price == nil {
		return decimal.Zero, false
	}
	return decimal.NewFromFloat(*price).
		Div(decimal.NewFromInt(1_000_000)).
		Mul(tokens).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit)), true
}

// quotaDecimalToIntChecked converts a billing decimal to an int quota using our
// conservative rounding policy, then routes the result through the shared int32
// saturation guard so an oversized product clamps instead of wrapping into a
// credit. Positive charges are rounded UP (ceil) so a fractional charge is never
// truncated into an undercharge; negative values (refunds/corrections) are
// rounded toward zero via ceil so we never over-credit the user. The returned
// *common.QuotaClamp is non-nil only when saturation kicked in, so callers can
// record the event for admin auditing via noteQuotaClamp.
func quotaDecimalToIntChecked(quota decimal.Decimal) (int, *common.QuotaClamp) {
	if quota.IsZero() {
		return 0, nil
	}
	var rounded decimal.Decimal
	if quota.IsPositive() {
		rounded = quota.Ceil()
	} else {
		rounded = quota.Ceil()
	}
	// rounded is already integer-valued; QuotaFromDecimalChecked's internal
	// Round(0) is a no-op here and only the int32 saturation applies.
	return common.QuotaFromDecimalChecked(rounded)
}

func quotaDecimalToInt(quota decimal.Decimal) int {
	q, _ := quotaDecimalToIntChecked(quota)
	return q
}

func applyTextGroupPriceOverride(relayInfo *relaycommon.RelayInfo, summary *textQuotaSummary, components map[string]decimal.Decimal) (decimal.Decimal, bool) {
	if relayInfo == nil || summary == nil || relayInfo.PriceData.GroupPriceOverride == nil {
		return decimal.Zero, false
	}
	override := relayInfo.PriceData.GroupPriceOverride
	quota := decimal.Zero
	applied := false

	if value, ok := priceOverrideToQuota(override.PromptPrice, components["prompt"]); ok {
		quota = quota.Add(value)
		applied = true
	} else {
		quota = quota.Add(components["prompt"].Mul(decimal.NewFromFloat(summary.ModelRatio)).Mul(decimal.NewFromFloat(summary.GroupRatio)))
	}
	if value, ok := priceOverrideToQuota(override.CompletionPrice, components["completion"]); ok {
		quota = quota.Add(value)
		applied = true
	} else {
		quota = quota.Add(components["completion"].Mul(decimal.NewFromFloat(summary.ModelRatio)).Mul(decimal.NewFromFloat(summary.CompletionRatio)).Mul(decimal.NewFromFloat(summary.GroupRatio)))
	}
	if value, ok := priceOverrideToQuota(override.CachePrice, components["cache"]); ok {
		quota = quota.Add(value)
		applied = true
	} else {
		quota = quota.Add(components["cache"].Mul(decimal.NewFromFloat(summary.ModelRatio)).Mul(decimal.NewFromFloat(summary.CacheRatio)).Mul(decimal.NewFromFloat(summary.GroupRatio)))
	}
	if value, ok := priceOverrideToQuota(override.CreateCachePrice, components["cache_create"]); ok {
		quota = quota.Add(value)
		applied = true
	} else {
		quota = quota.Add(components["cache_create"].Mul(decimal.NewFromFloat(summary.ModelRatio)).Mul(decimal.NewFromFloat(summary.CacheCreationRatio)).Mul(decimal.NewFromFloat(summary.GroupRatio)))
	}
	if value, ok := priceOverrideToQuota(override.CreateCachePrice, components["cache_create_5m"]); ok {
		quota = quota.Add(value)
		applied = true
	} else {
		quota = quota.Add(components["cache_create_5m"].Mul(decimal.NewFromFloat(summary.ModelRatio)).Mul(decimal.NewFromFloat(summary.CacheCreationRatio5m)).Mul(decimal.NewFromFloat(summary.GroupRatio)))
	}
	if value, ok := priceOverrideToQuota(override.CreateCachePrice, components["cache_create_1h"]); ok {
		quota = quota.Add(value)
		applied = true
	} else {
		quota = quota.Add(components["cache_create_1h"].Mul(decimal.NewFromFloat(summary.ModelRatio)).Mul(decimal.NewFromFloat(summary.CacheCreationRatio1h)).Mul(decimal.NewFromFloat(summary.GroupRatio)))
	}
	if value, ok := priceOverrideToQuota(override.ImagePrice, components["image"]); ok {
		quota = quota.Add(value)
		applied = true
	} else {
		quota = quota.Add(components["image"].Mul(decimal.NewFromFloat(summary.ModelRatio)).Mul(decimal.NewFromFloat(summary.ImageRatio)).Mul(decimal.NewFromFloat(summary.GroupRatio)))
	}

	return quota, applied
}

func cacheWriteTokensTotal(summary textQuotaSummary) int {
	if summary.CacheCreationTokens5m > 0 || summary.CacheCreationTokens1h > 0 {
		splitCacheWriteTokens := saturatingPositiveTokenSum(summary.CacheCreationTokens5m, summary.CacheCreationTokens1h)
		return maxPositiveTokenCount(summary.CacheCreationTokens, splitCacheWriteTokens)
	}
	return positiveTokenCount(summary.CacheCreationTokens)
}

func isLegacyClaudeDerivedOpenAIUsage(relayInfo *relaycommon.RelayInfo, usage *dto.Usage) bool {
	if relayInfo == nil || usage == nil {
		return false
	}
	if relayInfo.GetFinalRequestRelayFormat() == types.RelayFormatClaude {
		return false
	}
	if usage.UsageSource != "" || usage.UsageSemantic != "" {
		return false
	}
	return usage.ClaudeCacheCreation5mTokens > 0 || usage.ClaudeCacheCreation1hTokens > 0
}

func calculateTextToolCallSurcharge(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, summary *textQuotaSummary) decimal.Decimal {
	dGroupRatio := decimal.NewFromFloat(summary.GroupRatio)
	dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)

	var surcharge decimal.Decimal

	if relayInfo.ResponsesUsageInfo != nil {
		if webSearchTool, exists := relayInfo.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview]; exists && webSearchTool.CallCount > 0 {
			summary.WebSearchCallCount = webSearchTool.CallCount
			summary.WebSearchPrice = operation_setting.GetToolPriceForModel("web_search_preview", summary.ModelName)
			surcharge = surcharge.Add(decimal.NewFromFloat(summary.WebSearchPrice).
				Mul(decimal.NewFromInt(int64(webSearchTool.CallCount))).
				Div(decimal.NewFromInt(1000)).
				Mul(dGroupRatio).
				Mul(dQuotaPerUnit))
		}
	} else if strings.HasSuffix(summary.ModelName, "search-preview") {
		summary.WebSearchCallCount = 1
		summary.WebSearchPrice = operation_setting.GetToolPriceForModel("web_search_preview", summary.ModelName)
		surcharge = surcharge.Add(decimal.NewFromFloat(summary.WebSearchPrice).
			Div(decimal.NewFromInt(1000)).
			Mul(dGroupRatio).
			Mul(dQuotaPerUnit))
	}

	summary.ClaudeWebSearchCallCount = ctx.GetInt("claude_web_search_requests")
	if summary.ClaudeWebSearchCallCount > 0 {
		summary.ClaudeWebSearchPrice = operation_setting.GetToolPrice("web_search")
		surcharge = surcharge.Add(decimal.NewFromFloat(summary.ClaudeWebSearchPrice).
			Div(decimal.NewFromInt(1000)).
			Mul(dGroupRatio).
			Mul(dQuotaPerUnit).
			Mul(decimal.NewFromInt(int64(summary.ClaudeWebSearchCallCount))))
	}

	if relayInfo.ResponsesUsageInfo != nil {
		if fileSearchTool, exists := relayInfo.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolFileSearch]; exists && fileSearchTool.CallCount > 0 {
			summary.FileSearchCallCount = fileSearchTool.CallCount
			summary.FileSearchPrice = operation_setting.GetToolPrice("file_search")
			surcharge = surcharge.Add(decimal.NewFromFloat(summary.FileSearchPrice).
				Mul(decimal.NewFromInt(int64(fileSearchTool.CallCount))).
				Div(decimal.NewFromInt(1000)).
				Mul(dGroupRatio).
				Mul(dQuotaPerUnit))
		}
	}

	if ctx.GetBool("image_generation_call") {
		summary.ImageGenerationCallPrice = operation_setting.GetGPTImage1PriceOnceCall(ctx.GetString("image_generation_call_quality"), ctx.GetString("image_generation_call_size"))
		surcharge = surcharge.Add(decimal.NewFromFloat(summary.ImageGenerationCallPrice).
			Mul(dGroupRatio).
			Mul(dQuotaPerUnit))
	}

	return surcharge
}

// noteQuotaClamp records the first quota saturation event onto relayInfo so it
// can later be attached to the consume/task log for admin auditing. First
// non-nil clamp wins (a single request may hit multiple conversions).
func noteQuotaClamp(relayInfo *relaycommon.RelayInfo, clamp *common.QuotaClamp) {
	if clamp == nil || relayInfo == nil {
		return
	}
	if relayInfo.QuotaClamp == nil {
		relayInfo.QuotaClamp = clamp
	}
}

func composeTieredTextQuota(relayInfo *relaycommon.RelayInfo, summary textQuotaSummary, tieredQuota int, tieredResult *billingexpr.TieredResult) int {
	if summary.ToolCallSurchargeQuota.IsZero() {
		return tieredQuota
	}

	if tieredResult != nil {
		if snap := relayInfo.TieredBillingSnapshot; snap != nil {
			quota, clamp := common.QuotaFromDecimalChecked(decimal.NewFromFloat(tieredResult.ActualQuotaBeforeGroup).
				Mul(decimal.NewFromFloat(snap.GroupRatio)).
				Add(summary.ToolCallSurchargeQuota))
			noteQuotaClamp(relayInfo, clamp)
			return quota
		}
	}

	// Saturate the final sum, not just the surcharge: tieredQuota can be near
	// MaxQuota and adding the surcharge could push the total past the int32
	// quota policy bound (persisted quota columns are 32-bit).
	total, clamp := common.QuotaFromDecimalChecked(
		decimal.NewFromInt(int64(tieredQuota)).Add(summary.ToolCallSurchargeQuota),
	)
	noteQuotaClamp(relayInfo, clamp)
	return total
}

func positiveTokenCount(value int) int {
	if value > 0 {
		return value
	}
	return 0
}

func maxPositiveTokenCount(values ...int) int {
	maxValue := 0
	for _, value := range values {
		if value > maxValue {
			maxValue = value
		}
	}
	return maxValue
}

func saturatingPositiveTokenSum(values ...int) int {
	maxInt := int(^uint(0) >> 1)
	total := 0
	for _, value := range values {
		value = positiveTokenCount(value)
		if total > maxInt-value {
			return maxInt
		}
		total += value
	}
	return total
}

func chargeableTextTokenCount(summary textQuotaSummary) int {
	splitCacheCreationTokens := saturatingPositiveTokenSum(summary.CacheCreationTokens5m, summary.CacheCreationTokens1h)
	cacheCreationTokens := maxPositiveTokenCount(summary.CacheCreationTokens, splitCacheCreationTokens)

	return saturatingPositiveTokenSum(
		summary.PromptTokens,
		summary.CompletionTokens,
		summary.CacheTokens,
		cacheCreationTokens,
		summary.ImageTokens,
		summary.AudioTokens,
	)
}

// calculateTextQuotaSummary expects a usage already remapped by
// effectiveBillingUsage; PostTextConsumeQuota performs that remap once and shares
// the result with tiered billing, affinity observation and logging.
func calculateTextQuotaSummary(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage) textQuotaSummary {
	summary := textQuotaSummary{
		ModelName:            relayInfo.OriginModelName,
		TokenName:            ctx.GetString("token_name"),
		UseTimeSeconds:       time.Now().Unix() - relayInfo.StartTime.Unix(),
		CompletionRatio:      relayInfo.PriceData.CompletionRatio,
		CacheRatio:           relayInfo.PriceData.CacheRatio,
		ImageRatio:           relayInfo.PriceData.ImageRatio,
		ModelRatio:           relayInfo.PriceData.ModelRatio,
		GroupRatio:           relayInfo.PriceData.GroupRatioInfo.GroupRatio,
		ModelPrice:           relayInfo.PriceData.ModelPrice,
		CacheCreationRatio:   relayInfo.PriceData.CacheCreationRatio,
		CacheCreationRatio5m: relayInfo.PriceData.CacheCreation5mRatio,
		CacheCreationRatio1h: relayInfo.PriceData.CacheCreation1hRatio,
		UsageSemantic:        usageSemanticFromUsage(relayInfo, usage),
	}
	summary.IsClaudeUsageSemantic = summary.UsageSemantic == "anthropic"

	if usage == nil {
		usage = &dto.Usage{
			PromptTokens:     relayInfo.GetEstimatePromptTokens(),
			CompletionTokens: 0,
			TotalTokens:      relayInfo.GetEstimatePromptTokens(),
		}
	}

	summary.PromptTokens = positiveTokenCount(usage.PromptTokens)
	summary.CompletionTokens = positiveTokenCount(usage.CompletionTokens)
	summary.TotalTokens = saturatingPositiveTokenSum(summary.PromptTokens, summary.CompletionTokens)
	summary.CacheTokens = positiveTokenCount(usage.PromptTokensDetails.CachedTokens)
	summary.CacheCreationTokens = usage.PromptTokensDetails.CacheCreationTokensTotal()
	summary.CacheCreationTokens5m = positiveTokenCount(usage.ClaudeCacheCreation5mTokens)
	summary.CacheCreationTokens1h = positiveTokenCount(usage.ClaudeCacheCreation1hTokens)
	summary.ImageTokens = positiveTokenCount(usage.PromptTokensDetails.ImageTokens)
	summary.AudioTokens = positiveTokenCount(usage.PromptTokensDetails.AudioTokens)
	legacyClaudeDerived := isLegacyClaudeDerivedOpenAIUsage(relayInfo, usage)
	isOpenRouterClaudeBilling := relayInfo.ChannelMeta != nil &&
		relayInfo.ChannelType == constant.ChannelTypeOpenRouter &&
		summary.IsClaudeUsageSemantic

	if isOpenRouterClaudeBilling {
		summary.PromptTokens -= summary.CacheTokens
		isUsingCustomSettings := relayInfo.PriceData.UsePrice || hasCustomModelRatio(summary.ModelName, relayInfo.PriceData.ModelRatio)
		if summary.CacheCreationTokens == 0 && relayInfo.PriceData.CacheCreationRatio != 1 && usage.Cost != 0 && !isUsingCustomSettings {
			maybeCacheCreationTokens := CalcOpenRouterCacheCreateTokens(*usage, relayInfo.PriceData)
			if maybeCacheCreationTokens >= 0 && summary.PromptTokens >= maybeCacheCreationTokens {
				summary.CacheCreationTokens = maybeCacheCreationTokens
			}
		}
		summary.PromptTokens -= summary.CacheCreationTokens
		summary.PromptTokens = positiveTokenCount(summary.PromptTokens)
	}
	// ChargeableTokens is the zero-guard/min-fee sentinel. It deliberately
	// includes billable cache/image/audio input tokens while keeping TotalTokens
	// as the plain prompt+completion total. For Claude cache creation, aggregate
	// and 5m/1h split fields describe the same input side, so use the larger of
	// aggregate vs split sum rather than adding both.
	summary.ChargeableTokens = chargeableTextTokenCount(summary)

	dPromptTokens := decimal.NewFromInt(int64(summary.PromptTokens))
	dCacheTokens := decimal.NewFromInt(int64(summary.CacheTokens))
	dImageTokens := decimal.NewFromInt(int64(summary.ImageTokens))
	dAudioTokens := decimal.NewFromInt(int64(summary.AudioTokens))
	dCompletionTokens := decimal.NewFromInt(int64(summary.CompletionTokens))
	dCachedCreationTokens := decimal.NewFromInt(int64(summary.CacheCreationTokens))
	dCompletionRatio := decimal.NewFromFloat(summary.CompletionRatio)
	dCacheRatio := decimal.NewFromFloat(summary.CacheRatio)
	dImageRatio := decimal.NewFromFloat(summary.ImageRatio)
	dModelRatio := decimal.NewFromFloat(summary.ModelRatio)
	dGroupRatio := decimal.NewFromFloat(summary.GroupRatio)
	dCacheCreationRatio := decimal.NewFromFloat(summary.CacheCreationRatio)
	dCacheCreationRatio5m := decimal.NewFromFloat(summary.CacheCreationRatio5m)
	dCacheCreationRatio1h := decimal.NewFromFloat(summary.CacheCreationRatio1h)
	dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)

	ratio := dModelRatio.Mul(dGroupRatio)
	summary.ToolCallSurchargeQuota = calculateTextToolCallSurcharge(ctx, relayInfo, &summary)

	var audioInputQuota decimal.Decimal
	if !relayInfo.PriceData.UsePrice {
		baseTokens := dPromptTokens

		var cachedTokensWithRatio decimal.Decimal
		if !dCacheTokens.IsZero() {
			if !summary.IsClaudeUsageSemantic && !legacyClaudeDerived {
				baseTokens = baseTokens.Sub(dCacheTokens)
			}
			cachedTokensWithRatio = dCacheTokens.Mul(dCacheRatio)
		}

		var cachedCreationTokensWithRatio decimal.Decimal
		hasSplitCacheCreationTokens := summary.CacheCreationTokens5m > 0 || summary.CacheCreationTokens1h > 0
		if !dCachedCreationTokens.IsZero() || hasSplitCacheCreationTokens {
			if !summary.IsClaudeUsageSemantic && !legacyClaudeDerived {
				baseTokens = baseTokens.Sub(dCachedCreationTokens)
				cachedCreationTokensWithRatio = dCachedCreationTokens.Mul(dCacheCreationRatio)
			} else {
				remaining := summary.CacheCreationTokens - summary.CacheCreationTokens5m - summary.CacheCreationTokens1h
				if remaining < 0 {
					remaining = 0
				}
				cachedCreationTokensWithRatio = decimal.NewFromInt(int64(remaining)).Mul(dCacheCreationRatio)
				cachedCreationTokensWithRatio = cachedCreationTokensWithRatio.Add(decimal.NewFromInt(int64(summary.CacheCreationTokens5m)).Mul(dCacheCreationRatio5m))
				cachedCreationTokensWithRatio = cachedCreationTokensWithRatio.Add(decimal.NewFromInt(int64(summary.CacheCreationTokens1h)).Mul(dCacheCreationRatio1h))
			}
		}

		var imageTokensWithRatio decimal.Decimal
		if !dImageTokens.IsZero() {
			baseTokens = baseTokens.Sub(dImageTokens)
			imageTokensWithRatio = dImageTokens.Mul(dImageRatio)
		}

		cacheCreateOverrideTokens := dCachedCreationTokens
		if summary.IsClaudeUsageSemantic || legacyClaudeDerived {
			remaining := summary.CacheCreationTokens - summary.CacheCreationTokens5m - summary.CacheCreationTokens1h
			if remaining < 0 {
				remaining = 0
			}
			cacheCreateOverrideTokens = decimal.NewFromInt(int64(remaining))
		}

		if !dAudioTokens.IsZero() {
			if override := relayInfo.PriceData.GroupPriceOverride; override != nil && override.AudioPrice != nil {
				summary.AudioInputPrice = *override.AudioPrice
				baseTokens = baseTokens.Sub(dAudioTokens)
				if value, ok := priceOverrideToQuota(override.AudioPrice, dAudioTokens); ok {
					audioInputQuota = value
				}
			} else {
				summary.AudioInputPrice = operation_setting.GetGeminiInputAudioPricePerMillionTokens(summary.ModelName)
				if summary.AudioInputPrice > 0 {
					baseTokens = baseTokens.Sub(dAudioTokens)
					audioInputQuota = decimal.NewFromFloat(summary.AudioInputPrice).
						Div(decimal.NewFromInt(1000000)).Mul(dAudioTokens).Mul(dGroupRatio).Mul(dQuotaPerUnit)
				}
			}
		}

		// Cached/read/write prefix counters may overlap. Never let that overlap
		// turn the uncached remainder into a negative credit.
		if baseTokens.IsNegative() {
			baseTokens = decimal.Zero
		}

		quotaCalculateDecimal := decimal.Zero
		if overrideQuota, ok := applyTextGroupPriceOverride(relayInfo, &summary, map[string]decimal.Decimal{
			"prompt":          baseTokens,
			"completion":      dCompletionTokens,
			"cache":           dCacheTokens,
			"cache_create":    cacheCreateOverrideTokens,
			"cache_create_5m": decimal.NewFromInt(int64(summary.CacheCreationTokens5m)),
			"cache_create_1h": decimal.NewFromInt(int64(summary.CacheCreationTokens1h)),
			"image":           dImageTokens,
		}); ok {
			quotaCalculateDecimal = overrideQuota
		} else {
			promptQuota := baseTokens.Add(cachedTokensWithRatio).Add(imageTokensWithRatio).Add(cachedCreationTokensWithRatio)
			completionQuota := dCompletionTokens.Mul(dCompletionRatio)
			quotaCalculateDecimal = promptQuota.Add(completionQuota).Mul(ratio)
		}
		quotaCalculateDecimal = quotaCalculateDecimal.Add(summary.ToolCallSurchargeQuota)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(audioInputQuota)
		quotaCalculateDecimal = relayInfo.PriceData.ApplyOtherRatiosToDecimal(quotaCalculateDecimal)

		if (!ratio.IsZero() || (relayInfo.PriceData.GroupPriceOverride != nil && relayInfo.PriceData.GroupPriceOverride.HasPriceOverride())) && quotaCalculateDecimal.LessThanOrEqual(decimal.Zero) {
			quotaCalculateDecimal = decimal.NewFromInt(1)
		}
		quota, clamp := quotaDecimalToIntChecked(quotaCalculateDecimal)
		summary.Quota = quota
		noteQuotaClamp(relayInfo, clamp)
	} else {
		// 结算兜底：按次计费但 ModelPrice 为 -1 哨兵（未配置按次价）时当 0（免费），
		// 避免 -1 直接乘 QuotaPerUnit 产生负 quota（资损）。源头已在 ModelPriceHelper 归零，此处防御。
		if summary.ModelPrice < 0 {
			summary.ModelPrice = 0
		}
		quotaCalculateDecimal := decimal.NewFromFloat(summary.ModelPrice).Mul(dQuotaPerUnit).Mul(dGroupRatio)
		if override := relayInfo.PriceData.GroupPriceOverride; override != nil && override.ModelPrice != nil {
			quotaCalculateDecimal = decimal.NewFromFloat(*override.ModelPrice).Mul(dQuotaPerUnit)
			summary.ModelPrice = *override.ModelPrice
		}
		quotaCalculateDecimal = quotaCalculateDecimal.Add(summary.ToolCallSurchargeQuota)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(audioInputQuota)
		quotaCalculateDecimal = relayInfo.PriceData.ApplyOtherRatiosToDecimal(quotaCalculateDecimal)
		quota, clamp := quotaDecimalToIntChecked(quotaCalculateDecimal)
		summary.Quota = quota
		noteQuotaClamp(relayInfo, clamp)
	}

	if summary.ChargeableTokens == 0 {
		summary.Quota = 0
	} else if (!ratio.IsZero() || (relayInfo.PriceData.GroupPriceOverride != nil && relayInfo.PriceData.GroupPriceOverride.HasPriceOverride())) && summary.Quota == 0 {
		summary.Quota = 1
	}

	return summary
}

// applyMinFeeQuota 对按量计费请求兜底最低费用：仅 !UsePrice、非阶梯、有实际计费(chargeableTokens>0)
// 且算出的 quota 低于 MinQuota 时，抬升到 MinQuota。返回兜底后的 quota 及是否触发。
func applyMinFeeQuota(quota int, priceData types.PriceData, chargeableTokens int, tieredApplied bool) (int, bool) {
	if chargeableTokens <= 0 || priceData.UsePrice || tieredApplied {
		return quota, false
	}
	if priceData.MinQuota > 0 && quota < priceData.MinQuota {
		return priceData.MinQuota, true
	}
	return quota, false
}

func usageSemanticFromUsage(relayInfo *relaycommon.RelayInfo, usage *dto.Usage) string {
	if usage != nil && usage.UsageSemantic != "" {
		return usage.UsageSemantic
	}
	if relayInfo != nil && relayInfo.GetFinalRequestRelayFormat() == types.RelayFormatClaude {
		return "anthropic"
	}
	return "openai"
}

func PostTextConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, extraContent []string) {
	originUsage := usage
	billingUsage := effectiveBillingUsage(usage)
	if usage == nil {
		extraContent = append(extraContent, "上游无计费信息")
	}
	if originUsage != nil {
		ObserveChannelAffinityUsageCacheByRelayFormat(ctx, billingUsage, relayInfo.GetFinalRequestRelayFormat())
	}

	adminRejectReason := common.GetContextKeyString(ctx, constant.ContextKeyAdminRejectReason)
	summary := calculateTextQuotaSummary(ctx, relayInfo, billingUsage)

	var tieredResult *billingexpr.TieredResult
	tieredBillingApplied := false
	if originUsage != nil {
		var tieredUsedVars map[string]bool
		if snap := relayInfo.TieredBillingSnapshot; snap != nil {
			tieredUsedVars = billingexpr.UsedVars(snap.ExprString)
		}
		tieredOk, tieredQuota, tieredRes := TryTieredSettle(relayInfo, BuildTieredTokenParams(billingUsage, summary.IsClaudeUsageSemantic, tieredUsedVars))
		if tieredOk {
			tieredBillingApplied = true
			tieredResult = tieredRes
			summary.Quota = composeTieredTextQuota(relayInfo, summary, tieredQuota, tieredRes)
		}
	}

	summary.Quota, _ = applyMinFeeQuota(summary.Quota, relayInfo.PriceData, summary.ChargeableTokens, tieredBillingApplied)

	if summary.WebSearchCallCount > 0 {
		extraContent = append(extraContent, fmt.Sprintf("Web Search 调用 %d 次，调用花费 %s", summary.WebSearchCallCount, decimal.NewFromFloat(summary.WebSearchPrice).Mul(decimal.NewFromInt(int64(summary.WebSearchCallCount))).Div(decimal.NewFromInt(1000)).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).String()))
	}
	if summary.ClaudeWebSearchCallCount > 0 {
		extraContent = append(extraContent, fmt.Sprintf("Claude Web Search 调用 %d 次，调用花费 %s", summary.ClaudeWebSearchCallCount, decimal.NewFromFloat(summary.ClaudeWebSearchPrice).Div(decimal.NewFromInt(1000)).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Mul(decimal.NewFromInt(int64(summary.ClaudeWebSearchCallCount))).String()))
	}
	if summary.FileSearchCallCount > 0 {
		extraContent = append(extraContent, fmt.Sprintf("File Search 调用 %d 次，调用花费 %s", summary.FileSearchCallCount, decimal.NewFromFloat(summary.FileSearchPrice).Mul(decimal.NewFromInt(int64(summary.FileSearchCallCount))).Div(decimal.NewFromInt(1000)).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).String()))
	}
	if summary.AudioInputPrice > 0 && summary.AudioTokens > 0 {
		extraContent = append(extraContent, fmt.Sprintf("Audio Input 花费 %s", decimal.NewFromFloat(summary.AudioInputPrice).Div(decimal.NewFromInt(1000000)).Mul(decimal.NewFromInt(int64(summary.AudioTokens))).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).String()))
	}
	if summary.ImageGenerationCallPrice > 0 {
		extraContent = append(extraContent, fmt.Sprintf("Image Generation Call 花费 %s", decimal.NewFromFloat(summary.ImageGenerationCallPrice).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).String()))
	}

	if summary.ChargeableTokens == 0 {
		extraContent = append(extraContent, "上游没有返回计费信息，无法扣费（可能是上游超时）")
		logger.LogError(ctx, fmt.Sprintf("chargeable tokens is 0, cannot consume quota, userId %d, channelId %d, tokenId %d, model %s， pre-consumed quota %d", relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, summary.ModelName, relayInfo.FinalPreConsumedQuota))
	} else {
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, summary.Quota)
		model.UpdateChannelUsedQuota(relayInfo.ChannelId, summary.Quota)
	}

	if err := SettleBilling(ctx, relayInfo, summary.Quota); err != nil {
		logger.LogError(ctx, "error settling billing: "+err.Error())
	}

	logModel := summary.ModelName
	if strings.HasPrefix(logModel, "gpt-4-gizmo") {
		logModel = "gpt-4-gizmo-*"
		extraContent = append(extraContent, fmt.Sprintf("模型 %s", summary.ModelName))
	}
	if strings.HasPrefix(logModel, "gpt-4o-gizmo") {
		logModel = "gpt-4o-gizmo-*"
		extraContent = append(extraContent, fmt.Sprintf("模型 %s", summary.ModelName))
	}

	logContent := strings.Join(extraContent, ", ")
	var other map[string]interface{}
	if summary.IsClaudeUsageSemantic {
		other = GenerateClaudeOtherInfo(ctx, relayInfo,
			summary.ModelRatio, summary.GroupRatio, summary.CompletionRatio,
			summary.CacheTokens, summary.CacheRatio,
			summary.CacheCreationTokens, summary.CacheCreationRatio,
			summary.CacheCreationTokens5m, summary.CacheCreationRatio5m,
			summary.CacheCreationTokens1h, summary.CacheCreationRatio1h,
			summary.ModelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
		other["usage_semantic"] = "anthropic"
	} else {
		other = GenerateTextOtherInfo(ctx, relayInfo, summary.ModelRatio, summary.GroupRatio, summary.CompletionRatio, summary.CacheTokens, summary.CacheRatio, summary.ModelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	}
	appendUsageBillingPathForLog(other, common.GetContextKeyBool(ctx, constant.ContextKeyLocalCountTokens), originUsage)
	if adminRejectReason != "" {
		other["reject_reason"] = adminRejectReason
	}
	if summary.ImageTokens != 0 {
		other["image"] = true
		other["image_ratio"] = summary.ImageRatio
		other["image_output"] = summary.ImageTokens
	}
	if summary.WebSearchCallCount > 0 {
		other["web_search"] = true
		other["web_search_call_count"] = summary.WebSearchCallCount
		other["web_search_price"] = summary.WebSearchPrice
	} else if summary.ClaudeWebSearchCallCount > 0 {
		other["web_search"] = true
		other["web_search_call_count"] = summary.ClaudeWebSearchCallCount
		other["web_search_price"] = summary.ClaudeWebSearchPrice
	}
	if summary.FileSearchCallCount > 0 {
		other["file_search"] = true
		other["file_search_call_count"] = summary.FileSearchCallCount
		other["file_search_price"] = summary.FileSearchPrice
	}
	if summary.AudioInputPrice > 0 && summary.AudioTokens > 0 {
		other["audio_input_seperate_price"] = true
		other["audio_input_token_count"] = summary.AudioTokens
		other["audio_input_price"] = summary.AudioInputPrice
	}
	if summary.ImageGenerationCallPrice > 0 {
		other["image_generation_call"] = true
		other["image_generation_call_price"] = summary.ImageGenerationCallPrice
	}
	if summary.CacheCreationTokens > 0 {
		other["cache_creation_tokens"] = summary.CacheCreationTokens
		other["cache_creation_ratio"] = summary.CacheCreationRatio
	}
	if summary.CacheCreationTokens5m > 0 {
		other["cache_creation_tokens_5m"] = summary.CacheCreationTokens5m
		other["cache_creation_ratio_5m"] = summary.CacheCreationRatio5m
	}
	if summary.CacheCreationTokens1h > 0 {
		other["cache_creation_tokens_1h"] = summary.CacheCreationTokens1h
		other["cache_creation_ratio_1h"] = summary.CacheCreationRatio1h
	}
	cacheWriteTokens := cacheWriteTokensTotal(summary)
	if cacheWriteTokens > 0 {
		// cache_write_tokens: normalized cache creation total for UI display.
		// If split 5m/1h values are present, this is their sum; otherwise it falls back
		// to cache_creation_tokens.
		other["cache_write_tokens"] = cacheWriteTokens
	}
	if relayInfo.GetFinalRequestRelayFormat() != types.RelayFormatClaude && billingUsage != nil && billingUsage.UsageSource != "" && billingUsage.InputTokens > 0 {
		// input_tokens_total: explicit normalized total input used by the usage log UI.
		// Only write this field when upstream/current conversion has already provided a
		// reliable total input value and tagged the usage source. Do not infer it from
		// prompt/cache fields here, otherwise old upstream payloads may be double-counted.
		other["input_tokens_total"] = billingUsage.InputTokens
	}
	if tieredBillingApplied {
		InjectTieredBillingInfo(other, relayInfo, tieredResult)
	}

	attachQuotaSaturation(ctx, relayInfo, other)

	model.RecordConsumeLog(ctx, relayInfo.UserId, model.RecordConsumeLogParams{
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     summary.PromptTokens,
		CompletionTokens: summary.CompletionTokens,
		ModelName:        logModel,
		TokenName:        summary.TokenName,
		Quota:            summary.Quota,
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(summary.UseTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
	})
	gopool.Go(func() {
		perfmetrics.RecordRelaySample(relayInfo, true, int64(summary.CompletionTokens))
	})
}
