package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestReserveInflightBalance_SkipsExplicitSubscriptionKeyOnBalanceGroup(t *testing.T) {
	cfg := &config.Config{}
	cfg.Billing.InflightReservation.Enabled = true
	billing := service.NewBillingCacheService(newHandlerInflightCache(0.001), nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	est := &countingEstimator{cost: 1, priced: true}
	apiKey := &service.APIKey{
		User:          &service.User{ID: 1},
		Group:         &service.Group{SubscriptionType: service.SubscriptionTypeStandard},
		BillingSource: service.BillingSourceSubscription,
	}

	done, err := reserveInflightBalance(newInflightTestGinContext(), billing, est, apiKey, &service.UserSubscription{}, tokenInflightEstimate("m", []byte(`{}`)))
	require.NoError(t, err)
	done()
	require.Equal(t, 0, est.calls, "subscription-settled key must not reserve balance")
}

func TestReserveInflightBalance_ExplicitBalanceKeyOnSubscriptionGroupStillReserves(t *testing.T) {
	cache := newHandlerInflightCache(10)
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	est := &countingEstimator{cost: 1, priced: true}
	apiKey := &service.APIKey{
		User:          &service.User{ID: 1},
		Group:         &service.Group{SubscriptionType: service.SubscriptionTypeSubscription},
		BillingSource: service.BillingSourceBalance,
	}

	done, err := reserveInflightBalance(newInflightTestGinContext(), billing, est, apiKey, nil, tokenInflightEstimate("m", []byte(`{}`)))
	require.NoError(t, err)
	require.Equal(t, 1, est.calls)
	require.Equal(t, 1, cache.count())
	done()
}
