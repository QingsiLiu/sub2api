import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isSimpleMode: false }) }))
vi.mock('vue-i18n', async () => ({ ...(await vi.importActual<object>('vue-i18n')), useI18n: () => ({ t: (key: string) => key }) }))
import SubmissionAccountFormGeili from '../SubmissionAccountFormGeili.vue'
import CreateAccountModal from '../CreateAccountModal.vue'
import OAuthAuthorizationFlow from '../OAuthAuthorizationFlow.vue'

let fetchMock: ReturnType<typeof vi.fn>
beforeEach(() => {
  fetchMock = vi.fn(async (_url: string, options: RequestInit) => {
    const { action } = JSON.parse(String(options.body))
    const data = action === 'mixed-risk' ? {has_risk: false} : action === 'profiles' ? [] : action === 'settings' ? { openai_sync_model_ids: ['gpt-test'] } : action === 'web-search' ? { enabled: false, providers: [] } : { id: 0, status: 'submitted' }
    return { ok: true, json: async () => ({ code: 0, data }) }
  })
  vi.stubGlobal('fetch', fetchMock)
})
afterEach(() => {vi.unstubAllGlobals();document.body.innerHTML = ''})
const render = (platform: 'anthropic' | 'openai') => mount(SubmissionAccountFormGeili, {
  props: { token: 'synthetic-invite', invite: { platform, status: 'pending', name: 'Holder', concurrency: 17, expires_at: '2099-01-01' } },
  global: { plugins: [createPinia()], stubs: { Teleport: true } },
})

describe('complete official invitation account form', () => {
  it.each(['anthropic', 'openai'] as const)('uses the actual %s account modal with all form sections and no groups', async platform => {
    const wrapper = render(platform)
    await flushPromises()
    expect(wrapper.findComponent(CreateAccountModal).exists()).toBe(true)
    expect(wrapper.text()).toContain('admin.accounts.notes')
    expect(wrapper.text()).toContain('admin.accounts.priority')
    expect(wrapper.text()).toContain('admin.accounts.loadFactor')
    expect(wrapper.find('[data-tour="account-form-groups"]').exists()).toBe(false)
    expect((wrapper.get('[data-tour="account-form-name"]').element as HTMLInputElement).value).toBe('Holder')
    expect(wrapper.text()).toMatch(/OAuth|admin.accounts.oauthSetupToken/)
    expect(wrapper.text()).toMatch(/API Key|admin.accounts.apiKey/)
    expect(wrapper.text()).toContain('admin.proxies.createProxy')
    const next = wrapper.findAll('button').find(button => button.text().includes('common.next'))
    expect(next).toBeDefined()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    const flow = wrapper.getComponent(OAuthAuthorizationFlow)
    expect(flow.props('showManualOption')).toBe(true)
    if (platform === 'anthropic') {
      expect(flow.props('showCookieOption')).toBe(true)
    } else {
      expect(flow.props('showRefreshTokenOption')).toBe(true)
      expect(flow.props('showMobileRefreshTokenOption')).toBe(true)
      expect(flow.props('showCodexSessionImportOption')).toBe(true)
      expect(flow.props('showAgentIdentityOption')).toBe(true)
      expect(flow.props('showCodexPatOption')).toBe(true)
    }
    expect(fetchMock.mock.calls.every(([url]) => url === '/api/v1/account-submissions/form')).toBe(true)
    wrapper.unmount()
  })
})
