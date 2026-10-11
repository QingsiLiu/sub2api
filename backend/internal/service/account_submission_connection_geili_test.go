package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAccountSubmissionConnectionValidation(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"preset", "direct"} {
		require.NoError(t, ValidateSubmissionConnection(ctx, &SubmissionConnectionInput{Mode: mode}))
	}
	for _, host := range []string{"127.0.0.1", "::1", "169.254.169.254", "10.0.0.1", "http://8.8.8.8", "user@8.8.8.8"} {
		require.ErrorIs(t, ValidateSubmissionConnection(ctx, &SubmissionConnectionInput{Mode: "custom", Proxy: &SubmissionProxyDraft{Name: "p", Protocol: "http", Host: host, Port: 8080}}), ErrSubmissionInvalid)
	}
	for _, n := range []int{0, -1, 1001} {
		require.ErrorIs(t, ValidateSubmissionOptions(ctx, &SubmissionOptions{Concurrency: &n}), ErrSubmissionInvalid)
	}
	for _, name := range []string{"", "a\nb"} {
		require.ErrorIs(t, ValidateSubmissionOptions(ctx, &SubmissionOptions{Name: &name}), ErrSubmissionInvalid)
	}
	require.ErrorIs(t, ValidateSubmissionConnection(ctx, &SubmissionConnectionInput{Mode: "preset", Proxy: &SubmissionProxyDraft{}}), ErrSubmissionInvalid)
}
