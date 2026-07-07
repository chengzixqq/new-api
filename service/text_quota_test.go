package service

import (
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func testFloat64Ptr(value float64) *float64 {
	return &value
}

func TestResponseText2UsageCacheControlFallbackUsesConservativeCacheCreation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	common.SetContextKey(ctx, constant.ContextKeyRequestHasCacheControl, true)
	common.SetContextKey(ctx, constant.ContextKeyRequestCacheControlCount, 2)

	usage := ResponseText2Usage(ctx, "", "claude-3-7-sonnet", 100)

	// usage 缺失但请求带 cache_control 时:不再伪造缓存写入(历史的 input×断点数
	// 会爆扣),输入按普通输入率计费,cache_control 断点数仅作日志线索。
	require.Equal(t, 100, usage.PromptTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CachedCreationTokens)
	require.True(t, common.GetContextKeyBool(ctx, constant.ContextKeyLocalCountTokens))
	require.Equal(t, "local_cache_control_estimate", common.GetContextKeyString(ctx, constant.ContextKeyUsageFallback))
}

func TestQuotaDecimalToIntCeilsPositiveConsumption(t *testing.T) {
	require.Equal(t, 1, quotaDecimalToInt(decimal.RequireFromString("0.01")))
	require.Equal(t, 65, quotaDecimalToInt(decimal.RequireFromString("64.01")))
	require.Equal(t, 65, quotaDecimalToInt(decimal.RequireFromString("65")))
}

func TestCalculateTextQuotaSummaryUnifiedForClaudeSemantic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 200,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         100,
			CachedCreationTokens: 50,
		},
		ClaudeCacheCreation5mTokens: 10,
		ClaudeCacheCreation1hTokens: 20,
	}

	priceData := types.PriceData{
		ModelRatio:           1,
		CompletionRatio:      2,
		CacheRatio:           0.1,
		CacheCreationRatio:   1.25,
		CacheCreation5mRatio: 1.25,
		CacheCreation1hRatio: 2,
		GroupRatioInfo: types.GroupRatioInfo{
			GroupRatio: 1,
		},
	}

	chatRelayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "claude-3-7-sonnet",
		PriceData:               priceData,
		StartTime:               time.Now(),
	}
	messageRelayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatClaude,
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "claude-3-7-sonnet",
		PriceData:               priceData,
		StartTime:               time.Now(),
	}

	chatSummary := calculateTextQuotaSummary(ctx, chatRelayInfo, usage)
	messageSummary := calculateTextQuotaSummary(ctx, messageRelayInfo, usage)

	require.Equal(t, messageSummary.Quota, chatSummary.Quota)
	require.Equal(t, messageSummary.CacheCreationTokens5m, chatSummary.CacheCreationTokens5m)
	require.Equal(t, messageSummary.CacheCreationTokens1h, chatSummary.CacheCreationTokens1h)
	require.True(t, chatSummary.IsClaudeUsageSemantic)
	require.Equal(t, 1488, chatSummary.Quota)
}

func TestCalculateTextQuotaSummaryUsesSplitClaudeCacheCreationRatios(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "claude-3-7-sonnet",
		PriceData: types.PriceData{
			ModelRatio:           1,
			CompletionRatio:      1,
			CacheRatio:           0,
			CacheCreationRatio:   1,
			CacheCreation5mRatio: 2,
			CacheCreation1hRatio: 3,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 1,
			},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 0,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 10,
		},
		ClaudeCacheCreation5mTokens: 2,
		ClaudeCacheCreation1hTokens: 3,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// 100 + remaining(5)*1 + 2*2 + 3*3 = 118
	require.Equal(t, 118, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesAnthropicUsageSemanticFromUpstreamUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "claude-3-7-sonnet",
		PriceData: types.PriceData{
			ModelRatio:           1,
			CompletionRatio:      2,
			CacheRatio:           0.1,
			CacheCreationRatio:   1.25,
			CacheCreation5mRatio: 1.25,
			CacheCreation1hRatio: 2,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 1,
			},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 200,
		UsageSemantic:    "anthropic",
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         100,
			CachedCreationTokens: 50,
		},
		ClaudeCacheCreation5mTokens: 10,
		ClaudeCacheCreation1hTokens: 20,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	require.True(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, "anthropic", summary.UsageSemantic)
	require.Equal(t, 1488, summary.Quota)
}

func TestCacheWriteTokensTotal(t *testing.T) {
	t.Run("split cache creation", func(t *testing.T) {
		summary := textQuotaSummary{
			CacheCreationTokens:   50,
			CacheCreationTokens5m: 10,
			CacheCreationTokens1h: 20,
		}
		require.Equal(t, 50, cacheWriteTokensTotal(summary))
	})

	t.Run("legacy cache creation", func(t *testing.T) {
		summary := textQuotaSummary{CacheCreationTokens: 50}
		require.Equal(t, 50, cacheWriteTokensTotal(summary))
	})

	t.Run("split cache creation without aggregate remainder", func(t *testing.T) {
		summary := textQuotaSummary{
			CacheCreationTokens5m: 10,
			CacheCreationTokens1h: 20,
		}
		require.Equal(t, 30, cacheWriteTokensTotal(summary))
	})
}

func TestCalculateTextQuotaSummaryHandlesLegacyClaudeDerivedOpenAIUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "claude-3-7-sonnet",
		PriceData: types.PriceData{
			ModelRatio:           1,
			CompletionRatio:      5,
			CacheRatio:           0.1,
			CacheCreationRatio:   1.25,
			CacheCreation5mRatio: 1.25,
			CacheCreation1hRatio: 2,
			GroupRatioInfo:       types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     62,
		CompletionTokens: 95,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 3544,
		},
		ClaudeCacheCreation5mTokens: 586,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// 62 + 3544*0.1 + 586*1.25 + 95*5 = 1624.9 => 1624
	require.Equal(t, 1624, summary.Quota)
}

func TestCalculateTextQuotaSummarySeparatesOpenRouterCacheReadFromPromptBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "openai/gpt-4.1",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenRouter,
		},
		PriceData: types.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheRatio:         0.1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     2604,
		CompletionTokens: 383,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 2432,
		},
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// OpenRouter OpenAI-format display keeps prompt_tokens as total input,
	// but billing still separates normal input from cache read tokens.
	// quota = (2604 - 2432) + 2432*0.1 + 383 = 798.2 => 799
	require.Equal(t, 2604, summary.PromptTokens)
	require.Equal(t, 799, summary.Quota)
}

func TestCalculateTextQuotaSummarySeparatesOpenRouterCacheCreationFromPromptBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "openai/gpt-4.1",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenRouter,
		},
		PriceData: types.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     2604,
		CompletionTokens: 383,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 100,
		},
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// prompt_tokens is still logged as total input, but cache creation is billed separately.
	// quota = (2604 - 100) + 100*1.25 + 383 = 3012
	require.Equal(t, 2604, summary.PromptTokens)
	require.Equal(t, 3012, summary.Quota)
}

func TestCalculateTextQuotaSummaryKeepsPrePRClaudeOpenRouterBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "anthropic/claude-3.7-sonnet",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenRouter,
		},
		PriceData: types.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheRatio:         0.1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     2604,
		CompletionTokens: 383,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 2432,
		},
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// Pre-PR PostClaudeConsumeQuota behavior for OpenRouter:
	// prompt = 2604 - 2432 = 172
	// quota = 172 + 2432*0.1 + 383 = 798.2 => 799
	require.True(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, 172, summary.PromptTokens)
	require.Equal(t, 799, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesModelGroupPriceOverrides(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	override := &types.ModelGroupPricing{
		PromptPrice:     testFloat64Ptr(0.1),
		CompletionPrice: testFloat64Ptr(0.6),
		CachePrice:      testFloat64Ptr(0.1),
	}
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.5",
		PriceData: types.PriceData{
			ModelRatio:      99,
			CompletionRatio: 99,
			CacheRatio:      99,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio:           99,
				ModelGroupPricing:    override,
				HasModelGroupPricing: true,
			},
			GroupPriceOverride: override,
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     1_000_000,
		CompletionTokens: 500_000,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 200_000,
		},
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// Direct group prices are USD / 1M tokens and override the global ratios:
	// normal input: 800k * $0.1 = $0.08
	// cache read:   200k * $0.1 = $0.02
	// output:       500k * $0.6 = $0.30
	// total $0.40 * 500000 quota/unit = 200000
	require.Equal(t, 200000, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesModelGroupPerRequestPriceOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	override := &types.ModelGroupPricing{
		ModelPrice: testFloat64Ptr(0.25),
	}
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gpt-image-2",
		PriceData: types.PriceData{
			UsePrice:   true,
			ModelPrice: 99,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio:           99,
				ModelGroupPricing:    override,
				HasModelGroupPricing: true,
			},
			GroupPriceOverride: override,
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens: 1,
		TotalTokens:  1,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	require.Equal(t, 125000, summary.Quota)
	require.Equal(t, 0.25, summary.ModelPrice)
}

func TestComposeTieredTextQuotaKeepsToolCallSurcharges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Set("image_generation_call", true)
	ctx.Set("image_generation_call_quality", "low")
	ctx.Set("image_generation_call_size", "1024x1024")

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "o1",
		PriceData: types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		},
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: &relaycommon.BuildInToolInfo{
					CallCount: 1,
				},
				dto.BuildInToolFileSearch: &relaycommon.BuildInToolInfo{
					CallCount: 2,
				},
			},
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:               "tiered_expr",
			GroupRatio:                1,
			EstimatedQuotaBeforeGroup: 1000,
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)
	quota := composeTieredTextQuota(relayInfo, summary, 1000, &billingexpr.TieredResult{
		ActualQuotaBeforeGroup: 1000,
		ActualQuotaAfterGroup:  1000,
	})

	require.Equal(t, int64(13000), summary.ToolCallSurchargeQuota.Round(0).IntPart())
	require.Equal(t, 14000, quota)
}

func TestComposeTieredTextQuotaFallbackKeepsToolCallSurcharges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Set("claude_web_search_requests", 2)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-7-sonnet",
		PriceData: types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.25},
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:               "tiered_expr",
			GroupRatio:                1.25,
			EstimatedQuotaBeforeGroup: 1000,
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)
	quota := composeTieredTextQuota(relayInfo, summary, 1250, nil)

	require.Equal(t, int64(12500), summary.ToolCallSurchargeQuota.Round(0).IntPart())
	require.Equal(t, 13750, quota)
}

func TestComposeTieredTextQuotaErrorFallbackUsesPreConsumedQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Set("claude_web_search_requests", 2)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-7-sonnet",
		PriceData: types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.25},
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:               "tiered_expr",
			GroupRatio:                1.25,
			EstimatedQuotaBeforeGroup: 1000,
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// tieredResult=nil simulates a settlement error where TryTieredSettle
	// falls back to FinalPreConsumedQuota (2000), which differs from
	// EstimatedQuotaBeforeGroup * GroupRatio (1250).
	preConsumedFallback := 2000
	quota := composeTieredTextQuota(relayInfo, summary, preConsumedFallback, nil)

	require.Equal(t, int64(12500), summary.ToolCallSurchargeQuota.Round(0).IntPart())
	require.Equal(t, 14500, quota)
}

func TestCalculateTextQuotaSummaryGroupTieredSnapshotSettles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	// Frozen snapshot carries the GROUP's expression (set at the freeze point).
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "grp-expr-model",
		PriceData: types.PriceData{
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:  "tiered_expr",
			ModelName:    "grp-expr-model",
			ExprString:   `tier("base", p * 7)`,
			GroupRatio:   1,
			QuotaPerUnit: 500000,
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 0, TotalTokens: 1000}
	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)
	ok, quota, _ := TryTieredSettle(relayInfo, BuildTieredTokenParams(usage, false, billingexpr.UsedVars(`tier("base", p * 7)`)))
	require.True(t, ok)
	// 1000 * 7 = 7000 raw / 1e6 * 500000 = 3500
	require.Equal(t, 3500, quota)
	_ = summary
}

// TestTryTieredSettleRecordsClampOnOverflow guards that an oversized tiered
// settlement both saturates the quota and records the clamp on RelayInfo, so
// every consume path (text, audio, WSS) can surface it under admin_info.
func TestTryTieredSettleRecordsClampOnOverflow(t *testing.T) {
	// exprOutput = p * 1e9; quotaBeforeGroup = p*1e9 / 1e6 * 5e5 far exceeds
	// MaxInt32 and must saturate.
	exprStr := `tier("base", p * 1000000000)`
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "overflow-model",
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:  "tiered_expr",
			ExprString:   exprStr,
			ExprHash:     billingexpr.ExprHashString(exprStr),
			GroupRatio:   1,
			QuotaPerUnit: 500_000,
		},
	}

	ok, quota, result := TryTieredSettle(relayInfo, billingexpr.TokenParams{P: 1_000_000_000})

	require.True(t, ok)
	require.NotNil(t, result)
	require.Equal(t, math.MaxInt32, quota, "oversized settlement must clamp, never wrap negative")
	require.NotNil(t, relayInfo.QuotaClamp, "clamp must be recorded on RelayInfo for admin auditing")
	require.Equal(t, common.QuotaClampOverflow, relayInfo.QuotaClamp.Kind)
}

// TestTryTieredSettleNoClampInRange confirms an in-range settlement leaves
// RelayInfo.QuotaClamp nil.
func TestTryTieredSettleNoClampInRange(t *testing.T) {
	exprStr := `tier("base", p * 2 + c * 10)`
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "in-range-model",
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:  "tiered_expr",
			ExprString:   exprStr,
			ExprHash:     billingexpr.ExprHashString(exprStr),
			GroupRatio:   1,
			QuotaPerUnit: 500_000,
		},
	}

	ok, _, result := TryTieredSettle(relayInfo, billingexpr.TokenParams{P: 1000, C: 500})

	require.True(t, ok)
	require.NotNil(t, result)
	require.Nil(t, relayInfo.QuotaClamp, "in-range settlement must not record a clamp")
}

// TestApplyLocalCountCacheControlFallbackNoFakeCacheWrite 复刻线上爆扣场景的根因:
// 上游 usage 缺失、请求带 cache_control(断点数 3)。历史实现会把 CachedCreationTokens
// 估成 input×3 并按最贵的缓存写入费率计费。新实现不再伪造缓存写入,输入按普通输入计。
func TestApplyLocalCountCacheControlFallbackNoFakeCacheWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	common.SetContextKey(ctx, constant.ContextKeyLocalCountTokens, true)
	common.SetContextKey(ctx, constant.ContextKeyRequestHasCacheControl, true)
	common.SetContextKey(ctx, constant.ContextKeyRequestCacheControlCount, 3)

	usage := &dto.Usage{PromptTokens: 995481}
	ApplyLocalCountCacheControlFallback(ctx, usage)

	require.Equal(t, 995481, usage.PromptTokens, "真实输入保持不变")
	require.Equal(t, 0, usage.PromptTokensDetails.CachedCreationTokens, "不再伪造缓存写入(历史 bug: 995481×3)")
	require.Equal(t, 0, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, "local_cache_control_estimate", common.GetContextKeyString(ctx, constant.ContextKeyUsageFallback))
}

// TestClampEstimatedUsageToObservedInput 验证第二道防线:即便某段估算逻辑仍然伪造出
// 超过真实输入的缓存 token(模拟历史 bug 或未来回归),clamp 也会把它拉回普通输入率,
// 保证最坏只按「真实输入 × ×1」计费,不可能爆扣。
func TestClampEstimatedUsageToObservedInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	common.SetContextKey(ctx, constant.ContextKeyUsageReliability, "estimated_conservative")

	// 模拟历史爆扣: 输入 995481, 伪造缓存写入 995481×3
	usage := &dto.Usage{
		PromptTokens: 995481,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 995481 * 3,
		},
	}
	ClampEstimatedUsageToObservedInput(ctx, usage, 995481)

	require.Equal(t, 995481, usage.PromptTokens, "退回真实输入")
	require.Equal(t, 0, usage.PromptTokensDetails.CachedCreationTokens, "越界的伪造缓存写入被清零")
	require.Equal(t, 995481, usage.TotalTokens)
	require.Equal(t, "estimated_usage_clamped_to_observed_input", common.GetContextKeyString(ctx, constant.ContextKeyUsageFallbackReason))
}

// TestClampEstimatedUsageSkipsTrustedUsage 确认 clamp 绝不触碰上游真实 usage:
// 未标记 estimated_conservative 时,即使 cache 字段很大也原样保留。
func TestClampEstimatedUsageSkipsTrustedUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	// 不设置 usage_reliability = estimated_conservative

	usage := &dto.Usage{
		PromptTokens: 1000,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 50000, // 上游真实报告的缓存写入,远超普通输入
		},
	}
	ClampEstimatedUsageToObservedInput(ctx, usage, 1000)

	require.Equal(t, 1000, usage.PromptTokens)
	require.Equal(t, 50000, usage.PromptTokensDetails.CachedCreationTokens, "真实 usage 不被 clamp 触碰")
}
