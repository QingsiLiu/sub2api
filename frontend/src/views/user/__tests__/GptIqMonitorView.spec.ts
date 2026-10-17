import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import GptIqMonitorView from '../GptIqMonitorView.vue'
import type { IqMonitorSnapshot } from '@/api/gptIqMonitor'

const { getSnapshot } = vi.hoisted(() => ({ getSnapshot: vi.fn() }))
vi.mock('@/api/gptIqMonitor', async () => ({
  ...await vi.importActual<typeof import('@/api/gptIqMonitor')>('@/api/gptIqMonitor'),
  gptIqMonitorAPI: { getSnapshot, deleteDrawing: vi.fn() },
}))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh-CN' } }),
}))

function snapshot(admin = false): IqMonitorSnapshot {
  return {
    question: 'Q',
    expected_answer: 46.24,
    config: { model: 'gpt-6-astra', reasoning_effort: 'low', probe_interval_minutes: 2 },
    server_time: '2026-10-17T16:49:16+08:00',
    next_refresh_at: '2026-10-17T16:50:00+08:00',
    updated_at: '2026-10-17T16:48:09+08:00',
    groups: [
      { id: 126, name: 'GPT 高智商', multiplier: '0.25', status: 'normal', minutes: [
        { minute: '2026-10-17T16:46:00+08:00', status: 'degraded', answer: 21 },
        { minute: '2026-10-17T16:48:00+08:00', status: 'normal', answer: 46.24, output: 'final 46.24' },
      ] },
    ],
    is_admin: admin,
    probe_log: admin ? [{ minute: '2026-10-17T16:48:00+08:00', group_id: 126, group_name: 'GPT 高智商', status: 'normal', answer: 46.24, account: 'acc-1' }] : undefined,
  }
}

const mountView = () => mount(GptIqMonitorView, {
  global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, BaseDialog: true, ConfirmDialog: true } },
})
let wrapper: ReturnType<typeof mountView>

beforeEach(() => { vi.useFakeTimers(); getSnapshot.mockReset() })
afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })

describe('GptIqMonitorView', () => {
  it('renders one light per probe slot and hides admin logs from users', async () => {
    getSnapshot.mockResolvedValue(snapshot())
    wrapper = mountView(); await flushPromises()
    expect(wrapper.text()).toContain('GPT 高智商')
    expect(wrapper.findAll('[role="group"] button')).toHaveLength(2)
    expect(wrapper.text()).not.toContain('gptIq.admin.title')
  })

  it('shows admin logs with upstream accounts for admins', async () => {
    getSnapshot.mockResolvedValue(snapshot(true))
    wrapper = mountView(); await flushPromises()
    expect(wrapper.text()).toContain('gptIq.admin.title')
    expect(wrapper.text()).toContain('acc-1')
  })

  it('shows an inline auth notice instead of a login prompt on sidecar 401', async () => {
    const { IqMonitorError } = await import('@/api/gptIqMonitor')
    getSnapshot.mockRejectedValue(new IqMonitorError('login_required'))
    wrapper = mountView(); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('gptIq.errors.authFailed')
  })

  it('keeps the last snapshot and schedules the next probe after a failed refresh', async () => {
    getSnapshot.mockResolvedValueOnce(snapshot())
    wrapper = mountView(); await flushPromises()
    getSnapshot.mockRejectedValueOnce(new Error('boom'))
    await vi.advanceTimersByTimeAsync(45_000); await flushPromises()
    expect(getSnapshot).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('GPT 高智商')
    expect(wrapper.get('[role="alert"]').text()).toContain('gptIq.errors.stale')
  })
})
