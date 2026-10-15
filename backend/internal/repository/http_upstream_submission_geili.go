package repository

import (
	"context"
	"database/sql"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"net/http"
	"net/url"
	"time"
)

type submissionPolicyCacheEntryGeili struct {
	external bool
	expires  time.Time
}

// ProvideSubmissionHTTPUpstreamGeili preserves the official connection pool and
// applies the invitation policy before selecting a client, including TLS clients.
func ProvideSubmissionHTTPUpstreamGeili(cfg *config.Config, db *sql.DB) service.HTTPUpstream {
	s := NewHTTPUpstream(cfg).(*httpUpstreamService)
	s.submissionDB = db
	return s
}
func (s *httpUpstreamService) submissionProxyPolicyGeili(req *http.Request, raw string, id int64) (string, error) {
	if req == nil || req.URL == nil {
		return raw, nil
	}
	external := service.IsSubmissionPublicEgressGeili(req.Context())
	if !external && s.submissionDB != nil && id > 0 {
		cached, ok := s.submissionPolicyCache.Load(id)
		if entry, valid := cached.(submissionPolicyCacheEntryGeili); ok && valid && time.Now().Before(entry.expires) {
			external = entry.external
		} else {
			ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
			// Supplier-owned accounts carry the same public-egress marker as external intake.
			err := s.submissionDB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts a WHERE a.id=$1 AND a.extra->>'external_submission_form_geili'='true' AND (EXISTS(SELECT 1 FROM account_submission_invites_geili i WHERE i.account_id=a.id) OR EXISTS(SELECT 1 FROM account_supplier_owners_geili o WHERE o.account_id=a.id)))`, id).Scan(&external)
			cancel()
			if err != nil {
				return "", service.ErrSubmissionUnavailable
			}
			if s.submissionPolicyEntries.Add(1) > 10000 {
				s.submissionPolicyCache.Clear()
				s.submissionPolicyEntries.Store(0)
			}
			s.submissionPolicyCache.Store(id, submissionPolicyCacheEntryGeili{external: external, expires: time.Now().Add(time.Minute)})
		}
	}
	if !external {
		return raw, nil
	}
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()
	if proxyutil.ValidatePublicProxyHost(ctx, req.URL.Hostname()) != nil {
		return "", service.ErrSubmissionInvalid
	}
	if raw == "" || raw == proxyutil.PublicDirectURLGeili {
		return proxyutil.PublicDirectURLGeili, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", service.ErrSubmissionInvalid
	}
	// Full-form public callers cannot turn an administrative private proxy into a
	// tunnel to server-local resources. The policy marker remains part of pool keys.
	u.Fragment = proxyutil.PublicProxyMarker
	return u.String(), nil
}
