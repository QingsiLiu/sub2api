package service

import "github.com/gin-gonic/gin"

// A failed attempt that delivered no semantic content is replayable and must
// not contribute a second receipt. Once content was delivered, keep genuine
// supplier metering for the existing handler's single settlement. Cyber-policy
// refusals retain their established separate accounting contract.
func retainOpenAIResponsesPartialUsageGeili(c *gin.Context, result *openaiStreamingResult) bool {
	if result == nil || geiliPreOutput(c) || GetOpsCyberPolicy(c) != nil {
		return false
	}
	if result.imageCount > 0 || result.searchCount > 0 {
		return true
	}
	return openAIUsageHasTokens(result.usage)
}
