package service

import "github.com/QuantumNous/new-api/relaykit/dto"

func ApplyResponsesUsage(dst *dto.Usage, src *dto.Usage) {
	if dst == nil || src == nil {
		return
	}
	if src.InputTokens != 0 {
		dst.PromptTokens = src.InputTokens
		dst.InputTokens = src.InputTokens
	}
	if src.OutputTokens != 0 {
		dst.CompletionTokens = src.OutputTokens
		dst.OutputTokens = src.OutputTokens
	}
	if src.TotalTokens != 0 {
		dst.TotalTokens = src.TotalTokens
	}
	if src.InputTokensDetails != nil {
		inputDetails := *src.InputTokensDetails
		dst.InputTokensDetails = &inputDetails
		dst.PromptTokensDetails = inputDetails
	} else if !isZeroInputTokenDetails(src.PromptTokensDetails) {
		inputDetails := src.PromptTokensDetails
		dst.InputTokensDetails = &inputDetails
		dst.PromptTokensDetails = inputDetails
	}
	outputDetails := src.CompletionTokenDetails
	if src.OutputTokensDetails != nil {
		outputDetails = *src.OutputTokensDetails
	}
	if !isZeroOutputTokenDetails(outputDetails) {
		dst.CompletionTokenDetails = outputDetails
		dst.OutputTokensDetails = &outputDetails
	}
	dst.PromptCacheHitTokens = src.PromptCacheHitTokens
	dst.UsageSemantic = src.UsageSemantic
	dst.UsageSource = src.UsageSource
	if src.BillingUsage != nil {
		dst.BillingUsage = dto.CloneBillingUsage(src.BillingUsage)
	}
	if src.Cost != nil {
		dst.Cost = src.Cost
	}
	if src.ClaudeCacheCreation5mTokens != 0 {
		dst.ClaudeCacheCreation5mTokens = src.ClaudeCacheCreation5mTokens
	}
	if src.ClaudeCacheCreation1hTokens != 0 {
		dst.ClaudeCacheCreation1hTokens = src.ClaudeCacheCreation1hTokens
	}
}

func isZeroInputTokenDetails(details dto.InputTokenDetails) bool {
	return details.CachedTokens == 0 &&
		details.CachedCreationTokens == 0 &&
		details.CacheWriteTokens == 0 &&
		details.TextTokens == 0 &&
		details.AudioTokens == 0 &&
		details.ImageTokens == 0
}

func isZeroOutputTokenDetails(details dto.OutputTokenDetails) bool {
	return details.TextTokens == 0 &&
		details.AudioTokens == 0 &&
		details.ImageTokens == 0 &&
		details.ReasoningTokens == 0
}
