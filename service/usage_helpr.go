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
// 的兜底计费。历史实现曾把 CachedCreationTokens 估成 PromptTokens × cache_control
// 断点数,这在物理上不成立(断点数不是缓存倍率,缓存写入也不可能超过总输入),
// 在客户端中途断开(client_gone)且上游已回过真实 input_tokens 的场景下会把真实
// 输入放大数倍、再按最贵的缓存写入费率(×1.25)计费,造成严重多扣。
//
// 现策略:usage 缺失时不再伪造任何缓存写入/缓存读,直接按已知输入以普通输入费率
// (×1)计费。cache_control 断点数只作为日志线索保留,永不进入计费。这样断流场景
// 不会爆扣;代价是上游若真写了缓存,这部分会按普通输入而非缓存写入计,可能略微少扣
// ——这是在「usage 不可信」时刻意偏向用户的取舍。
//
// 兜底标记(usage_fallback / reason / reliability)继续写入,便于日志区分与审计。
// 计费天花板由 clampEstimatedUsageToObservedInput 兜底,防止任何估算路径失控。
func ApplyLocalCountCacheControlFallback(c *gin.Context, usage *dto.Usage) {
	if c == nil || usage == nil {
		return
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens) ||
		!common.GetContextKeyBool(c, constant.ContextKeyRequestHasCacheControl) {
		return
	}
	if usage.PromptTokens <= 0 ||
		usage.PromptTokensDetails.CachedCreationTokens > 0 ||
		usage.PromptTokensDetails.CachedTokens > 0 ||
		usage.ClaudeCacheCreation5mTokens > 0 ||
		usage.ClaudeCacheCreation1hTokens > 0 {
		return
	}

	// 不伪造缓存写入:输入按普通输入率(×1)计费。断点数仅记入日志作线索。
	common.SetContextKey(c, constant.ContextKeyUsageFallback, "local_cache_control_estimate")
	common.SetContextKey(c, constant.ContextKeyUsageFallbackReason, "upstream_usage_missing_with_cache_control")
	common.SetContextKey(c, constant.ContextKeyUsageReliability, "estimated_conservative")
}

func ValidUsage(usage *dto.Usage) bool {
	return usage != nil && (usage.PromptTokens != 0 || usage.CompletionTokens != 0)
}

// ClampEstimatedUsageToObservedInput 是计费路径的第二道防线(防御纵深):当且仅当
// 本次 usage 被标记为本地估算(usage_reliability=estimated_conservative)时,强制
// 一条物理不变量——「估算出的输入侧计费 token(普通输入 + 缓存读 + 各类缓存写入)
// 之和,不得超过我们观测到的真实输入 token」。
//
// 缓存读/缓存写入在物理上都只是「输入的一部分」,其总和永不可能超过总输入。若超过,
// 必然是某段估算逻辑出错(历史上的 PromptTokens × cache_control_count 就是典型)。
// 这里一旦检测到越界,就丢弃全部伪造的缓存字段、把全部输入退回普通输入(×1),
// 并打 warn 日志,保证任何估算路径失控时最坏也只按「真实输入 × 普通费率」计费,
// 再不可能爆出数倍账单。
//
// 该函数只在 estimated_conservative 标记下生效,绝不触碰上游返回的真实 usage。
// observedInput 为可信输入基准(通常取上游 message_start 的 input_tokens,缺失时
// 取本地 PromptTokens)。
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

	cacheTotal := usage.PromptTokensDetails.CachedTokens +
		usage.PromptTokensDetails.CachedCreationTokens +
		usage.ClaudeCacheCreation5mTokens +
		usage.ClaudeCacheCreation1hTokens
	inputSide := usage.PromptTokens + cacheTotal

	if inputSide <= observedInput || observedInput == 0 && cacheTotal == 0 {
		return
	}

	common.SysError(fmt.Sprintf(
		"estimated usage exceeds observed input, clamping to plain input rate: input_side=%d observed_input=%d cache_total=%d",
		inputSide, observedInput, cacheTotal,
	))

	// 丢弃伪造的缓存字段,全部退回普通输入(×1)。
	usage.PromptTokensDetails.CachedTokens = 0
	usage.PromptTokensDetails.CachedCreationTokens = 0
	usage.ClaudeCacheCreation5mTokens = 0
	usage.ClaudeCacheCreation1hTokens = 0
	if observedInput > 0 {
		usage.PromptTokens = observedInput
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	common.SetContextKey(c, constant.ContextKeyUsageFallbackReason, "estimated_usage_clamped_to_observed_input")
}
