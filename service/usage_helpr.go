package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
)

//func GetPromptTokens(textRequest dto.GeneralOpenAIRequest, relayMode int) (int, error) {
//	switch relayMode {
//	case constant.RelayModeChatCompletions:
//		return CountTokenMessages(textRequest.Messages, textRequest.Model)
//	case constant.RelayModeCompletions:
//		return CountTokenInput(textRequest.Prompt, textRequest.Model), nil
//	case constant.RelayModeModerations:
//		return CountTokenInput(textRequest.Input, textRequest.Model), nil
//	}
//	return 0, errors.New("unknown relay mode")
//}

func ResponseText2Usage(c *gin.Context, responseText string, modeName string, promptTokens int) *dto.Usage {
	common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
	usage := &dto.Usage{}
	usage.PromptTokens = promptTokens
	usage.CompletionTokens = EstimateTokenByModel(modeName, responseText)
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	ApplyLocalCountCacheControlFallback(c, usage)
	return usage
}

// ApplyLocalCountCacheControlFallback 处理「上游 usage 缺失但请求带 cache_control」
// 的兜底计费。典型触发场景:Claude CLI 流式请求携带 cache_control,用户中途 Ctrl+C
// 断开(client_gone),我们随即关闭上游连接,再也收不到 Anthropic 最终 message_delta
// 里的真实缓存 token —— 但中间商上游(如 uniprep)已按真实缓存量对本站计费。
//
// 历史演变:
//   - v1 曾把 CachedCreationTokens 估成 PromptTokens × cache_control 断点数,物理不成立
//     (断点数不是缓存倍率),会爆扣。
//   - v2(90ef595e)矫枉过正:usage 缺失时缓存字段一律清零、全按普通输入率(×1)计费,
//     结果对重度用缓存的 Claude CLI 流量大幅少扣(实测半天净亏数百刀)。
//
// 现策略(v3):usage 缺失但请求带 cache_control 时,把已知输入按缓存写入率保守估算,
// 而非普通输入(×1)。如果请求显式包含 1h cache_control,则落到 1h 写入桶;否则落到
// 默认 5m 写入桶。理由:带 cache_control 的请求上游几乎必然产生缓存写入,按写入率
// 估算能保证「本站收 ≥ 上游扣」,符合宁可多扣不可少扣的计费红线。上界仍由
// ClampEstimatedUsageToObservedInput 兜住(不超过请求体真实输入),因此不会重演 v1 的爆扣。
//
// 对已返回的真实字段只补缺口:若上游 usage 已完整覆盖观测输入,保持原样;若断流只拿到
// 部分输入侧字段,把缺失部分保守补为缓存写入。
func ApplyLocalCountCacheControlFallback(c *gin.Context, usage *dto.Usage) {
	observedInput := 0
	if usage != nil {
		observedInput = usage.PromptTokens
	}
	ApplyLocalCountCacheControlFallbackWithObservedInput(c, usage, observedInput)
}

func ApplyLocalCountCacheControlFallbackWithObservedInput(c *gin.Context, usage *dto.Usage, observedInput int) {
	if c == nil || usage == nil {
		return
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens) ||
		!common.GetContextKeyBool(c, constant.ContextKeyRequestHasCacheControl) {
		return
	}
	if observedInput <= 0 {
		observedInput = usage.PromptTokens
	}
	if observedInput <= 0 {
		return
	}

	hasCacheUsage := usage.PromptTokensDetails.CachedCreationTokens > 0 ||
		usage.PromptTokensDetails.CachedTokens > 0 ||
		usage.ClaudeCacheCreation5mTokens > 0 ||
		usage.ClaudeCacheCreation1hTokens > 0

	if !hasCacheUsage {
		// Local-count usage has no trusted cache split. Treat the whole observed input as
		// cache creation instead of cheap plain input so interrupted cache_control requests
		// cannot be underbilled.
		usage.PromptTokensDetails.CachedCreationTokens = observedInput
		setEstimatedCacheCreationSplit(c, usage, observedInput)
		usage.PromptTokens = 0
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

		common.SetContextKey(c, constant.ContextKeyUsageFallback, "local_cache_control_estimate")
		common.SetContextKey(c, constant.ContextKeyUsageFallbackReason, "upstream_usage_missing_with_cache_control")
		common.SetContextKey(c, constant.ContextKeyUsageReliability, "estimated_conservative")
		return
	}

	effectiveCreation := effectiveCacheCreationTokens(usage)
	knownInputSide := usage.PromptTokens + usage.PromptTokensDetails.CachedTokens + effectiveCreation
	missingInput := observedInput - knownInputSide
	if missingInput <= 0 {
		return
	}

	// Preserve trusted partial fields and conservatively fill the missing observed
	// input as cache creation, choosing the 1h bucket when the request asked for it.
	usage.PromptTokensDetails.CachedCreationTokens = effectiveCreation + missingInput
	addEstimatedCacheCreationSplit(c, usage, missingInput)
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	common.SetContextKey(c, constant.ContextKeyUsageFallback, "local_cache_control_estimate")
	common.SetContextKey(c, constant.ContextKeyUsageFallbackReason, "upstream_usage_partial_with_cache_control")
	common.SetContextKey(c, constant.ContextKeyUsageReliability, "estimated_conservative")
}

func effectiveCacheCreationTokens(usage *dto.Usage) int {
	if usage == nil {
		return 0
	}
	splitCreation := usage.ClaudeCacheCreation5mTokens + usage.ClaudeCacheCreation1hTokens
	if splitCreation > usage.PromptTokensDetails.CachedCreationTokens {
		return splitCreation
	}
	return usage.PromptTokensDetails.CachedCreationTokens
}

func setEstimatedCacheCreationSplit(c *gin.Context, usage *dto.Usage, tokens int) {
	usage.ClaudeCacheCreation5mTokens = 0
	usage.ClaudeCacheCreation1hTokens = 0
	addEstimatedCacheCreationSplit(c, usage, tokens)
}

func addEstimatedCacheCreationSplit(c *gin.Context, usage *dto.Usage, tokens int) {
	if usage == nil || tokens <= 0 {
		return
	}
	if shouldUseCacheCreation1h(c, usage) {
		usage.ClaudeCacheCreation1hTokens += tokens
		return
	}
	usage.ClaudeCacheCreation5mTokens += tokens
}

func shouldUseCacheCreation1h(c *gin.Context, usage *dto.Usage) bool {
	if c != nil && common.GetContextKeyBool(c, constant.ContextKeyRequestHasCacheControl1h) {
		return true
	}
	return usage != nil && usage.ClaudeCacheCreation1hTokens > 0
}

func ValidUsage(usage *dto.Usage) bool {
	return usage != nil && (usage.PromptTokens != 0 || usage.CompletionTokens != 0)
}

// ClampEstimatedUsageToObservedInput 是计费路径的第二道防线(防御纵深):当且仅当
// 本次 usage 被标记为本地估算(usage_reliability=estimated_conservative)时,强制
// 一条物理不变量——「估算出的输入侧计费 token(普通输入 + 缓存读 + 缓存写入)之和,
// 不得超过我们观测到的真实输入 token」。
//
// 缓存读/缓存写入在物理上都只是「输入的一部分」,其总和永不可能超过总输入。若超过,
// 必然是某段估算逻辑出错(历史上的 PromptTokens × cache_control_count 就是典型)。
// 这里一旦检测到越界,把超出部分从缓存写入里削掉、令输入侧总量精确等于观测输入,
// 并打 warn 日志,保证任何估算路径失控时最坏也只按「观测输入」的量计费,再不可能
// 爆出数倍账单。注意:削减时保留缓存写入的性质(×1.25),不退回普通输入(×1),
// 因为退回普通输入会少扣、违背「宁可多扣不可少扣」的红线。
//
// 该函数只在 estimated_conservative 标记下生效,绝不触碰上游返回的真实 usage。
// observedInput 为可信输入基准:应传请求体数出的真实输入 token(EstimatePromptTokens),
// 而非 usage.PromptTokens 自身——后者在保守估算里已被转记为缓存写入、置 0,拿它当
// 上界会让任何缓存写入都被判越界而误伤。
func ClampEstimatedUsageToObservedInput(c *gin.Context, usage *dto.Usage, observedInput int) {
	if c == nil || usage == nil {
		return
	}
	if common.GetContextKeyString(c, constant.ContextKeyUsageReliability) != "estimated_conservative" {
		return
	}
	if observedInput < 0 {
		observedInput = 0
	}

	// CachedCreationTokens 是缓存写入总量,已包含 5m/1h 拆分(见 text_quota.go 计费口径),
	// 故有效缓存写入取「总量」与「5m+1h 之和」的较大者,避免把拆分项重复计入总和。
	splitCreation := usage.ClaudeCacheCreation5mTokens + usage.ClaudeCacheCreation1hTokens
	use1hSplit := shouldUseCacheCreation1h(c, usage)
	effectiveCreation := usage.PromptTokensDetails.CachedCreationTokens
	if splitCreation > effectiveCreation {
		effectiveCreation = splitCreation
	}
	cacheTotal := usage.PromptTokensDetails.CachedTokens + effectiveCreation
	inputSide := usage.PromptTokens + cacheTotal

	if inputSide <= observedInput || (observedInput == 0 && cacheTotal == 0) {
		return
	}

	common.SysError(fmt.Sprintf(
		"estimated usage exceeds observed input, clamping cache-write down to observed input: input_side=%d observed_input=%d cache_total=%d",
		inputSide, observedInput, cacheTotal,
	))

	// 削减策略:先保住普通输入与缓存读(它们更接近真实观测),把越界部分从缓存写入里扣。
	// 削减后仍保留缓存写入性质(×1.25),不退回普通输入,以免少扣。
	budget := observedInput - usage.PromptTokens - usage.PromptTokensDetails.CachedTokens
	if budget < 0 {
		// 连普通输入+缓存读都已超过观测输入:极端失控,退回按观测输入计的普通输入。
		budget = 0
		usage.PromptTokensDetails.CachedTokens = 0
		if observedInput > 0 {
			usage.PromptTokens = observedInput
		}
	}
	usage.PromptTokensDetails.CachedCreationTokens = budget
	if budget == 0 {
		usage.ClaudeCacheCreation5mTokens = 0
		usage.ClaudeCacheCreation1hTokens = 0
	} else if splitCreation > 0 {
		usage.ClaudeCacheCreation5mTokens = 0
		usage.ClaudeCacheCreation1hTokens = 0
		if use1hSplit {
			usage.ClaudeCacheCreation1hTokens = budget
		} else {
			usage.ClaudeCacheCreation5mTokens = budget
		}
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	common.SetContextKey(c, constant.ContextKeyUsageFallbackReason, "estimated_usage_clamped_to_observed_input")
}
