package trainingdata

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestObjectStoreRejectsEmptyDestructiveTargets(t *testing.T) {
	store := &S3ObjectStore{}
	require.ErrorContains(t, store.Delete(context.Background(), ""), "empty")
	require.ErrorContains(t, store.DeletePrefix(context.Background(), ""), "empty")
	require.ErrorContains(t, store.DeletePrefix(context.Background(), "/"), "empty")
}
