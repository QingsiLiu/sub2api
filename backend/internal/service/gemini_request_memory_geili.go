package service

import (
	"unsafe"

	"github.com/tidwall/gjson"
)

// Inspect every top-level tools key, including escaped and duplicate keys. A
// nonempty tools array keeps the existing encoding/json path and its last-key
// wins behavior. Other payloads cannot need googleSearch normalization.
func geminiAIStudioToolsMayNeedNormalizationGeili(body []byte) bool {
	// This view never escapes the call or mutates body. ParseBytes would copy the
	// entire payload, defeating the no-tools path for large conversation history.
	jsonStr := unsafe.String(unsafe.SliceData(body), len(body))
	needsDecode := false
	gjson.Parse(jsonStr).ForEach(func(key, value gjson.Result) bool {
		if key.Type != gjson.String || key.String() != "tools" || !value.IsArray() {
			return true
		}
		value.ForEach(func(_, _ gjson.Result) bool {
			needsDecode = true
			return false
		})
		return !needsDecode
	})
	return needsDecode
}
