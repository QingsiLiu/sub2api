package repository

import (
	"context"
	"database/sql"
	"sync"
)

// These are query plans, never query results. Every use still executes a fresh
// PostgreSQL read. database/sql maintains each statement on the pool's physical
// connections; DB.Close closes their driver statements. No separate goroutine,
// process-global registry or repository Close/wire lifecycle is needed.
type accountRuntimePreparedReadsGeili struct {
	db        *sql.DB
	admission accountRuntimeStatementGeili
	state     accountRuntimeStatementGeili
}

type accountRuntimeStatementGeili struct {
	mu        sync.Mutex
	stmt      *sql.Stmt
	preparing chan struct{}
}

func newAccountRuntimePreparedReadsGeili(db *sql.DB) *accountRuntimePreparedReadsGeili {
	if db == nil {
		return nil
	}
	return &accountRuntimePreparedReadsGeili{db: db}
}

func (s *accountRuntimeStatementGeili) statement(ctx context.Context, db *sql.DB, query string) (*sql.Stmt, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		if s.stmt != nil {
			stmt := s.stmt
			s.mu.Unlock()
			return stmt, nil
		}
		if s.preparing != nil {
			ready := s.preparing
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ready:
				continue
			}
		}
		s.preparing = make(chan struct{})
		s.mu.Unlock()
		stmt, err := db.PrepareContext(ctx, query)
		s.mu.Lock()
		if err == nil {
			s.stmt = stmt
		}
		close(s.preparing)
		s.preparing = nil
		s.mu.Unlock()
		// A failed prepare denies this read; later calls can prepare again.
		return stmt, err
	}
}

func (r *accountRepository) accountRuntimeReadRowGeili(ctx context.Context, query string, args ...any) accountRuntimeRowGeili {
	row := accountRuntimeQueryRowGeili(ctx, r.sql, query, args...)
	reads := r.runtimeReadsGeili
	db, ok := r.sql.(*sql.DB)
	if reads == nil || !ok || db != reads.db {
		// withAccountRuntimeGeili copies the repository but sets sql to the Ent
		// transaction client. Its snapshot/locks must stay in that transaction.
		return row
	}
	var slot *accountRuntimeStatementGeili
	switch query {
	case accountRuntimeAdmissionSQLGeili:
		slot = &reads.admission
	case accountRuntimeGetSQLGeili:
		slot = &reads.state
	default:
		return row
	}
	row.stmt, row.err = slot.statement(ctx, db, query)
	return row
}
