package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectCheckoutGroupRates_FiltersAndOrdersByPanel(t *testing.T) {
	got := selectCheckoutGroupRates([]checkoutGroupInput{
		{ID: 46, Name: "CC-满血 Max", UsagePanel: "claude", SubscriptionRateMultiplier: 8, SubscriptionType: SubscriptionTypeStandard},
		{ID: 11, Name: "周卡", SubscriptionType: SubscriptionTypeSubscription, SubscriptionRateMultiplier: 1},
		{ID: 60, Name: "好兄弟专用", IsExclusive: true, SubscriptionRateMultiplier: 0.9, SubscriptionType: SubscriptionTypeStandard},
		{ID: 4, Name: "GPT 稳定", UsagePanel: "gpt", SubscriptionRateMultiplier: 1, SubscriptionType: SubscriptionTypeStandard},
		{ID: 99, Name: "门面", Platform: PlatformComposite, SubscriptionRateMultiplier: 1, SubscriptionType: SubscriptionTypeStandard},
		{ID: 27, Name: "GPT 给力 Pro", UsagePanel: "gpt", SubscriptionRateMultiplier: 1.3, SubscriptionType: SubscriptionTypeStandard},
	})

	require.Equal(t, []string{"GPT 稳定", "GPT 给力 Pro", "CC-满血 Max"}, checkoutRateNames(got))
	require.Equal(t, []float64{1, 1.3, 8}, checkoutRateValues(got))
}

func TestSelectCheckoutGroupRates_Empty(t *testing.T) {
	require.Empty(t, selectCheckoutGroupRates(nil))
}

func checkoutRateNames(rows []CheckoutGroupRate) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.Name
	}
	return out
}

func checkoutRateValues(rows []CheckoutGroupRate) []float64 {
	out := make([]float64, len(rows))
	for i, row := range rows {
		out[i] = row.SubscriptionRateMultiplier
	}
	return out
}
