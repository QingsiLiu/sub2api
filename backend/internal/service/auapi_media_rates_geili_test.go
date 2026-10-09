package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAUAPIMediaIndependentRateCapturesOnce(t *testing.T) {
	for _, tc := range []struct {
		name, kind, source        string
		independent               bool
		effective, override, want float64
	}{
		{"video override replaces user rate", "video", BillingSourceBalance, true, .2, .9, .9},
		{"image override replaces user rate", "image", BillingSourceBalance, true, .2, .8, .8},
		{"video inherited personal rate", "video", BillingSourceBalance, false, .7, .9, .7},
		{"zero video rate valid", "video", BillingSourceBalance, true, .2, 0, 0},
		{"subscription preserves subscription rate", "video", BillingSourceSubscription, true, 1.5, .2, 1.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := &APIKey{BillingSource: tc.source, Group: &Group{ImageRateIndependent: tc.independent, ImageRateMultiplier: tc.override, VideoRateIndependent: tc.independent, VideoRateMultiplier: tc.override}}
			rate := resolveAUAPIMediaRateMultiplier(tc.kind, key, tc.effective)
			require.Equal(t, tc.want, rate)
			repo, billing := &auapiSettlementRepo{}, &auapiSettlementBilling{}
			s := &AUAPIImageTaskService{repo: repo, billing: billing}
			amount := QuantizeUsageBillingAmount(.2 * rate)
			task := &AUAPIImageTaskRecord{TaskID: "auvidtask_rate", Kind: tc.kind, Phase: "settle", Count: 1, SuccessCount: 1, UnitPrice: .2, RateMultiplier: rate, ActualAmount: amount, HoldAmount: amount, CreatedAt: time.Now()}
			require.NoError(t, s.finish(context.Background(), task))
			if amount == 0 {
				require.Empty(t, billing.captured)
				require.Len(t, billing.applied, 1)
				require.Zero(t, billing.applied[0].UsageDetail.ActualCost)
			} else {
				require.Len(t, billing.captured, 1)
				require.Equal(t, amount, billing.captured[0].UsageDetail.ActualCost)
				require.Equal(t, rate, billing.captured[0].UsageDetail.RateMultiplier)
			}
		})
	}
}
