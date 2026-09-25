package service

import (
	"context"
	"encoding/json"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"sort"
)

type LegacyRollout struct {
	Enabled bool    `json:"enabled"`
	UserIDs []int64 `json:"user_ids"`
}

func (s *SubscriptionService) GetLegacyRollout(ctx context.Context) (*LegacyRollout, error) {
	out := &LegacyRollout{UserIDs: []int64{}}
	row, err := s.entClient.Setting.Query().Where(setting.KeyEQ(SettingLegacySubscriptionManagement)).Only(ctx)
	if dbent.IsNotFound(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if row.Value == "true" {
		out.Enabled = true
		return out, nil
	}
	if row.Value == "false" {
		return out, nil
	}
	if err = json.Unmarshal([]byte(row.Value), &out.UserIDs); err != nil {
		return nil, err
	}
	out.Enabled = len(out.UserIDs) > 0
	return out, nil
}
func (s *SubscriptionService) SetLegacyRollout(ctx context.Context, in LegacyRollout) (*LegacyRollout, error) {
	if len(in.UserIDs) > 1000 {
		return nil, geilisub.ErrSelection
	}
	sort.Slice(in.UserIDs, func(i, j int) bool { return in.UserIDs[i] < in.UserIDs[j] })
	for i, id := range in.UserIDs {
		if id <= 0 || i > 0 && id == in.UserIDs[i-1] {
			return nil, geilisub.ErrSelection
		}
	}
	value := "false"
	if in.Enabled {
		value = "true"
		if len(in.UserIDs) > 0 {
			raw, _ := json.Marshal(in.UserIDs)
			value = string(raw)
		}
	}
	if err := s.entClient.Setting.Create().SetKey(SettingLegacySubscriptionManagement).SetValue(value).OnConflictColumns(setting.FieldKey).UpdateNewValues().Exec(ctx); err != nil {
		return nil, err
	}
	return s.GetLegacyRollout(ctx)
}
