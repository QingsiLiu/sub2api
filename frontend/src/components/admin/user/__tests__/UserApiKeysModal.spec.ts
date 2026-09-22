import { flushPromises, shallowMount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import UserApiKeysModal from '../UserApiKeysModal.vue'
import type { AdminUser } from '@/types'

const mocks = vi.hoisted(() => ({ list: vi.fn(), groups: vi.fn(), update: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: {
  users: { getUserApiKeys: mocks.list },
  groups: { getAll: mocks.groups },
  apiKeys: { updateApiKeyGroup: mocks.update }
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() }) }))
vi.mock('vue-i18n', async (importOriginal) => { const actual = await importOriginal<typeof import('vue-i18n')>(); return { ...actual, useI18n: () => ({ t: (key: string) => key }) } })

const key = { id: 7, name: 'Composite', key: 'sk-fixture-not-a-real-key', status: 'active', created_at: '2026-09-22T00:00:00Z', group_id: null, routing_mode: 'composite', group_ids: [22, 11, 99], groups: [{ id: 22, name: 'Second' }, { id: 11, name: 'First' }] }
async function openModal() {
  const wrapper = shallowMount(UserApiKeysModal, {
    props: { show: false, user: { id: 1, email: 'test@example.invalid', username: 'Test' } as AdminUser },
    global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, Teleport: true, GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' } } }
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}
describe('UserApiKeysModal composite group display', () => {
  beforeEach(() => { vi.clearAllMocks(); mocks.list.mockResolvedValue({ items: [key] }); mocks.groups.mockResolvedValue([]) })
  it('shows ordered composite groups and a missing-group ID without a single-group editor', async () => {
    const wrapper = await openModal()
    const groups = wrapper.get('[data-testid="composite-key-groups"]')
    expect(groups.text()).toContain('keys.compositeKey')
    expect(groups.text().indexOf('Second')).toBeLessThan(groups.text().indexOf('First'))
    expect(groups.text()).toContain('#99')
    expect(wrapper.text()).not.toContain('admin.users.none')
    expect(wrapper.findAll('button')).toHaveLength(0)
    expect(mocks.update).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('uses the admin group catalog with older API responses', async () => {
    mocks.list.mockResolvedValue({ items: [{ ...key, groups: undefined }] })
    mocks.groups.mockResolvedValue([{ id: 22, name: 'Second' }, { id: 11, name: 'First' }])
    const wrapper = await openModal()
    expect(wrapper.get('[data-testid="composite-key-groups"]').text()).toContain('Second')
    wrapper.unmount()
  })
  it('keeps the existing single-group display and editor', async () => {
    mocks.list.mockResolvedValue({ items: [{ ...key, routing_mode: 'single', group_ids: undefined, group_id: 11, group: { id: 11, name: 'Single' } }] })
    const wrapper = await openModal()
    expect(wrapper.find('[data-testid="composite-key-groups"]').exists()).toBe(false)
    expect(wrapper.get('button').text()).toContain('Single')
    wrapper.unmount()
  })
})
