import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const { getUsage } = vi.hoisted(() => ({ getUsage: vi.fn() }))
vi.mock('@/api/accountSubmission', async () => ({ ...await vi.importActual<typeof import('@/api/accountSubmission')>('@/api/accountSubmission'), getSubmissionUsage: getUsage }))
vi.mock('@/api/client', () => ({ apiClient: {} }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/components/charts/TokenUsageTrend.vue', () => ({ default: { props: ['trendData'], template: '<div data-trend />' } }))
import AccountSubmissionUsage from '@/components/account/AccountSubmissionUsage.vue'
import { SubmissionRequestError } from '@/api/accountSubmission'
const summary = { requests: 2, input_tokens: 10, output_tokens: 20, cache_creation_tokens: 30, cache_read_tokens: 40, reference_cost: .75 }
const fixture = { name: 'external account', platform: 'anthropic', enabled: false, start_date: '2026-10-05', end_date: '2026-10-11', timezone: 'Asia/Shanghai', currency: 'USD', updated_at: '2026-10-11T00:00:00Z', summary, daily: [{ date: '2026-10-11', ...summary }], models: [{ model: 'claude-sonnet', ...summary }] }
describe('external owner usage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] })
    vi.setSystemTime(new Date('2026-10-10T16:30:00Z'))
    getUsage.mockResolvedValue(fixture)
  })
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })
  it('loads only the capability and bounded Beijing dates, rendering reference cost and tokens', async () => {
    const w = mount(AccountSubmissionUsage, { props: { token: 'private-link' } })
    await flushPromises()
    expect(getUsage).toHaveBeenCalledWith('private-link', '2026-10-05', '2026-10-11')
    expect(w.text()).toContain('$0.750000')
    expect(w.text()).toContain('external account')
    expect(w.text()).toContain('keyIntake.costNotice')
    expect(w.html()).not.toContain('private-link')
    expect(w.text()).toContain('claude-sonnet')
    w.unmount()
  })
  it('shows each account and a shared total, emitting only scoped account selectors', async () => {
    const accounts = [
      { account_ref: '11111111-1111-1111-1111-111111111111', name: 'Holder #1', platform: 'anthropic', enabled: false, summary },
      { account_ref: '22222222-2222-2222-2222-222222222222', name: 'Holder #2', platform: 'anthropic', enabled: true, summary: { ...summary, requests: 0, reference_cost: 0 } }
    ]
    getUsage.mockResolvedValueOnce({ ...fixture, accounts })
    const w = mount(AccountSubmissionUsage, { props: { token: 'private-link' } }); await flushPromises()
    expect(w.findAll('tbody tr')).toHaveLength(2)
    expect(w.text()).toContain('Holder #1'); expect(w.text()).toContain('Holder #2'); expect(w.text()).toContain('batchTotal')
    expect(w.emitted('accounts')).toEqual([[accounts]])
    expect(w.html()).not.toContain('private-link'); w.unmount()
  })
  it('rejects a custom range beyond 90 days without issuing a request', async () => {
    const w = mount(AccountSubmissionUsage, { props: { token: 'private-link' } }); await flushPromises()
    await w.get('select').setValue('custom')
    const dates = w.findAll('input[type="date"]')
    await dates[0].setValue('2026-07-13'); await dates[1].setValue('2026-10-11')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(getUsage).toHaveBeenCalledTimes(1)
    expect(w.get('[role="alert"]').text()).toContain('dateInvalid')
    w.unmount()
  })
  it('clears old values on failure and stops polling after revocation', async () => {
    const w = mount(AccountSubmissionUsage, { props: { token: 'private-link' } }); await flushPromises()
    getUsage.mockRejectedValueOnce(new SubmissionRequestError(410))
    await w.get('form').trigger('submit'); await flushPromises()
    expect(w.text()).toContain('viewUnavailable'); expect(w.text()).not.toContain('$0.750000')
    vi.advanceTimersByTime(120000); await flushPromises()
    expect(getUsage).toHaveBeenCalledTimes(2)
    w.unmount()
  })
  it('polls only when visible and stops after unmount', async () => {
    const visible = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    const w = mount(AccountSubmissionUsage, { props: { token: 'private-link' } }); await flushPromises()
    vi.advanceTimersByTime(60000); await flushPromises(); expect(getUsage).toHaveBeenCalledTimes(1)
    visible.mockReturnValue('visible'); vi.advanceTimersByTime(60000); await flushPromises(); expect(getUsage).toHaveBeenCalledTimes(2)
    w.unmount(); vi.advanceTimersByTime(60000); await flushPromises(); expect(getUsage).toHaveBeenCalledTimes(2)
  })
  it('ignores a prior capability response arriving after the link changes', async () => {
    let complete!: (value: unknown) => void
    getUsage.mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const w = mount(AccountSubmissionUsage, { props: { token: 'first' } })
    await w.setProps({ token: 'second' }); await flushPromises()
    complete({ ...fixture, name: 'old owner private name' }); await flushPromises()
    expect(w.text()).not.toContain('old owner private name')
    expect(getUsage).toHaveBeenLastCalledWith('second', '2026-10-05', '2026-10-11')
    w.unmount()
  })
})
