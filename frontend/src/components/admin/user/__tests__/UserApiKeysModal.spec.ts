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
vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})
vi.mock('@/utils/format', () => ({ formatDateTime: (value: string) => value }))

const compositeKey = {
  id: 7,
  name: 'Composite',
  key: 'sk-fixture-not-a-real-key',
  status: 'active',
  created_at: '2026-09-22T00:00:00Z',
  group_id: null,
  routing_mode: 'composite',
  group_ids: [22, 11, 99],
  groups: [{ id: 22, name: 'Second' }, { id: 11, name: 'First' }]
}
const user = (id: number) => ({ id, email: `user${id}@example.com`, username: `user${id}` }) as AdminUser
const keys = (id: number, name: string) => ({ items: [{ id, name, key: 'sk-example-key-value-for-tests', status: 'active', created_at: '2026-09-20', group_id: null }] })

async function openModal(selectedUser = user(1)) {
  const wrapper = shallowMount(UserApiKeysModal, {
    props: { show: false, user: selectedUser },
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /></div>' },
        Teleport: true,
        GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' },
        GroupOptionItem: true
      }
    }
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

describe('UserApiKeysModal', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.groups.mockResolvedValue([])
    mocks.list.mockResolvedValue({ items: [compositeKey] })
  })

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
    mocks.list.mockResolvedValue({ items: [{ ...compositeKey, groups: undefined }] })
    mocks.groups.mockResolvedValue([{ id: 22, name: 'Second' }, { id: 11, name: 'First' }])
    const wrapper = await openModal()
    expect(wrapper.get('[data-testid="composite-key-groups"]').text()).toContain('Second')
    wrapper.unmount()
  })

  it('keeps the existing single-group display and editor', async () => {
    mocks.list.mockResolvedValue({ items: [{ ...compositeKey, routing_mode: 'single', group_ids: undefined, group_id: 11, group: { id: 11, name: 'Single' } }] })
    const wrapper = await openModal()
    expect(wrapper.find('[data-testid="composite-key-groups"]').exists()).toBe(false)
    expect(wrapper.get('button').text()).toContain('Single')
    wrapper.unmount()
  })

  it('does not display the previous user keys when the next load fails', async () => {
    mocks.list.mockResolvedValueOnce(keys(1, 'first-user-key')).mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = await openModal()
    expect(wrapper.text()).toContain('first-user-key')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true, user: user(2) })
    await flushPromises()
    expect(wrapper.text()).toContain('user2@example.com')
    expect(wrapper.text()).not.toContain('first-user-key')
    wrapper.unmount()
  })

  it('does not replace current keys with a late previous response', async () => {
    const old = deferred<ReturnType<typeof keys>>()
    mocks.list.mockReturnValueOnce(old.promise).mockResolvedValueOnce(keys(2, 'current-user-key'))
    const wrapper = await openModal()
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true, user: user(2) })
    await flushPromises()
    old.resolve(keys(1, 'old-user-key'))
    await flushPromises()
    expect(wrapper.text()).toContain('current-user-key')
    expect(wrapper.text()).not.toContain('old-user-key')
    wrapper.unmount()
  })

  it('keeps the current request loading when an obsolete request fails', async () => {
    const old = deferred<ReturnType<typeof keys>>()
    const current = deferred<ReturnType<typeof keys>>()
    mocks.list.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const wrapper = await openModal()
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true, user: user(2) })
    old.reject(new Error('obsolete'))
    await flushPromises()
    expect(wrapper.find('.animate-spin').exists()).toBe(true)
    current.resolve(keys(2, 'current-user-key'))
    await flushPromises()
    expect(wrapper.text()).toContain('current-user-key')
    expect(wrapper.find('.animate-spin').exists()).toBe(false)
    wrapper.unmount()
  })

  it('loads keys when the selected user changes while the dialog is open', async () => {
    mocks.list.mockResolvedValueOnce(keys(1, 'first-user-key')).mockResolvedValueOnce(keys(2, 'second-user-key'))
    const wrapper = await openModal()
    await wrapper.setProps({ user: user(2) })
    await flushPromises()
    expect(mocks.list).toHaveBeenLastCalledWith(2)
    expect(wrapper.text()).toContain('second-user-key')
    expect(wrapper.text()).not.toContain('first-user-key')
    wrapper.unmount()
  })
})
