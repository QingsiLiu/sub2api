package service

// Keep the balance argument for existing wiring compatibility. Recovery is
// triggered by an exhausted pool and never queries supplier balance endpoints.
func AttachAccountRecoveryGeili(tests *AccountTestService, limits *RateLimitService, _ *CNProviderBalanceService) {
	if tests == nil || limits == nil {
		return
	}
	limits.accountRecoveryGeili = NewAccountRecoveryServiceGeili(limits.accountRepo, tests.ProbeAccountRecoveryGeili, nil)
	if recovery := limits.accountRecoveryGeili; recovery != nil {
		recovery.clearSchedulingBlock = limits.ClearRecoveredGenericTemporaryCacheGeili
		recovery.notifySchedulingRecovery = limits.notifyAccountSchedulingBlockCleared
	}
}
