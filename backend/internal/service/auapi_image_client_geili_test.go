package service

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBuildAUAPIImagePayloadMapsOpenAIFields(t *testing.T) {
	payload, tier, err := buildAUAPIImagePayload([]byte(`{"model":"gpt-image-2","prompt":"a cat","n":2,"size":"1536x1024","quality":"high","output_format":"webp"}`))
	require.NoError(t, err)
	require.Equal(t, "2K", tier)
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"gpt-image-2","input":{"prompt":"a cat"},"parameters":{"n":2,"size":"1536x1024","resolution":"2k","quality":"high","output_format":"webp"}}`, string(raw))
}

func TestBuildAUAPIImagePayloadRejectsUnsupportedReferenceAndSize(t *testing.T) {
	_, _, err := buildAUAPIImagePayload([]byte(`{"model":"gpt-image-2","prompt":"cat","image_url":"https://example.com/cat.png"}`))
	require.Error(t, err)
	_, _, err = buildAUAPIImagePayload([]byte(`{"model":"gpt-image-2","prompt":"cat","size":"1024x576"}`))
	require.Error(t, err)
}
