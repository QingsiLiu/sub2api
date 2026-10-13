import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
const { read, save } = vi.hoisted(() => ({ read: vi.fn(), save: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: {} }))
vi.mock('@/api/accountSubmission', async () => ({ ...await vi.importActual('@/api/accountSubmission'), getSubmissionConnection: read, updateSubmissionConnection: save }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
import Component from '../SubmissionConnectionGeili.vue'
import { SubmissionRequestError } from '@/api/accountSubmission'
describe('holder connection management', () => {
  beforeEach(() => { vi.clearAllMocks(); read.mockResolvedValue({ mode: 'custom', version: 0, has_preset: true, has_password: true, proxy: { name: 'Own proxy', protocol: 'http', host: '8.8.8.8', port: 8080, username: 'holder' } }); save.mockResolvedValue({ mode: 'direct', version: 1, has_preset: true, has_password: false }) })
  const render = () => mount(Component, { props: { token: 'private-holder-token' }, global: { stubs: { SubmissionProxyEditorGeili: { props: ['modelValue'], template: '<button class="choose-direct" @click="$emit(\'update:modelValue\', {mode: \'direct\'})">direct</button>' } } } })
  it('sends only its capability and proxy configuration, with optimistic version and request ID', async () => {
    const wrapper = render(); await flushPromises(); expect(read).toHaveBeenCalledWith('private-holder-token', undefined)
    await wrapper.get('.choose-direct').trigger('click'); await wrapper.get('.btn-primary').trigger('click'); await flushPromises()
    expect(save).toHaveBeenCalledWith('private-holder-token', 0, expect.stringMatching(/^[a-f0-9-]{36}$/), { mode: 'direct' }, undefined)
    expect(wrapper.text()).toContain('connectionSaved'); expect(wrapper.html()).not.toContain('private-holder-token'); wrapper.unmount()
  })
  it('scopes connection reads and writes to the selected batch member', async () => {
    const ref = '11111111-1111-1111-1111-111111111111'
    const wrapper = mount(Component, { props: { token: 'private-holder-token', accountRef: ref }, global: { stubs: { SubmissionProxyEditorGeili: true } } })
    await flushPromises(); expect(read).toHaveBeenCalledWith('private-holder-token', ref)
    await wrapper.get('.btn-primary').trigger('click'); await flushPromises()
    expect(save.mock.calls[0][4]).toBe(ref); wrapper.unmount()
  })
  it('ignores a previous member response after selection changes', async () => {
    let complete!: (value: unknown) => void
    read.mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const first = '11111111-1111-1111-1111-111111111111', second = '22222222-2222-2222-2222-222222222222'
    const wrapper = mount(Component, { props: { token: 'private-holder-token', accountRef: first }, global: { stubs: { SubmissionProxyEditorGeili: true } } })
    await wrapper.setProps({ accountRef: second }); await flushPromises()
    complete({ mode: 'direct', version: 99, has_preset: false, has_password: false }); await flushPromises()
    await wrapper.get('.btn-primary').trigger('click'); await flushPromises()
    expect(save.mock.calls[0][1]).toBe(0); expect(save.mock.calls[0][4]).toBe(second); wrapper.unmount()
  })
  it('does not automatically retry an uncertain save and requires a fresh snapshot', async () => {
    save.mockRejectedValueOnce(new SubmissionRequestError(0)); const wrapper = render(); await flushPromises()
    await wrapper.get('.btn-primary').trigger('click'); await flushPromises(); expect(save).toHaveBeenCalledTimes(1); expect(wrapper.text()).toContain('connectionUncertain'); expect(wrapper.find('.btn-primary').exists()).toBe(false)
    await wrapper.get('.btn-secondary').trigger('click'); await flushPromises(); expect(read).toHaveBeenCalledTimes(2); wrapper.unmount()
  })
  it('reports conflict without overwriting another instance’s configuration', async () => {
    save.mockRejectedValueOnce(new SubmissionRequestError(409)); const wrapper = render(); await flushPromises(); await wrapper.get('.btn-primary').trigger('click'); await flushPromises(); expect(wrapper.text()).toContain('connectionConflict'); expect(save).toHaveBeenCalledTimes(1); wrapper.unmount()
  })
})
