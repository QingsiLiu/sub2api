import { describe, expect, it, vi } from 'vitest'
const { post, get, put } = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { post, get, put } }))
import { list, recheck, setManualBalanceStop } from '@/api/admin/accounts'
describe('account runtime API', () => {
  it.each([true, false])('sends explicit manual balance blocked=%s and returns the stopped account', async (blocked) => {
    const account = { id: 1, status: 'inactive', schedulable: false, runtime_state: { balance_blocked: blocked } }
    put.mockResolvedValue({ data: account })
    expect(await setManualBalanceStop(1, blocked)).toEqual(account)
    expect(put).toHaveBeenLastCalledWith('/admin/accounts/1/manual-balance-stop', { blocked })
  })
  it('keeps unpaid confirmation explicit and returns the whole updated DTO', async () => {
    post.mockResolvedValue({ data: { id: 1, runtime_state: { balance_blocked: false } } })
    expect(await recheck(1, { model: 'gpt-6', confirm_recharged: true })).toEqual({ id: 1, runtime_state: { balance_blocked: false } })
    expect(post).toHaveBeenCalledWith('/admin/accounts/1/recheck', { model: 'gpt-6', confirm_recharged: true })
  })
  it('maps availability filters without sending an unknown legacy status', async () => {
    get.mockResolvedValue({ data: { items: [] } })
    await list(1, 20, { status: 'geili_balance_stopped', platform: 'anthropic' })
    expect(get.mock.calls.at(-1)?.[1].params).toEqual({ page: 1, page_size: 20, platform: 'anthropic', availability_status: 'balance_stopped' })
    await list(1, 20, { status: 'error' })
    expect(get.mock.calls.at(-1)?.[1].params.status).toBe('error')
  })
})
