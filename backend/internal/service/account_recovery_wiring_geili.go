package service

import (
	"context"
	"time"
)

func AttachAccountRecoveryGeili(tests *AccountTestService, limits *RateLimitService, balance *CNProviderBalanceService) {
	if tests == nil || limits == nil {
		return
	}
	verify := func(ctx context.Context, a *Account) (bool, bool, error) {
		if balance == nil || a == nil || a.Type != AccountTypeAPIKey || a.Platform != PlatformDeepseek && a.Platform != PlatformKimi {
			return false, false, nil
		}
		result, err := balance.QueryBalanceForAccount(ctx, a)
		if err != nil {
			return true, false, err
		}
		if result == nil || !result.Success {
			return true, false, nil
		}
		funded := result.Balance > 0
		for _, entry := range result.Balances {
			funded = funded || entry.Balance > 0
		}
		return true, result.Available && funded, nil
	}
	limits.accountRecoveryGeili = NewAccountRecoveryServiceGeili(limits.accountRepo, tests.ProbeAccountRecoveryGeili, verify)
	if recovery := limits.accountRecoveryGeili; recovery != nil {
		recovery.balanceLatch, _ = limits.tempUnschedCache.(AccountBalanceLatchCacheGeili)
		recovery.clearSchedulingBlock = limits.ClearRecoveredGenericTemporaryCacheGeili
		recovery.notifySchedulingRecovery = limits.notifyAccountSchedulingBlockCleared
		recovery.wg.Add(1)
		go func() {
			defer recovery.wg.Done()
			if initializer, ok := limits.accountRepo.(interface {
				InitializeAccountBalanceBlocksGeili(context.Context, int) (int, error)
			}); ok {
				ctx, cancel := context.WithTimeout(recovery.ctx, 20*time.Second)
				for {
					n, err := initializer.InitializeAccountBalanceBlocksGeili(ctx, 200)
					if err != nil || n < 200 {
						break
					}
				}
				cancel()
			}
			// Paid generation never probes debt automatically. Only trusted balance
			// endpoints may grant a subsequent, bounded model verification.
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			var balanceCursor int64
			for {
				select {
				case <-recovery.ctx.Done():
					return
				case <-ticker.C:
					lister, ok := limits.accountRepo.(interface {
						ListAccountBalanceBlockedGeili(context.Context, int64, int) ([]int64, bool, error)
					})
					if !ok {
						continue
					}
					ids, more, err := lister.ListAccountBalanceBlockedGeili(recovery.ctx, balanceCursor, 20)
					if err != nil {
						continue
					}
					if len(ids) > 0 {
						balanceCursor = ids[len(ids)-1]
					}
					if !more {
						balanceCursor = 0
					}
					for _, id := range ids {
						a, e := limits.accountRepo.GetByID(recovery.ctx, id)
						if e != nil {
							continue
						}
						ctx, cancel := context.WithTimeout(recovery.ctx, 25*time.Second)
						known, sufficient, e := verify(ctx, a)
						if e == nil && known && sufficient {
							model := a.providerDefaultTestModel()
							if a.RuntimeState != nil && a.RuntimeState.LastProbe != nil && a.RuntimeState.LastProbe.Model != "" {
								model = a.RuntimeState.LastProbe.Model
							}
							_ = recovery.check(ctx, a, model, "balance_recovered", false)
						}
						cancel()
					}
				}
			}
		}()
	}
}
