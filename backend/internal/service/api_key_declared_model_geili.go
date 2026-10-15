package service

import (
	"context"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ExplicitGroupModels always includes these exact declarations, independently
// of account availability. Routing one declared model does not require loading
// the entire catalog and its account/proxy/group projections. Authorization,
// endpoint and pricing checks remain in the caller; dispatch still verifies
// fresh account state. Aliases, wildcard and case-equivalent matches retain
// catalog lookup, and model listing itself is unchanged.
func (r *CompositeRouteResolver) explicitRequestedModelSupportedGeili(ctx context.Context, group *Group, model string) (bool, error) {
	if r.accountRepo == nil {
		return false, infraerrors.ServiceUnavailable("MODEL_CATALOG_UNAVAILABLE", "account model catalog is unavailable")
	}
	if group.ModelAllowlist.Enabled && model != "" && !strings.Contains(model, "*") {
		for _, entry := range group.ModelAllowlist.Models {
			if strings.TrimSpace(entry) == model && group.ModelAllowlist.Allows(model) {
				return true, nil
			}
		}
	}
	models, err := r.ExplicitGroupModels(ctx, group)
	if err != nil {
		return false, err
	}
	for _, known := range models {
		if known == model {
			return true, nil
		}
	}
	return false, nil
}
