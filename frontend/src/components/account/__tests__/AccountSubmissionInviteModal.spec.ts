import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
const { create, list, revoke } = vi.hoisted(() => ({ create: vi.fn(), list: vi.fn(), revoke: vi.fn() }))
vi.mock('@/api/accountSubmission', () => ({ createSubmissionInvite: create, listSubmissionInvites: list, revokeSubmissionInvite: revoke }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/components/common/BaseDialog.vue', () => ({ default: { props: ['show'], template: '<div v-if="show"><slot /></div>' } }))
import AccountSubmissionInviteModal from '@/components/account/AccountSubmissionInviteModal.vue'

describe('administrator key invitation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    list.mockResolvedValue({ items: [], total: 0 })
    create.mockResolvedValue({ invite: { id: 1 }, token: 'synthetic-token' })
    revoke.mockResolvedValue(undefined)
  })
  it('generates a fragment link with explicit platform configuration and zero multiplier', async () => {
    const wrapper = mount(AccountSubmissionInviteModal, { props: { show: false, groups: [], proxies: [] } })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.get('#invite-name').setValue('external owner')
    await wrapper.get('#invite-platform').setValue('openai')
    await wrapper.get('#invite-rate').setValue('0')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(create.mock.calls[0][0]).toMatchObject({ name: 'external owner', platform: 'openai', rate_multiplier: 0, group_ids: [], concurrency: 1, priority: 50, proxy_id: null })
    const link = wrapper.get('input[readonly]').element as HTMLInputElement
    const url = new URL(link.value)
    expect(url.pathname).toBe('/submit-key')
    expect(url.search).toBe('')
    expect(url.hash).toBe('#token=synthetic-token')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    expect(wrapper.find('input[readonly]').exists()).toBe(false)
    wrapper.unmount()
  })
})
