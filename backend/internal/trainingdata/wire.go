package trainingdata

import (
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/wire"
)

// ProviderSet keeps the capture manager optional and injectable. The manager
// never owns the application DB; the main migration runner remains the single
// schema authority.
var ProviderSet = wire.NewSet(ProvideManager)

func ProvideManager(db *sql.DB, cfg *config.Config) *Manager {
	return NewManager(db, cfg)
}
