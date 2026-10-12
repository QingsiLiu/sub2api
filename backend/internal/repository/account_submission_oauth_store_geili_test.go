package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestAccountSubmissionOAuthStoreGeiliOwnershipAndSingleUse(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	primary := NewSubmissionOAuthSessionStoreGeili(client)
	replica := NewSubmissionOAuthSessionStoreGeili(client)
	ctx := context.Background()
	require.NoError(t, primary.Save(ctx, "session", "invite:platform:flow", []byte("pkce-state"), time.Minute))
	_, err := replica.Take(ctx, "session", "foreign:platform:flow")
	require.Error(t, err)
	require.True(t, mr.Exists(submissionOAuthSessionKeyGeili("session")))
	state, err := replica.Take(ctx, "session", "invite:platform:flow")
	require.NoError(t, err)
	require.Equal(t, []byte("pkce-state"), state)
	_, err = primary.Take(ctx, "session", "invite:platform:flow")
	require.Error(t, err)
	require.NotContains(t, submissionOAuthSessionKeyGeili("session"), "session")
	require.NoError(t, primary.Save(ctx, "expired", "owner", []byte("state"), time.Minute))
	mr.FastForward(time.Minute)
	_, err = primary.Take(ctx, "expired", "owner")
	require.Error(t, err)
	require.Nil(t, NewSubmissionOAuthSessionStoreGeili(nil))
}
