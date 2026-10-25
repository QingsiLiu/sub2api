package repository

import (
	"database/sql"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/lib/pq"
)

// Provenance reads and best-effort capture must not cancel or occupy the
// authentication/routing/settlement pool. The admission guard also bounds
// retries when short metadata deadlines cause lib/pq to retire connections.
func newEncryptedRoutePoolGeili(cfg *config.Config) (*sql.DB, error) {
	if cfg == nil {
		return nil, fmt.Errorf("encrypted provenance database configuration unavailable")
	}
	connector, err := pq.NewConnector(cfg.Database.DSNWithTimezone(cfg.Timezone) + " application_name=geili_encrypted_provenance connect_timeout=2")
	if err != nil {
		return nil, fmt.Errorf("initialize encrypted provenance database connector: %w", err)
	}
	db := sql.OpenDB(newServerTimingConnector(connector))
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(defaultConnMaxLifetime)
	db.SetConnMaxIdleTime(defaultConnMaxIdleTime)
	return db, nil
}

// The Redis view shares its parent's lifecycle; only this owned SQL pool closes.
func (c *gatewayCache) CloseEncryptedRouteCacheGeili() error {
	if c == nil || !c.encryptedRouteDBOwned || c.encryptedRouteDB == nil {
		return nil
	}
	return c.encryptedRouteDB.Close()
}

// Release capacity checks must count this pool in addition to the main pool.
func (c *gatewayCache) EncryptedRoutePoolStatsGeili() sql.DBStats {
	if c == nil || !c.encryptedRouteDBOwned || c.encryptedRouteDB == nil {
		return sql.DBStats{}
	}
	return c.encryptedRouteDB.Stats()
}
