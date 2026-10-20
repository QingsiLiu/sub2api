import { describe, expect, it } from 'vitest'
import { accountRuntimeStateGeili, accountSchedulingEnabledGeili } from '../accountRuntimeGeili'
import type { Account } from '@/types'

describe('independent account scheduling intent', () => {
  it.each([true, false])('keeps error recovery armed regardless of unpaid=%s', balance_blocked => {
    const account = { status: 'error', schedulable: false,
      runtime_state: { state: 'temporarily_stopped', stop_reason: 'temporary_failure', balance_blocked }
    } as Account
    expect(accountSchedulingEnabledGeili(account)).toBe(true)
    expect(accountRuntimeStateGeili(account)).toBe('temporarily_stopped')
  })

  it('lets a manual stop persist through a later error', () => {
    const account = { status: 'error', schedulable: false,
      runtime_state: { state: 'stopped', stop_reason: 'manual', balance_blocked: false }
    } as Account
    expect(accountSchedulingEnabledGeili(account)).toBe(false)
    expect(accountRuntimeStateGeili(account)).toBe('stopped')
  })

  it('keeps enabled and legacy manual accounts independent from unpaid labels', () => {
    const account = { status: 'active', schedulable: true,
      runtime_state: { state: 'enabled', stop_reason: '', balance_blocked: true }
    } as Account
    expect(accountSchedulingEnabledGeili(account)).toBe(true)
    expect(accountRuntimeStateGeili(account)).toBe('enabled')
    expect(accountSchedulingEnabledGeili({ ...account, status: 'inactive' })).toBe(false)
  })
})
