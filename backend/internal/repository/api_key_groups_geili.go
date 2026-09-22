package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Hydrate configured groups outside the authentication hot path, preserving route order.
func (r *apiKeyRepository) hydrateCompositeGroupList(ctx context.Context, keys []service.APIKey) error {
	refs := make([]*service.APIKey, len(keys))
	for i := range keys {
		refs[i] = &keys[i]
	}
	return r.hydrateCompositeGroups(ctx, refs)
}

func (r *apiKeyRepository) hydrateCompositeGroups(ctx context.Context, keys []*service.APIKey) error {
	ids := make([]int64, 0)
	seen := make(map[int64]struct{})
	for _, key := range keys {
		if key == nil || !key.UsesGroupListRouting() {
			continue
		}
		for _, id := range key.GroupIDs {
			if id > 0 {
				if _, ok := seen[id]; !ok {
					seen[id] = struct{}{}
					ids = append(ids, id)
				}
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	groups, err := r.client.Group.Query().Where(group.IDIn(ids...)).All(ctx)
	if err != nil {
		return err
	}
	byID := make(map[int64]*service.Group, len(groups))
	for _, entity := range groups {
		byID[entity.ID] = groupEntityToService(entity)
	}
	for _, key := range keys {
		if key == nil || !key.UsesGroupListRouting() {
			continue
		}
		key.Groups = make([]*service.Group, 0, len(key.GroupIDs))
		for _, id := range key.GroupIDs {
			if group := byID[id]; group != nil {
				key.Groups = append(key.Groups, group)
			}
		}
	}
	return nil
}
