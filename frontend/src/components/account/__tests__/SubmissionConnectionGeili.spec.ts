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
    const wrapper = render(); await flushPromises(); expect(read).toHaveBeenCalledWith('private-holder-token')
    await wrapper.get('.choose-direct').trigger('click'); await wrapper.get('.btn-primary').trigger('click'); await flushPromises()
    expect(save).toHaveBeenCalledWith('private-holder-token', 0, expect.stringMatching(/^[a-f0-9-]{36}$/), { mode: 'direct' })
    expect(wrapper.text()).toContain('connectionSaved'); expect(wrapper.html()).not.toContain('private-holder-token'); wrapper.unmount()
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
