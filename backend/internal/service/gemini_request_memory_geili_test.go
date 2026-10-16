package service

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeminiAIStudioNoToolsGeiliPreservesOriginalBytes(t *testing.T) {
	for _, body := range []string{
		` { "contents": [{"parts":[{"text":"unchanged"}]}] } `,
		`{"contents":[],"tools":[]}`,
		`{"tools":null}`,
		`{"tools":{"googleSearch":{}}}`,
		`{"contents":[{"tools":[{"googleSearch":{}}]}]}`,
		`{"tools":[],"tools":null}`,
		`{"tools":[],"tools":[]}`,
		`{"tools":[{"functionDeclarations":[]}]}`,
		`{"tools":[{"googleSearch":{},"google_search":{}}]}`,
		`{"tools":[{"googleSearch":{}}],"tools":[]}`,
		`{"tools":[{"googleSearch":{}}],`,
		`{"contents":[`,
		`null`,
		`[]`,
	} {
		t.Run(body, func(t *testing.T) {
			raw := []byte(body)
			got := normalizeGeminiRequestForAIStudio(raw)
			require.Equal(t, raw, got)
			require.Same(t, &raw[0], &got[0], "unchanged payloads must keep the existing byte slice")
		})
	}
}

func TestGeminiAIStudioToolsGeiliPreservesNormalizationSemantics(t *testing.T) {
	for _, body := range []string{
		`{"contents":[{"parts":[{"text":"hello"}]}],"tools":[{"googleSearch":{}}]}`,
		`{"tools":[],"tools":[{"googleSearch":{}}]}`,
		`{"\u0074ools":[{"googleSearch":{}}]}`,
		`{"contents":[],"tools":[null,{}, {"googleSearch":{"excludeDomains":["example.org"]}}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			raw := []byte(body)
			before := bytes.Clone(raw)
			got := normalizeGeminiRequestForAIStudio(raw)
			require.Equal(t, before, raw, "normalization must not mutate the caller's bytes")
			var payload map[string]any
			require.NoError(t, json.Unmarshal(got, &payload))
			tools := payload["tools"].([]any)
			search := tools[len(tools)-1].(map[string]any)
			require.Contains(t, search, "google_search")
			require.NotContains(t, search, "googleSearch")
		})
	}
}

func TestGeminiAIStudioNoToolsGeiliLargeBodyDoesNotAllocate(t *testing.T) {
	body := largeGeminiNoToolsBodyGeili()
	allocs := testing.AllocsPerRun(5, func() {
		got := normalizeGeminiRequestForAIStudio(body)
		if &body[0] != &got[0] {
			t.Fatal("no-tools normalization must retain the original body")
		}
	})
	require.Zero(t, allocs, "no-tools normalization must not decode large conversation content")
}

func largeGeminiNoToolsBodyGeili() []byte {
	body := []byte(`{"contents":[{"parts":[{"text":"`)
	body = append(body, bytes.Repeat([]byte("x"), 20<<20)...)
	return append(body, []byte(`"}]}]}`)...)
}

var geminiNoToolsBenchmarkSinkGeili []byte
var geminiNoToolsDecodedBenchmarkSinkGeili map[string]any

func BenchmarkGeminiAIStudioNoToolsGeili20MiB(b *testing.B) {
	body := largeGeminiNoToolsBodyGeili()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		geminiNoToolsBenchmarkSinkGeili = normalizeGeminiRequestForAIStudio(body)
	}
}

func BenchmarkGeminiAIStudioNoToolsDecodeBaselineGeili20MiB(b *testing.B) {
	body := largeGeminiNoToolsBodyGeili()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			b.Fatal(err)
		}
		geminiNoToolsDecodedBenchmarkSinkGeili = payload
	}
}
