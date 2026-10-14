package repository

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type accountRuntimeAdmissionReaderGeili interface {
	CheckAccountRuntimeAdmissionGeili(context.Context, int64, string) (*service.AccountRuntimeAdmissionGeili, error)
}

// The production provider retains invitation egress rules and adds the final
// text generation admission and native paid-operation debt checks. This does
// not make native operations eligible for automatic recovery/probes.
// NewHTTPUpstream remains the original constructor
// for transport tests and clients with no account runtime dependency.
func ProvideHTTPUpstreamGeili(cfg *config.Config, accounts service.AccountRepository, cache service.TempUnschedCache) service.HTTPUpstream {
	s := NewHTTPUpstream(cfg).(*httpUpstreamService)
	s.runtimeAdmissionRequiredGeili = true
	s.runtimeAdmissionRepoGeili, _ = accounts.(accountRuntimeAdmissionReaderGeili)
	s.runtimeBalanceLatchGeili, _ = cache.(service.AccountBalanceLatchCacheGeili)
	if repository, ok := accounts.(*accountRepository); ok {
		if db, ok := repository.sql.(*sql.DB); ok {
			s.submissionDB = db
		}
	}
	return s
}

// RequiresFinalAccountRuntimeAdmissionGeili describes the provider's mandatory
// final barrier, not dependency health. Missing dependencies still fail closed
// in Do/DoWithTLS; this capability never verifies or caches account state.
func (s *httpUpstreamService) RequiresFinalAccountRuntimeAdmissionGeili() bool {
	return s != nil && s.runtimeAdmissionRequiredGeili
}

// Inspect only the method and endpoint. Never consume or copy a potentially
// large request body to rediscover a model already known to the forwarder.
func isGenerationRequestRuntimeGeili(req *http.Request) bool {
	if req == nil || req.URL == nil || req.Method != http.MethodPost {
		return false
	}
	path := strings.TrimRight(req.URL.Path, "/")
	for _, suffix := range []string{"/messages", "/responses", "/responses/compact", "/chat/completions", ":generateContent", ":streamGenerateContent"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	// Vertex's Anthropic Messages transport uses publisher rawPredict actions.
	if strings.Contains(path, "/publishers/anthropic/models/") && (strings.HasSuffix(path, ":rawPredict") || strings.HasSuffix(path, ":streamRawPredict")) {
		return true
	}
	// Bedrock's Anthropic Messages transport uses invoke rather than /messages.
	return strings.Contains(path, "/model/") && (strings.HasSuffix(path, "/invoke") || strings.HasSuffix(path, "/invoke-with-response-stream"))
}

// Native paid operations only require the mandatory debt barrier. Other stopped
// states still permit an administrator's explicit media/cache test. Polling,
// content retrieval, cache PATCH/delete, billing and OAuth metadata remain
// unaffected. The task suffixes are vendor paid submissions, not task polling.
func isNativePaidBalanceRequestRuntimeGeili(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	path := strings.TrimRight(req.URL.Path, "/")
	if req.Method == http.MethodPost {
		for _, suffix := range []string{"/images/generations", "/images/edits", "/videos/generations", "/videos/edits", "/videos/extensions", "/embeddings", "/cachedContents", "/images/tasks", "/videos/tasks", "/contents/generations/tasks"} {
			if strings.HasSuffix(path, suffix) {
				return true
			}
		}
	}
	return false
}

// CheckNativeBalanceBeforeDispatchGeili is also used by handled native plugin
// branches that dispatch without central Do. It verifies only native paid
// operations, so metadata and text plugin requests do not add database reads.
func (s *httpUpstreamService) CheckNativeBalanceBeforeDispatchGeili(req *http.Request, accountID int64) error {
	if !isNativePaidBalanceRequestRuntimeGeili(req) {
		return nil
	}
	return s.checkAccountRuntimeGenerationGeili(req, accountID)
}

// The existing transport hook name is retained. Full generation eligibility
// and native balance-only eligibility deliberately remain separate policies.
func (s *httpUpstreamService) checkAccountRuntimeGenerationGeili(req *http.Request, accountID int64) error {
	if s == nil || (!s.runtimeAdmissionRequiredGeili && s.runtimeAdmissionRepoGeili == nil) {
		return nil
	}
	balanceOnly := isNativePaidBalanceRequestRuntimeGeili(req)
	if !balanceOnly && !isGenerationRequestRuntimeGeili(req) {
		return nil
	}
	if !balanceOnly && service.IsAccountRecoveryProbeGeili(req.Context()) {
		return nil
	}
	if s.runtimeAdmissionRepoGeili == nil || accountID <= 0 {
		return service.NewAccountRuntimeAdmissionErrorGeili("verification_unavailable")
	}
	ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
	defer cancel()
	if s.runtimeBalanceLatchGeili != nil {
		token, err := s.runtimeBalanceLatchGeili.GetAccountBalanceLatchGeili(ctx, accountID)
		if err != nil {
			return service.NewAccountRuntimeAdmissionErrorGeili("verification_unavailable")
		}
		if token != "" {
			return service.NewAccountRuntimeAdmissionErrorGeili("balance")
		}
	} else if s.runtimeAdmissionRequiredGeili {
		return service.NewAccountRuntimeAdmissionErrorGeili("verification_unavailable")
	}
	verdict, err := s.runtimeAdmissionRepoGeili.CheckAccountRuntimeAdmissionGeili(ctx, accountID, service.AccountRuntimeForwardModelGeili(req.Context()))
	if err != nil || verdict == nil {
		return service.NewAccountRuntimeAdmissionErrorGeili("verification_unavailable")
	}
	if s.runtimeBalanceLatchGeili != nil && verdict.CredentialAccountID > 0 && verdict.CredentialAccountID != accountID {
		token, err := s.runtimeBalanceLatchGeili.GetAccountBalanceLatchGeili(ctx, verdict.CredentialAccountID)
		if err != nil {
			return service.NewAccountRuntimeAdmissionErrorGeili("verification_unavailable")
		}
		if token != "" {
			return service.NewAccountRuntimeAdmissionErrorGeili("balance")
		}
	}
	if verdict.BalanceBlocked {
		return service.NewAccountRuntimeAdmissionErrorGeili("balance")
	}
	if !balanceOnly && !verdict.Allowed {
		return service.NewAccountRuntimeAdmissionErrorGeili(verdict.Reason)
	}
	return nil
}
