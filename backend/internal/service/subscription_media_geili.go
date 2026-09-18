package service

import (
	"context"
	"fmt"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
)

type subscriptionMediaBinderKey struct{}
type SubscriptionMediaBinder func(context.Context, string, *UserSubscription) error

func WithSubscriptionMediaBinder(ctx context.Context, bind SubscriptionMediaBinder) context.Context {
	return context.WithValue(ctx, subscriptionMediaBinderKey{}, bind)
}
func BindSubscriptionMediaTask(ctx context.Context, task string, sub *UserSubscription) error {
	if bind, ok := ctx.Value(subscriptionMediaBinderKey{}).(SubscriptionMediaBinder); ok {
		return bind(ctx, task, sub)
	}
	return nil
}
func (s *SubscriptionService) BindMediaConsumption(ctx context.Context, task string, sub *UserSubscription, keyID int64) error {
	if s.entClient == nil {
		return nil
	}
	if sub == nil || sub.AdmissionKey == "" || strings.TrimSpace(task) == "" {
		return ErrSubscriptionInvalid
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	c := tx.Client()
	tc := dbent.NewTxContext(ctx, tx)
	if _, err := geilisub.LockParent(tc, c, sub.ID); err != nil {
		return err
	}
	rows, err := c.QueryContext(tc, `INSERT INTO subscription_media_tasks(task_id,api_key_id,user_id,subscription_id,admission_key) SELECT $1,$2,$3,$4,request_key FROM subscription_requests WHERE request_key=$5 AND api_key_id=$2 AND subscription_id=$4 AND status='admitted' ON CONFLICT(task_id,api_key_id) DO UPDATE SET task_id=EXCLUDED.task_id WHERE subscription_media_tasks.admission_key=EXCLUDED.admission_key RETURNING admission_key`, task, keyID, sub.UserID, sub.ID, sub.AdmissionKey)
	if err != nil {
		return err
	}
	if !rows.Next() {
		_ = rows.Close()
		return fmt.Errorf("media admission binding conflict")
	}
	var key string
	err = rows.Scan(&key)
	_ = rows.Close()
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *SubscriptionService) ResumeMediaConsumption(ctx context.Context, task string, userID, keyID int64) (*UserSubscription, error) {
	if s.entClient == nil {
		return nil, ErrSubscriptionNotFound
	}
	rows, err := s.entClient.QueryContext(ctx, `SELECT t.subscription_id,t.admission_key FROM subscription_media_tasks t JOIN subscription_requests r ON r.request_key=t.admission_key WHERE t.task_id=$1 AND t.user_id=$2 AND t.api_key_id=$3 AND r.status IN ('admitted','settled')`, task, userID, keyID)
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		return nil, ErrSubscriptionNotFound
	}
	var id int64
	var key string
	err = rows.Scan(&id, &key)
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	sub, err := s.userSubRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if sub.UserID != userID {
		return nil, ErrSubscriptionNotFound
	}
	sub.AdmissionKey = key
	sub.MediaLookupAdmission = true
	return sub, nil
}
