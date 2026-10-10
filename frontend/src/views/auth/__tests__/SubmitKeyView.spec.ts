import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const { inspect, submit } = vi.hoisted(() => ({ inspect: vi.fn(), submit: vi.fn() }))
vi.mock('@/api/accountSubmission', async () => {
  const actual = await vi.importActual<typeof import('@/api/accountSubmission')>('@/api/accountSubmission')
  return { ...actual, inspectSubmissionInvite: inspect, submitAccountKey: submit }
})
vi.mock('@/api/client', () => ({ apiClient: {} }))
vi.mock('@/components/layout/AuthLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
import SubmitKeyView from '@/views/auth/SubmitKeyView.vue'
import { SubmissionRequestError } from '@/api/accountSubmission'

describe('public key submission page', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    window.history.replaceState({}, '', '/submit-key#token=synthetic-token')
    inspect.mockResolvedValue({ platform: 'anthropic', status: 'pending', expires_at: '2099-01-01T00:00:00Z' })
    submit.mockResolvedValue({ status: 'submitted' })
  })
  afterEach(() => window.history.replaceState({}, '', '/'))
  const render = () => mount(SubmitKeyView, { global: { stubs: { AuthLayout: { template: '<main><slot /></main>' } } } })

  it('needs no login, masks the key and clears it and the link after submission', async () => {
    const wrapper = render()
    await flushPromises()
    expect(inspect).toHaveBeenCalledWith('synthetic-token')
    const input = wrapper.get('input')
    expect(input.attributes('type')).toBe('password')
    expect(input.attributes('autocomplete')).toBe('off')
    await input.setValue('synthetic-private-key')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(submit).toHaveBeenCalledWith('synthetic-token', 'synthetic-private-key')
    expect(wrapper.find('input').exists()).toBe(false)
    expect(wrapper.text()).toContain('keyIntake.success')
    expect(window.location.hash).toBe('')
    expect(wrapper.html()).not.toContain('synthetic-private-key')
    wrapper.unmount()
  })
  it('confirms a lost success response without submitting twice', async () => {
    const wrapper = render()
    await flushPromises()
    submit.mockRejectedValueOnce(new SubmissionRequestError(0))
    inspect.mockResolvedValueOnce({ platform: 'anthropic', status: 'submitted' })
    await wrapper.get('input').setValue('synthetic-private-key')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(submit).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('keyIntake.success')
    wrapper.unmount()
  })
  it('blocks a revoked invitation and never sends a key', async () => {
    inspect.mockRejectedValueOnce(new SubmissionRequestError(410))
    const wrapper = render()
    await flushPromises()
    expect(wrapper.find('input').exists()).toBe(false)
    expect(wrapper.text()).toContain('keyIntake.invalid')
    expect(submit).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('rejects multiline keys locally without consuming the invite', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('input').setValue('synthetic key')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.text()).toContain('keyIntake.keyInvalid')
    expect(submit).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
