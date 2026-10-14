package service

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAccountProbeSnapshotGeiliBindsConfigurationAndRuntimeVersion(t *testing.T) {
	id := int64(9)
	account := &Account{ID: 17, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusError, Credentials: map[string]any{"api_key": "private-key", "model_mapping": map[string]any{"a": "b"}}, ProxyID: &id, Proxy: &Proxy{ID: 9, Host: "example.test", Port: 80}, GroupIDs: []int64{3, 2}, UpdatedAt: time.Now(), RuntimeState: &AccountRuntimeStateGeili{Version: 4}}
	baseline := MakeAccountProbeSnapshotGeili(account)
	require.NotContains(t, baseline.Fingerprint, "private-key")
	account.GroupIDs = []int64{2, 3}
	require.Equal(t, baseline, MakeAccountProbeSnapshotGeili(account))
	account.RuntimeState.Version++
	require.NotEqual(t, baseline, MakeAccountProbeSnapshotGeili(account))
	account.RuntimeState.Version--
	account.Schedulable = true
	require.NotEqual(t, baseline, MakeAccountProbeSnapshotGeili(account))
	account.Schedulable = false
	account.Proxy.Host = "changed.test"
	require.NotEqual(t, baseline, MakeAccountProbeSnapshotGeili(account))
	account.Proxy.Host = "example.test"
	account.Credentials["api_key"] = "new-key"
	require.NotEqual(t, baseline, MakeAccountProbeSnapshotGeili(account))
}
