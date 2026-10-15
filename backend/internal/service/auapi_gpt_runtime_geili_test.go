package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAUAPIGPTDurableLocalAdmissionReleasesUnsubmittedHold(t *testing.T) {
	s, key, repo, upstream := gptDurableFixture("gpt-image-2")
	p := &OpenAIImagesRequest{Model: "gpt-image-2", Prompt: "cup", N: 1, Size: "1024x1024", Endpoint: openAIImagesGenerationsEndpoint}
	_, _, err := s.SubmitImageRequest(context.Background(), key, nil, []byte(`{"model":"gpt-image-2","prompt":"cup"}`), p, "local-admission")
	require.NoError(t, err)
	task := repo.created[0]
	task.Phase = "submit"
	upstream.err = NewAccountRuntimeAdmissionErrorGeili("balance")
	require.Error(t, s.step(context.Background(), task))
	require.Equal(t, "fail", task.Phase, "the local runtime barrier proves no procurement was accepted")
	require.Equal(t, http.StatusServiceUnavailable, task.HTTPStatus)
}
