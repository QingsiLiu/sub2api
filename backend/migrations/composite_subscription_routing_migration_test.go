package migrations

import (
	"strings"
	"testing"
)

func TestCompositeSubscriptionRoutingMigration(t *testing.T) {
	sql, err := FS.ReadFile("243_composite_subscription_key_routing.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(sql)
	for _, fragment := range []string{
		"subscription_id BIGINT",
		"route_preferences JSONB",
		"target_group_id BIGINT",
		"IF NOT EXISTS",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("migration missing %q", fragment)
		}
	}
}

func TestGroupSubscriptionRateMigration(t *testing.T) {
	sql, err := FS.ReadFile("242_group_subscription_rate_multiplier.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(sql)
	for _, fragment := range []string{
		"subscription_rate_multiplier",
		"SET subscription_rate_multiplier = rate_multiplier",
		"SET NOT NULL",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("migration missing %q", fragment)
		}
	}
}

func TestUnifiedSubscriptionGroupMigration(t *testing.T) {
	sql, err := FS.ReadFile("245_create_unified_subscription_group.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(sql)
	for _, fragment := range []string{
		"全模型订阅",
		"platform = 'composite'",
		"subscription_plan_groups",
		"user_subscription_groups",
		"ON CONFLICT",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("migration missing %q", fragment)
		}
	}
}
