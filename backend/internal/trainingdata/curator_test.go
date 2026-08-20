package trainingdata

import (
	"bytes"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func TestBuildTrainingSampleExcludesSystemAndRedactsPrivacy(t *testing.T) {
	manifest := Manifest{
		CaptureID: "capture-1", Protocol: "openai_chat_completions", ClientModel: "model-a",
		UserSubjectRef: "subject-a", RightsID: "rights-a", CaptureComplete: true,
		ClientResponse: HeaderSnapshot{Status: 200}, StartedAt: time.Now(),
		Attempts: []AttemptManifest{{Model: "upstream-a", HTTPStatus: 200, Complete: true}},
	}
	sample, err := BuildTrainingSample(manifest,
		[]byte(`{"model":"model-a","messages":[{"role":"system","content":"private policy"},{"role":"user","content":"email me at user@example.com"}]}`),
		[]byte(`{"choices":[{"message":{"role":"assistant","content":"done"}}]}`), "chat")
	require.NoError(t, err)
	require.Len(t, sample.Messages, 2)
	require.Equal(t, "user", sample.Messages[0].Role)
	require.Equal(t, "email me at <EMAIL>", sample.Messages[0].Content)
	require.Equal(t, "assistant", sample.Messages[1].Role)
	require.Equal(t, "upstream-a", sample.AssistantSourceModel)
	require.Contains(t, sample.QualityFlags, "privacy_redacted")
}

func TestExtractSSEText(t *testing.T) {
	body := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n" +
		"data: [DONE]\n\n")
	require.Equal(t, "hello", extractResponseText("openai_chat_completions", body))
}

func TestResponsesStringInput(t *testing.T) {
	messages, err := extractRequestMessages("openai_responses", []byte(`{"input":"hello"}`))
	require.NoError(t, err)
	require.Equal(t, []TrainingMessage{{Role: "user", Content: "hello"}}, messages)
}

func TestExtractGeminiSSEText(t *testing.T) {
	body := []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hel\"}]}}]}\n\n" +
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"lo\"}]}}]}\n\n")
	require.Equal(t, "hello", extractResponseText("gemini", body))
}

func TestFirstUpstreamModelUsesLastSuccessfulAttempt(t *testing.T) {
	manifest := Manifest{Attempts: []AttemptManifest{
		{Model: "model-a", HTTPStatus: 200, Complete: true},
		{Model: "model-b", Error: "retry", Complete: false},
		{Model: "model-c", HTTPStatus: 200, Complete: true},
	}}
	require.Equal(t, "model-c", firstUpstreamModel(manifest))
}

func TestBuildTrainingSampleRequiresUserTurn(t *testing.T) {
	manifest := Manifest{
		CaptureID: "capture-assistant-only", Protocol: "openai_chat_completions",
		UserSubjectRef: "subject-a", CaptureComplete: true, ClientResponse: HeaderSnapshot{Status: 200},
	}
	_, err := BuildTrainingSample(manifest,
		[]byte(`{"messages":[{"role":"assistant","content":"history"}]}`),
		[]byte(`{"choices":[{"message":{"role":"assistant","content":"answer"}}]}`), "chat")
	require.ErrorContains(t, err, "no usable user request")
}

func TestRedactTrainingTextRemovesPrivateKey(t *testing.T) {
	cleaned, flags := redactTrainingText("-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----")
	require.Equal(t, "<REDACTED_PRIVATE_KEY>", cleaned)
	require.Contains(t, flags, "private_key")
}

func TestDecodeCaptureBodyZstd(t *testing.T) {
	var buffer bytes.Buffer
	encoder, err := zstd.NewWriter(&buffer)
	require.NoError(t, err)
	_, err = encoder.Write([]byte("Hello world"))
	require.NoError(t, err)
	require.NoError(t, encoder.Close())
	compressed := buffer.Bytes()
	decoded, err := DecodeCaptureBody(bytes.NewReader(compressed), "body.zst")
	require.NoError(t, err)
	require.Equal(t, "Hello world", string(decoded))
}
