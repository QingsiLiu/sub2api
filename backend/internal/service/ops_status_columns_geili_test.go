//go:build unit

package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpsStatusColumnsGeiliJSON(t *testing.T) {
	client, upstream := 503, 400
	for _, value := range []any{
		OpsErrorLog{StatusCode: 400, ClientStatusCode: &client, UpstreamStatusCode: &upstream},
		OpsErrorLogDetail{OpsErrorLog: OpsErrorLog{StatusCode: 400, ClientStatusCode: &client, UpstreamStatusCode: &upstream}},
	} {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.Unmarshal(encoded, &body))
		require.EqualValues(t, 400, body["status_code"])
		require.EqualValues(t, 503, body["client_status_code"])
		require.EqualValues(t, 400, body["upstream_status_code"])
	}

	encoded, err := json.Marshal(OpsErrorLogDetail{})
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"client_status_code":null`)
	require.Contains(t, string(encoded), `"upstream_status_code":null`)
}
