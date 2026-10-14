import type { Account } from '@/types'

// Keep legacy status queries intact. The new UI filters are a separate API field.
export function accountAvailabilityFiltersGeili<T extends { status?: string }>(filters?: T) {
  if (!filters) return filters
  const availability = filters.status === 'geili_balance_stopped' ? 'balance_stopped'
    : filters.status === 'geili_temporary_stopped' ? 'temporarily_stopped' : undefined
  if (!availability) return filters
  const { status: _status, ...rest } = filters
  return { ...rest, availability_status: availability }
}

export function accountRuntimeStateGeili(account: Account): string {
  if (account.runtime_state?.balance_blocked) return 'balance_stopped'
  if (account.runtime_state?.state) return account.runtime_state.state
  if (account.status !== 'active' || !account.schedulable) return 'stopped'
  const now = Date.now()
  if ([account.rate_limit_reset_at, account.overload_until, account.temp_unschedulable_until]
    .some(value => value && new Date(value).getTime() > now)) return 'temporarily_stopped'
  return 'enabled'
}
