package subscription

// MigrationPolicy documents the compatibility behavior used by the SQL
// migration: legacy subscriptions retain their primary group and gain only
// explicitly configured additional entitlements.
type MigrationPolicy struct {
	PrimaryGroupID    int64
	AdditionalGroupID int64
}
