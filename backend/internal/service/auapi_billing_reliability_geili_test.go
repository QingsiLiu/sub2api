package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type auapiSettlementRepo struct {
	AUAPIImageTaskRepository
	saved   []*AUAPIImageTaskRecord
	saveErr error
}

func (r *auapiSettlementRepo) Save(_ context.Context, t *AUAPIImageTaskRecord) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	copy := *t
	r.saved = append(r.saved, &copy)
	return nil
}

type auapiSettlementBilling struct {
	UsageBillingRepository
	applied            []*UsageBillingCommand
	captured, released []*BatchImageBalanceHoldCommand
	err                error
}

func (r *auapiSettlementBilling) Apply(_ context.Context, c *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	r.applied = append(r.applied, c)
	return &UsageBillingApplyResult{Applied: true}, r.err
}
func (r *auapiSettlementBilling) CaptureBatchImageBalance(_ context.Context, c *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error) {
	r.captured = append(r.captured, c)
	return &BatchImageBalanceHoldResult{Applied: true}, r.err
}
func (r *auapiSettlementBilling) ReleaseBatchImageBalance(_ context.Context, c *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error) {
	r.released = append(r.released, c)
	return &BatchImageBalanceHoldResult{Applied: true}, r.err
}

func TestAUAPISettlementPreservesHoldNamespaceAndSafeDetail(t *testing.T) {
	for _, kind := range []string{"capture", "release", "subscription", "failed_subscription", "free"} {
		t.Run(kind, func(t *testing.T) {
			repo, billing := &auapiSettlementRepo{}, &auapiSettlementBilling{}
			svc := &AUAPIImageTaskService{repo: repo, billing: billing}
			task := &AUAPIImageTaskRecord{TaskID: "auimgtask_test", UserID: 1, APIKeyID: 2, AccountID: 3, GroupID: 4, Model: "gpt-image-2", UpstreamModel: "gpt-image-2", Phase: "settle", HoldAmount: 1, ActualAmount: .8, UnitPrice: .4, RateMultiplier: 2, SuccessCount: 1, CreatedAt: time.Now().Add(-time.Minute), RequestJSON: json.RawMessage(`{"prompt":"NEVER-PERSIST-THIS","api_key":"SECRET"}`)}
			if kind == "release" {
				task.Phase = "fail"
			}
			if kind == "subscription" || kind == "failed_subscription" {
				id := int64(9)
				task.SubscriptionID = &id
				task.AdmissionKey = "admit"
			}
			if kind == "failed_subscription" {
				task.Phase = "fail"
			}
			if kind == "free" {
				task.HoldAmount = 0
				task.ActualAmount = 0
			}
			require.NoError(t, svc.finish(context.Background(), task))
			require.Equal(t, "done", task.Phase)
			require.NotNil(t, task.CompletedAt)
			var detail *UsageLog
			switch kind {
			case "capture":
				require.Len(t, billing.captured, 1)
				c := billing.captured[0]
				require.Equal(t, "auapi_image_hold:auimgtask_test", c.HoldRequestID)
				require.Equal(t, "auapi_image_capture:auimgtask_test", c.RequestID)
				detail = c.UsageDetail
				require.Equal(t, *task.CompletedAt, c.CompletedAt)
			case "release":
				require.Len(t, billing.released, 1)
				require.Equal(t, "auapi_image_hold:auimgtask_test", billing.released[0].HoldRequestID)
				require.Zero(t, task.ActualAmount)
			default:
				require.Len(t, billing.applied, 1)
				detail = billing.applied[0].UsageDetail
				require.Equal(t, "auapi_image_settle:auimgtask_test", billing.applied[0].RequestID)
			}
			if kind == "failed_subscription" {
				require.Nil(t, detail)
				require.True(t, billing.applied[0].TerminalFailure)
				require.Zero(t, billing.applied[0].SubscriptionCost)
			}
			if detail != nil {
				require.Equal(t, "auapi_image:auimgtask_test", detail.RequestID)
				encoded, err := json.Marshal(detail)
				require.NoError(t, err)
				require.NotContains(t, string(encoded), "NEVER-PERSIST")
				require.NotContains(t, string(encoded), "SECRET")
			}
		})
	}
}
func TestAUAPISettlementCannotChargeWhenCompletionSnapshotCannotPersist(t *testing.T) {
	repo, billing := &auapiSettlementRepo{saveErr: ErrAUAPIImageLease}, &auapiSettlementBilling{}
	svc := &AUAPIImageTaskService{repo: repo, billing: billing}
	task := &AUAPIImageTaskRecord{TaskID: "auimgtask_lost", Phase: "settle", HoldAmount: 1}
	require.ErrorIs(t, svc.finish(context.Background(), task), ErrAUAPIImageLease)
	require.Empty(t, billing.captured)
	require.Empty(t, billing.applied)
}
func TestAUAPISettlementRetriesKeepCompletionIdentity(t *testing.T) {
	repo, billing := &auapiSettlementRepo{}, &auapiSettlementBilling{err: errors.New("db unavailable")}
	svc := &AUAPIImageTaskService{repo: repo, billing: billing}
	task := &AUAPIImageTaskRecord{TaskID: "auimgtask_retry", Phase: "settle", HoldAmount: 1, ActualAmount: .3}
	require.Error(t, svc.finish(context.Background(), task))
	completed := *task.CompletedAt
	billing.err = nil
	require.NoError(t, svc.finish(context.Background(), task))
	require.Equal(t, completed, *task.CompletedAt)
	require.Len(t, billing.captured, 2)
	require.Equal(t, billing.captured[0].RequestID, billing.captured[1].RequestID)
	require.Equal(t, billing.captured[0].CompletedAt, billing.captured[1].CompletedAt)
}
