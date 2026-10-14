package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type accountModelCatalogContextKeyGeili struct{}

// ListModelCatalogCandidatesGeili keeps the original configured-account query
// and model declarations. Only its private conversion context omits best-effort
// runtime presentation, which ExplicitGroupModels does not consume. The marker
// cannot be installed by a service or escape back into its request context.
func (r *accountRepository) ListModelCatalogCandidatesGeili(ctx context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]service.Account, error) {
	return r.ListModelAvailabilityCandidates(context.WithValue(ctx, accountModelCatalogContextKeyGeili{}, true), groupID, platforms, includeGrouped)
}

func isAccountModelCatalogContextGeili(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	catalog, _ := ctx.Value(accountModelCatalogContextKeyGeili{}).(bool)
	return catalog
}
