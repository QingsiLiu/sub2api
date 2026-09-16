package service

import (
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	UsagePanelGPT      = "gpt"
	UsagePanelGrok     = "grok"
	UsagePanelClaude   = "claude"
	UsagePanelNational = "national"
	UsagePanelGemini   = "gemini"
)

// UsagePanelOrder is the composite-key fallback order across panels.
var UsagePanelOrder = []string{
	UsagePanelGPT,
	UsagePanelGrok,
	UsagePanelClaude,
	UsagePanelNational,
	UsagePanelGemini,
}

func IsUsagePanel(value string) bool {
	switch strings.TrimSpace(value) {
	case UsagePanelGPT, UsagePanelGrok, UsagePanelClaude, UsagePanelNational, UsagePanelGemini:
		return true
	default:
		return false
	}
}

// NormalizeUsagePanel accepts an empty value (unassigned) or one of the five
// live-routing panels. It never infers a panel from platform or model names.
func NormalizeUsagePanel(value string) (string, error) {
	panel := strings.TrimSpace(value)
	if panel == "" || IsUsagePanel(panel) {
		return panel, nil
	}
	return "", infraerrors.BadRequest("USAGE_PANEL_INVALID", "usage_panel must be gpt, grok, claude, national, gemini, or empty")
}
