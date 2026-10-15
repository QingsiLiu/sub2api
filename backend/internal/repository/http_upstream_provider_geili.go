package repository

import (
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ProvideHTTPUpstreamGeili retains invitation egress rules without adding an
// account health or supplier balance query to each upstream dispatch.
func ProvideHTTPUpstreamGeili(cfg *config.Config, accounts service.AccountRepository) service.HTTPUpstream {
	s := NewHTTPUpstream(cfg).(*httpUpstreamService)
	if repository, ok := accounts.(*accountRepository); ok {
		if db, ok := repository.sql.(*sql.DB); ok {
			s.submissionDB = db
		}
	}
	return s
}
