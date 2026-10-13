import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
const { submit, inspect } = vi.hoisted(() => ({ submit: vi.fn(), inspect: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: {} }))
vi.mock('@/api/accountSubmission', async () => ({ ...await vi.importActual('@/api/accountSubmission'), submitAccountKeyBatch: submit, inspectSubmissionInvite: inspect }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
import Component from '../SubmissionBatchFormGeili.vue'
import { SubmissionRequestError } from '@/api/accountSubmission'
const render = () => mount(Component, { props: { token: 'synthetic-private-link', invite: { name: 'Holder', platform: 'anthropic', status: 'pending', expires_at: '', concurrency: 3 } }, global: { stubs: { SubmissionProxyEditorGeili: true } } })
async function paste(w: ReturnType<typeof render>, text: string) {
  await w.get('input[type="password"]').trigger('paste', { clipboardData: { getData: () => text } })
}
beforeEach(() => { vi.clearAllMocks(); submit.mockResolvedValue({ status: 'submitted' }); inspect.mockResolvedValue({ status: 'pending' }) })
describe('batch holder submission', () => {
  it('splits multiline paste into masked rows and submits the batch with common defaults', async () => {
    const w = render(); await paste(w, 'synthetic-first\r\n synthetic-second \n\nsynthetic-third')
    expect(w.findAll('input[type="password"]')).toHaveLength(3)
    expect(w.find('textarea').exists()).toBe(false)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(submit).toHaveBeenCalledWith('synthetic-private-link', ['synthetic-first', 'synthetic-second', 'synthetic-third'], { name: 'Holder', concurrency: 3, connection: { mode: 'direct' } })
    expect(w.emitted('created')).toEqual([['synthetic-private-link']])
    expect((w.get('input[type="password"]').element as HTMLInputElement).value).toBe('')
    w.unmount()
  })
  it('rejects duplicate keys and a paste exceeding the batch limit without sending credentials', async () => {
    const w = render(); await paste(w, 'synthetic-a\nsynthetic-a'); await w.get('form').trigger('submit')
    expect(submit).not.toHaveBeenCalled(); expect(w.text()).toContain('batchInvalid')
    await paste(w, Array.from({ length: 101 }, (_, n) => `synthetic-${n}`).join('\n'))
    expect(w.findAll('input[type="password"]')).toHaveLength(2)
    expect(submit).not.toHaveBeenCalled(); w.unmount()
  })
  it('clears all secrets after an uncertain response and only inspects before any retry', async () => {
    submit.mockRejectedValueOnce(new SubmissionRequestError(0))
    const w = render(); await paste(w, 'synthetic-a\nsynthetic-b'); await w.get('form').trigger('submit'); await flushPromises()
    expect(w.text()).toContain('batchUncertain')
    expect((w.get('input[type="password"]').element as HTMLInputElement).value).toBe('')
    expect(w.get('input[type="password"]').attributes('disabled')).toBeDefined()
    await w.get('form').trigger('submit'); expect(submit).toHaveBeenCalledTimes(1)
    inspect.mockResolvedValueOnce({ status: 'submitted', view_available: true })
    const confirm = w.findAll('button').find(button => button.text().includes('batchConfirm'))!
    await confirm.trigger('click'); await flushPromises()
    expect(inspect).toHaveBeenCalledWith('synthetic-private-link'); expect(submit).toHaveBeenCalledTimes(1)
    expect(w.emitted('created')).toEqual([['synthetic-private-link']]); w.unmount()
  })
  it('ignores a completed write after the form is unmounted or the invitation mode changes', async () => {
    let complete!: (value: unknown) => void
    submit.mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const w = render(); await paste(w, 'synthetic-a\nsynthetic-b'); await w.get('form').trigger('submit'); w.unmount()
    complete({ status: 'submitted' }); await flushPromises(); expect(w.emitted('created')).toBeUndefined()
  })
})
