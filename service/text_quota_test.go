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

	// usage 缺失但请求带 cache_control 时:把已知输入按缓存写入率(×1.25)保守计费,
	// 而非普通输入(×1),保证「本站收 ≥ 上游扣」。输入侧计费 token 总量不变(仍为 100),
	// 只是费率提到缓存写入率;PromptTokens 转记为缓存写入并置 0,避免同批 token 重复计。
	require.Equal(t, 0, usage.PromptTokens)
	require.Equal(t, 100, usage.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 100, usage.ClaudeCacheCreation5mTokens)
	require.True(t, common.GetContextKeyBool(ctx, constant.ContextKeyLocalCountTokens))
	require.Equal(t, "local_cache_control_estimate", common.GetContextKeyString(ctx, constant.ContextKeyUsageFallback))
	require.Equal(t, "estimated_conservative", common.GetContextKeyString(ctx, constant.ContextKeyUsageReliability))
}

func TestResponseText2UsageCacheControlFallbackUses1hWhenRequested(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	common.SetContextKey(ctx, constant.ContextKeyRequestHasCacheControl, true)
	common.SetContextKey(ctx, constant.ContextKeyRequestHasCacheControl1h, true)

	usage := ResponseText2Usage(ctx, "", "claude-3-7-sonnet", 100)

	require.Equal(t, 0, usage.PromptTokens)
	require.Equal(t, 100, usage.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 0, usage.ClaudeCacheCreation5mTokens)
	require.Equal(t, 100, usage.ClaudeCacheCreation1hTokens)
	require.Equal(t, "estimated_conservative", common.GetContextKeyString(ctx, constant.ContextKeyUsageReliability))
}

func TestApplyLocalCountCacheControlFallbackFillsPartialUsageGapAs1h(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	common.SetContextKey(ctx, constant.ContextKeyLocalCountTokens, true)
	common.SetContextKey(ctx, constant.ContextKeyRequestHasCacheControl, true)
	common.SetContextKey(ctx, constant.ContextKeyRequestHasCacheControl1h, true)

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 7,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 50,
		},
	}

	ApplyLocalCountCacheControlFallbackWithObservedInput(ctx, usage, 1000)

	require.Equal(t, 100, usage.PromptTokens)
	require.Equal(t, 50, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 850, usage.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 0, usage.ClaudeCacheCreation5mTokens)
	require.Equal(t, 850, usage.ClaudeCacheCreation1hTokens)
	require.Equal(t, "upstream_usage_partial_with_cache_control", common.GetContextKeyString(ctx, constant.ContextKeyUsageFallbackReason))
	require.Equal(t, "estimated_conservative", common.GetContextKeyString(ctx, constant.ContextKeyUsageReliability))
}

func TestApplyLocalCountCacheControlFallbackLeavesCompletePartialUsageAlone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	common.SetContextKey(ctx, constant.ContextKeyLocalCountTokens, true)
	common.SetContextKey(ctx, constant.ContextKeyRequestHasCacheControl, true)

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 7,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         50,
			CachedCreationTokens: 850,
		},
		ClaudeCacheCreation5mTokens: 850,
	}

	ApplyLocalCountCacheControlFallbackWithObservedInput(ctx, usage, 1000)

	require.Equal(t, 100, usage.PromptTokens)
	require.Equal(t, 50, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 850, usage.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 850, usage.ClaudeCacheCreation5mTokens)
	require.Equal(t, 0, usage.ClaudeCacheCreation1hTokens)
	require.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyUsageReliability))
}

func TestQuotaDecimalToIntCeilsPositiveConsumption(t *testing.T) {
	require.Equal(t, 1, quotaDecimalToInt(decimal.RequireFromString("0.01")))
	require.Equal(t, 65, quotaDecimalToInt(decimal.RequireFromString("64.01")))
	require.Equal(t, 65, quotaDecimalToInt(decimal.RequireFromString("65")))
}

func TestQuotaDecimalToIntRoundsNegativeCorrectionsTowardZero(t *testing.T) {
	require.Equal(t, 0, quotaDecimalToInt(decimal.RequireFromString("-0.01")))
	require.Equal(t, -64, quotaDecimalToInt(decimal.RequireFromString("-64.99")))
	require.Equal(t, -65, quotaDecimalToInt(decimal.RequireFromString("-65")))
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

func TestCalculateTextQuotaSummaryUsesClaudeBillingUsageBeforeTopLevelUsage(t *testing.T) {
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
			GroupRatioInfo:       types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     999,
		CompletionTokens: 999,
		TotalTokens:      1998,
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{
			InputTokens:              70,
			CacheReadInputTokens:     30,
			CacheCreationInputTokens: 20,
			OutputTokens:             7,
			CacheCreation: &dto.ClaudeCacheCreationUsage{
				Ephemeral5mInputTokens: 12,
				Ephemeral1hInputTokens: 8,
			},
		}),
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveBillingUsage(usage))

	require.True(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, dto.BillingUsageSemanticAnthropic, summary.UsageSemantic)
	require.Equal(t, 70, summary.PromptTokens)
	require.Equal(t, 7, summary.CompletionTokens)
	require.Equal(t, 30, summary.CacheTokens)
	require.Equal(t, 20, summary.CacheCreationTokens)
	require.Equal(t, 12, summary.CacheCreationTokens5m)
	require.Equal(t, 8, summary.CacheCreationTokens1h)
	require.Equal(t, 118, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesGeminiBillingUsageBeforeTopLevelUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "gemini-2.5-flash",
		PriceData: types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 2,
			CacheRatio:      0.1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     999,
		CompletionTokens: 999,
		TotalTokens:      1998,
		BillingUsage: dto.NewGeminiChatBillingUsage(&dto.GeminiUsageMetadata{
			PromptTokenCount:        100,
			ToolUsePromptTokenCount: 5,
			CandidatesTokenCount:    20,
			ThoughtsTokenCount:      3,
			TotalTokenCount:         128,
			CachedContentTokenCount: 7,
		}),
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveBillingUsage(usage))

	require.False(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, dto.BillingUsageSemanticGemini, summary.UsageSemantic)
	require.Equal(t, 105, summary.PromptTokens)
	require.Equal(t, 23, summary.CompletionTokens)
	require.Equal(t, 7, summary.CacheTokens)
	require.Equal(t, 128, summary.TotalTokens)
	require.Equal(t, 145, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesOpenAIBillingUsageBeforeTopLevelUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatClaude,
		OriginModelName: "gpt-4o",
		PriceData: types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 2,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     999,
		CompletionTokens: 999,
		TotalTokens:      1998,
		BillingUsage: dto.NewOpenAIChatBillingUsage(&dto.Usage{
			PromptTokens:     80,
			CompletionTokens: 9,
			TotalTokens:      89,
		}),
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveBillingUsage(usage))

	require.False(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, dto.BillingUsageSemanticOpenAI, summary.UsageSemantic)
	require.Equal(t, 80, summary.PromptTokens)
	require.Equal(t, 9, summary.CompletionTokens)
	require.Equal(t, 89, summary.TotalTokens)
	require.Equal(t, 98, summary.Quota)
}

func TestUsageBillingPathForLog(t *testing.T) {
	require.Equal(t, usageBillingPathLocal, usageBillingPathForLog(true, &dto.Usage{
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 1}),
	}))
	require.Equal(t, usageBillingPathUpstream, usageBillingPathForLog(false, &dto.Usage{}))
	require.Equal(t, usageBillingPathOpenAI, usageBillingPathForLog(false, &dto.Usage{
		BillingUsage: dto.NewOpenAIChatBillingUsage(&dto.Usage{PromptTokens: 1}),
	}))
	require.Equal(t, usageBillingPathAnthropic, usageBillingPathForLog(false, &dto.Usage{
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 1}),
	}))
	require.Equal(t, usageBillingPathGemini, usageBillingPathForLog(false, &dto.Usage{
		BillingUsage: dto.NewGeminiChatBillingUsage(&dto.GeminiUsageMetadata{PromptTokenCount: 1}),
	}))
	require.Equal(t, usageBillingPathGeminiEstimated, usageBillingPathForLog(false, &dto.Usage{
		BillingUsage: dto.NewEstimatedGeminiChatBillingUsage(&dto.Usage{PromptTokens: 1}),
	}))
}

func TestAppendUsageBillingPathForLogWritesAdminInfo(t *testing.T) {
	other := map[string]interface{}{
		"admin_info": map[string]interface{}{},
	}
	appendUsageBillingPathForLog(other, false, &dto.Usage{
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 1}),
	})

	adminInfo, ok := other["admin_info"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, usageBillingPathAnthropic, adminInfo["usage_billing_path"])

	other = map[string]interface{}{}
	appendUsageBillingPathForLog(other, true, nil)
	adminInfo, ok = other["admin_info"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, usageBillingPathLocal, adminInfo["usage_billing_path"])
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

	t.Run("overflowing split cache creation saturates", func(t *testing.T) {
		maxInt := int(^uint(0) >> 1)
		summary := textQuotaSummary{
			CacheCreationTokens5m: maxInt,
			CacheCreationTokens1h: maxInt,
		}
		require.Equal(t, maxInt, cacheWriteTokensTotal(summary))
	})
}

func TestChargeableTextTokenCountSaturatesAndIgnoresNegativeValues(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	summary := textQuotaSummary{
		PromptTokens:          maxInt,
		CompletionTokens:      maxInt,
		CacheTokens:           -1,
		CacheCreationTokens:   -1,
		CacheCreationTokens5m: maxInt,
		CacheCreationTokens1h: maxInt,
		ImageTokens:           -1,
		AudioTokens:           maxInt,
	}

	require.Equal(t, maxInt, chargeableTextTokenCount(summary))
}

func TestUsageFromClaudeBillingUsageUsesMaximumCacheBuckets(t *testing.T) {
	billingUsage := &dto.BillingUsage{
		Source:   dto.BillingUsageSourceClaudeMessages,
		Semantic: dto.BillingUsageSemanticAnthropic,
		ClaudeUsage: &dto.ClaudeUsage{
			InputTokens: 1,
			CacheCreation: &dto.ClaudeCacheCreationUsage{
				Ephemeral5mInputTokens: 10,
				Ephemeral1hInputTokens: 40,
			},
			ClaudeCacheCreation5mTokens: 30,
			ClaudeCacheCreation1hTokens: 20,
		},
	}

	usage := usageFromClaudeBillingUsage(billingUsage)
	require.Equal(t, 30, usage.ClaudeCacheCreation5mTokens)
	require.Equal(t, 40, usage.ClaudeCacheCreation1hTokens)
}

func TestCalculateTextQuotaSummaryIgnoresNegativeUpstreamTokenCounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "negative-usage-model",
		PriceData: types.PriceData{
			ModelRatio:         1,
			CompletionRatio:    2,
			CacheRatio:         0.1,
			CacheCreationRatio: 1.25,
			ImageRatio:         1,
			GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}
	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: -100,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:     -200,
			CacheWriteTokens: -300,
			ImageTokens:      -400,
			AudioTokens:      -500,
		},
		ClaudeCacheCreation5mTokens: -600,
		ClaudeCacheCreation1hTokens: -700,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	require.Equal(t, 1000, summary.PromptTokens)
	require.Zero(t, summary.CompletionTokens)
	require.Zero(t, summary.CacheTokens)
	require.Zero(t, summary.CacheCreationTokens)
	require.Zero(t, summary.CacheCreationTokens5m)
	require.Zero(t, summary.CacheCreationTokens1h)
	require.Equal(t, 1000, summary.TotalTokens)
	require.Equal(t, 1000, summary.ChargeableTokens)
	require.Equal(t, 1000, summary.Quota)
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

func TestCalculateTextQuotaSummaryBillsOpenAICacheWriteTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "gpt-5.1",
		PriceData: types.PriceData{
			ModelRatio:         1,
			CompletionRatio:    2,
			CacheRatio:         0.1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	t.Run("uncached remainder stays positive", func(t *testing.T) {
		usage := &dto.Usage{
			PromptTokens:     1473,
			CompletionTokens: 19,
			PromptTokensDetails: dto.InputTokenDetails{
				CacheWriteTokens: 1470,
			},
		}

		summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

		require.Equal(t, 1470, summary.CacheCreationTokens)
		// (1473-0-1470) + 1470*1.25 + 19*2 = 3 + 1837.5 + 38 = 1878.5 => 1879
		require.Equal(t, 1879, summary.Quota)
	})

	t.Run("uncached remainder clamps to zero", func(t *testing.T) {
		// Real OpenAI payload shape: cached_tokens + cache_write_tokens exceeds
		// prompt_tokens because both are unadjusted prefix counts. The negative
		// remainder must clamp to zero, never turn into a negative base charge.
		usage := &dto.Usage{
			PromptTokens:     3619,
			CompletionTokens: 36,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens:     2921,
				CacheWriteTokens: 3616,
			},
		}

		summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

		require.Equal(t, 3619, summary.PromptTokens)
		require.Equal(t, 3616, summary.CacheCreationTokens)
		// max(3619-2921-3616, 0) + 2921*0.1 + 3616*1.25 + 36*2 = 4884.1.
		// The custom billing policy rounds positive settlement quota up once, so
		// fractional charges cannot be truncated into an undercharge.
		require.Equal(t, 4885, summary.Quota)
	})
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

// TestApplyLocalCountCacheControlFallbackConservativeCacheWrite 复刻线上少扣场景的根因:
// Claude CLI 请求带 cache_control(断点数 3)、客户端中途断开导致上游 usage 缺失。
// v2 曾把这类请求全按普通输入(×1)计,对重度用缓存的流量大幅少扣;v3 改为按缓存写入率
// (×1.25)保守估算,保证本站收 ≥ 上游扣。同时验证不再重演 v1 的 input×断点数爆扣
// (缓存写入量等于输入量,而非输入×3)。
func TestApplyLocalCountCacheControlFallbackConservativeCacheWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	common.SetContextKey(ctx, constant.ContextKeyLocalCountTokens, true)
	common.SetContextKey(ctx, constant.ContextKeyRequestHasCacheControl, true)
	common.SetContextKey(ctx, constant.ContextKeyRequestCacheControlCount, 3)

	usage := &dto.Usage{PromptTokens: 995481}
	ApplyLocalCountCacheControlFallback(ctx, usage)

	require.Equal(t, 0, usage.PromptTokens, "输入转记为缓存写入")
	require.Equal(t, 995481, usage.PromptTokensDetails.CachedCreationTokens, "按输入量保守估为缓存写入(非 input×3 爆扣)")
	require.Equal(t, 995481, usage.ClaudeCacheCreation5mTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, "local_cache_control_estimate", common.GetContextKeyString(ctx, constant.ContextKeyUsageFallback))
	require.Equal(t, "estimated_conservative", common.GetContextKeyString(ctx, constant.ContextKeyUsageReliability))
}

// TestApplyLocalCountCacheControlFallbackSkipsRealUsage 确认上游已回真实缓存字段时,
// 兜底绝不改动(不把真实 usage 转成估算)。
func TestApplyLocalCountCacheControlFallbackSkipsRealUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	common.SetContextKey(ctx, constant.ContextKeyLocalCountTokens, true)
	common.SetContextKey(ctx, constant.ContextKeyRequestHasCacheControl, true)

	usage := &dto.Usage{
		PromptTokens: 787450,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 244243, // 上游 message_start 真实返回
		},
	}
	ApplyLocalCountCacheControlFallback(ctx, usage)

	require.Equal(t, 787450, usage.PromptTokens, "真实输入不动")
	require.Equal(t, 244243, usage.PromptTokensDetails.CachedCreationTokens, "真实缓存写入不动")
	require.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyUsageReliability), "有真实值不标记为估算")
}

// TestCalculateTextQuotaSummaryCacheWriteOnlyNotZeroed 复刻部署后发现的漏扣回归:
// cache_control 兜底把输入全部转记为缓存写入(PromptTokens=0),且响应为空(CompletionTokens=0)
// 时,若空请求保护只看 prompt+completion,会把已算好的缓存写入 quota 清零 —— 生产实测
// 38 万缓存写入 token 收费 0。修复:用 ChargeableTokens 判空,TotalTokens 仍保持真实
// prompt+completion 语义。此测试确保纯缓存写入请求按公式精确扣费。
func TestCalculateTextQuotaSummaryCacheWriteOnlyNotZeroed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	// 兜底产物:38 万输入全部转记为 5m 缓存写入,prompt/completion 均为 0。
	usage := &dto.Usage{
		PromptTokens:     0,
		CompletionTokens: 0,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 382182,
		},
		ClaudeCacheCreation5mTokens: 382182,
	}
	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatClaude,
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "claude-opus-4-8",
		PriceData: types.PriceData{
			ModelRatio:           2.5,
			CompletionRatio:      5,
			CacheRatio:           0.1,
			CacheCreationRatio:   1.25,
			CacheCreation5mRatio: 1.25,
			CacheCreation1hRatio: 2,
			GroupRatioInfo:       types.GroupRatioInfo{GroupRatio: 0.08},
		},
		StartTime: time.Now(),
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	require.Equal(t, 0, summary.TotalTokens, "TotalTokens 保持 prompt+completion 真实语义")
	require.Equal(t, 382182, summary.ChargeableTokens, "缓存写入 token 必须计入判空用的 ChargeableTokens")
	require.Equal(t, 95546, summary.Quota, "382182 * 1.25 * 2.5 * 0.08 应向上取整扣费")
}

func TestCalculateTextQuotaSummaryChargeableTokensAvoidsClaudeCacheCreationDoubleCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	usage := &dto.Usage{
		PromptTokens:     0,
		CompletionTokens: 0,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 100,
		},
		ClaudeCacheCreation5mTokens: 100,
	}
	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatClaude,
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "claude-opus-4-8",
		PriceData: types.PriceData{
			ModelRatio:           1,
			CompletionRatio:      1,
			CacheRatio:           0.1,
			CacheCreationRatio:   1.25,
			CacheCreation5mRatio: 1.25,
			CacheCreation1hRatio: 2,
			GroupRatioInfo:       types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	require.Equal(t, 0, summary.TotalTokens)
	require.Equal(t, 100, summary.ChargeableTokens, "aggregate 与 5m/1h split 描述同一批 cache creation,判空计数不能双算")
}

// TestClampEstimatedUsageToObservedInput 验证第二道防线:即便某段估算逻辑仍然伪造出
// 超过真实输入的缓存 token(模拟历史 v1 的 input×断点数 bug),clamp 也会把缓存写入
// 削减到「精确等于观测输入」,保证最坏也只按观测输入量计费,不可能爆扣。注意:削减后
// 仍保留缓存写入性质(×1.25),不退回普通输入(那会少扣)。
func TestClampEstimatedUsageToObservedInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	common.SetContextKey(ctx, constant.ContextKeyUsageReliability, "estimated_conservative")

	// 模拟历史爆扣: 观测输入 995481, 伪造缓存写入 995481×3
	usage := &dto.Usage{
		PromptTokens: 0,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 995481 * 3,
		},
		ClaudeCacheCreation5mTokens: 995481 * 3,
	}
	ClampEstimatedUsageToObservedInput(ctx, usage, 995481)

	require.Equal(t, 995481, usage.PromptTokensDetails.CachedCreationTokens, "缓存写入削减到观测输入")
	require.Equal(t, 995481, usage.ClaudeCacheCreation5mTokens, "5m 拆分同步削减")
	require.Equal(t, 0, usage.PromptTokens, "不退回普通输入(那会少扣)")
	require.Equal(t, "estimated_usage_clamped_to_observed_input", common.GetContextKeyString(ctx, constant.ContextKeyUsageFallbackReason))
}

// TestClampEstimatedUsageAllowsConservativeCacheWrite 确认 v3 兜底产出的合理缓存写入
// (缓存写入量 == 观测输入)不会被 clamp 误伤清零——这正是 v2 clamp 传参 bug 的回归点。
func TestClampEstimatedUsageAllowsConservativeCacheWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	common.SetContextKey(ctx, constant.ContextKeyUsageReliability, "estimated_conservative")

	// ApplyLocalCountCacheControlFallback 的产物: 输入 787450 全部转记为缓存写入。
	usage := &dto.Usage{
		PromptTokens: 0,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 787450,
		},
		ClaudeCacheCreation5mTokens: 787450,
	}
	ClampEstimatedUsageToObservedInput(ctx, usage, 787450)

	require.Equal(t, 787450, usage.PromptTokensDetails.CachedCreationTokens, "合理缓存写入原样保留,不被误伤")
	require.Equal(t, 787450, usage.ClaudeCacheCreation5mTokens)
}

func TestClampEstimatedUsagePreserves1hCacheCreationBucket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	common.SetContextKey(ctx, constant.ContextKeyUsageReliability, "estimated_conservative")
	common.SetContextKey(ctx, constant.ContextKeyRequestHasCacheControl1h, true)

	usage := &dto.Usage{
		PromptTokens: 0,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 1000,
		},
		ClaudeCacheCreation1hTokens: 1000,
	}

	ClampEstimatedUsageToObservedInput(ctx, usage, 600)

	require.Equal(t, 600, usage.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 0, usage.ClaudeCacheCreation5mTokens)
	require.Equal(t, 600, usage.ClaudeCacheCreation1hTokens)
	require.Equal(t, "estimated_usage_clamped_to_observed_input", common.GetContextKeyString(ctx, constant.ContextKeyUsageFallbackReason))
}

// TestConservativeFallbackBillsAtLeastPlainInput 端到端验证经济结果:client_gone 且
// 带 cache_control 时,v3 兜底(缓存写入 ×1.25)算出的 quota 严格高于 v2 的普通输入
// (×1)口径,这正是修复少扣的落点。同一批输入 token,费率从 ×1 提到 ×1.25。
func TestConservativeFallbackBillsAtLeastPlainInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	common.SetContextKey(ctx, constant.ContextKeyLocalCountTokens, true)
	common.SetContextKey(ctx, constant.ContextKeyRequestHasCacheControl, true)

	priceData := types.PriceData{
		ModelRatio:           2.5,
		CompletionRatio:      5,
		CacheRatio:           0.1,
		CacheCreationRatio:   1.25,
		CacheCreation5mRatio: 1.25,
		CacheCreation1hRatio: 2,
		GroupRatioInfo:       types.GroupRatioInfo{GroupRatio: 0.65},
	}
	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatClaude,
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "claude-opus-4-7",
		PriceData:               priceData,
		StartTime:               time.Now(),
	}

	// v2 口径基线:同样输入按普通输入(×1)计。
	plainUsage := &dto.Usage{PromptTokens: 100000, CompletionTokens: 1}
	plainSummary := calculateTextQuotaSummary(ctx, relayInfo, plainUsage)

	// v3 兜底:输入转记为缓存写入(×1.25)。
	fallbackUsage := &dto.Usage{PromptTokens: 100000, CompletionTokens: 1}
	ApplyLocalCountCacheControlFallback(ctx, fallbackUsage)
	fallbackSummary := calculateTextQuotaSummary(ctx, relayInfo, fallbackUsage)

	require.Greater(t, fallbackSummary.Quota, plainSummary.Quota,
		"缓存写入率(×1.25)计费必须严格高于普通输入(×1),否则仍在少扣")
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

func TestCalculateTextQuotaSummaryFixedPriceAppliesImageCountOnceAndAllowsOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	priceData := types.PriceData{
		ModelPrice: 0.12,
		UsePrice:   true,
		GroupRatioInfo: types.GroupRatioInfo{
			GroupRatio: 1,
		},
	}
	priceData.AddOtherRatio("n", 3)
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "dall-e-3",
		PriceData:       priceData,
		StartTime:       time.Now(),
	}
	usage := &dto.Usage{PromptTokens: 1, TotalTokens: 1}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)
	require.Equal(t, 180000, summary.Quota)

	// An adaptor-reported actual count replaces the requested count rather
	// than multiplying it a second time.
	relayInfo.PriceData.AddOtherRatio("n", 2)
	summary = calculateTextQuotaSummary(ctx, relayInfo, usage)
	require.Equal(t, 120000, summary.Quota)
}
