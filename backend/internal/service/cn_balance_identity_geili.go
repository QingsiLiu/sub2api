package service

import "time"

// Observational timestamp writes must not break same-credential coalescing.
// Credential, proxy and group changes must never share recharge evidence.
func accountBalanceIdentityGeili(account *Account) string {
	if account == nil {
		return ""
	}
	clone := *account
	clone.UpdatedAt = time.Time{}
	return MakeAccountProbeSnapshotGeili(&clone).Fingerprint
}
