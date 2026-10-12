package service

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// normalizeAnthropicThinkingEffortGeili repairs clients that put effort inside
// adaptive thinking. Anthropic accepts effort only in output_config. Resolve
// account model mappings first so third-party thinking protocols stay intact.
// An explicit output_config.effort wins; malformed values remain upstream errors.
func normalizeAnthropicThinkingEffortGeili(body []byte, upstreamModel string) ([]byte, bool) {
	if !strings.HasPrefix(strings.ToLower(upstreamModel), "claude-") ||
		gjson.GetBytes(body, "thinking.type").String() != "adaptive" {
		return body, false
	}
	effort := gjson.GetBytes(body, "thinking.effort")
	if effort.Type != gjson.String {
		return body, false
	}
	output := gjson.GetBytes(body, "output_config")
	if output.Exists() && !output.IsObject() {
		return body, false
	}
	out := body
	var err error
	if !output.Get("effort").Exists() {
		out, err = sjson.SetRawBytes(out, "output_config.effort", []byte(effort.Raw))
		if err != nil {
			return body, false
		}
	}
	out, err = sjson.DeleteBytes(out, "thinking.effort")
	if err != nil {
		return body, false
	}
	return out, true
}
