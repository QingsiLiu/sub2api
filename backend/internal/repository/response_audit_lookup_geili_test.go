package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestResponseAuditLookupGeiliIgnoresUnlinkedAndDuplicateIdentities(t *testing.T) {
	invalid := []service.ResponseAuditUsageKey{{APIKeyID: 1}, {RequestID: "request"}, {APIKeyID: -1, RequestID: "request"}}
	// No database is needed for identities that cannot link a financial row.
	got, err := (&responseAuditRepository{}).Lookup(context.Background(), invalid)
	require.NoError(t, err)
	require.Empty(t, got)
	key := service.ResponseAuditUsageKey{APIKeyID: 1, RequestID: " request identity "}
	q, args := responseAuditLookupQueryGeili(append(invalid, key, key))
	require.NotEmpty(t, q)
	require.Equal(t, []any{int64(1), " request identity "}, args)
}
