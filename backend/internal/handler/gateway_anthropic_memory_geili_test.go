package handler

import (
	"bytes"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAnthropicBodySnapshotGeiliPreservesStrictClientBytes(t *testing.T) {
	key := &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}}
	client := []byte("{ \"model\":\"claude-sonnet-4-6[1m]\", \"max_tokens\":16, \"messages\":[] }\n")
	parsed, err := parseAnthropicGatewayRequest(client, nil, key)
	require.NoError(t, err)
	routed := parsed.Body.Bytes()
	snapshot := snapshotAnthropicRoutedBodyGeili(routed)
	attempt, err := parsed.CloneForBody(routed)
	require.NoError(t, err)

	require.True(t, useStrictAnthropicBodySnapshotGeili(attempt, snapshot, client))
	require.Equal(t, client, attempt.Body.Bytes())
	require.Equal(t, "claude-sonnet-4-6", attempt.Model)
	require.NotSame(t, &client[0], &attempt.Body.Bytes()[0], "strict attempts own their byte copy")
	attempt.Body.Bytes()[0] = ' '
	require.Equal(t, byte('{'), client[0], "mutating a strict attempt must not corrupt the retry source")
}

func TestAnthropicBodySnapshotGeiliRejectsRewrites(t *testing.T) {
	for _, inPlace := range []bool{false, true} {
		t.Run(map[bool]string{false: "replacement", true: "aliased_in_place"}[inPlace], func(t *testing.T) {
			body := []byte(`{"model":"claude-opus-5","messages":[{"role":"user","content":"before"}]}`)
			parsed, err := service.ParseGatewayRequest(service.NewRequestBodyRef(body), service.PlatformAnthropic)
			require.NoError(t, err)
			snapshot := snapshotAnthropicRoutedBodyGeili(body)
			attempt, err := parsed.CloneForBody(body)
			require.NoError(t, err)
			if inPlace {
				copy(body[bytes.Index(body, []byte("before")):], "after!")
			} else {
				require.NoError(t, attempt.ReplaceBody(bytes.ReplaceAll(body, []byte("before"), []byte("after!"))))
			}
			require.False(t, useStrictAnthropicBodySnapshotGeili(attempt, snapshot, body))
		})
	}
}

func TestAnthropicBodySnapshotGeiliRejectsRepairedClientJSON(t *testing.T) {
	key := &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}}
	client := []byte("{\"model\":\"claude-opus-5\",\"messages\":[{\"role\":\"user\",\"content\":\"a\tb\"}]}")
	parsed, err := parseAnthropicGatewayRequest(client, nil, key)
	require.NoError(t, err)
	snapshot := snapshotAnthropicRoutedBodyGeili(parsed.Body.Bytes())
	require.False(t, useStrictAnthropicBodySnapshotGeili(parsed, snapshot, client))
	require.False(t, useStrictAnthropicBodySnapshotGeili(nil, snapshot, client))
}

func TestAnthropicBodySnapshotGeiliLargeBodyDoesNotAllocate(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 20<<20)
	allocs := testing.AllocsPerRun(5, func() {
		snapshot := snapshotAnthropicRoutedBodyGeili(body)
		if !snapshot.matches(body) {
			t.Fatal("unchanged large body must match")
		}
	})
	require.Zero(t, allocs, "routing snapshots must not allocate another large request body")
}

var anthropicBodySnapshotBenchmarkSinkGeili anthropicRoutedBodySnapshotGeili
var anthropicBodyCloneBenchmarkSinkGeili []byte

func BenchmarkAnthropicBodySnapshotGeili20MiB(b *testing.B) {
	body := bytes.Repeat([]byte("x"), 20<<20)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		anthropicBodySnapshotBenchmarkSinkGeili = snapshotAnthropicRoutedBodyGeili(body)
	}
}

func BenchmarkAnthropicBodyCloneBaselineGeili20MiB(b *testing.B) {
	body := bytes.Repeat([]byte("x"), 20<<20)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		anthropicBodyCloneBenchmarkSinkGeili = bytes.Clone(body)
	}
}
