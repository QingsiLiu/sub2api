import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountActionMenu from '../AccountActionMenu.vue'
import type { Account } from '@/types'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const account = (blocked: boolean, parent: number | null = null) => ({
  id: 17, type: 'apikey', platform: 'anthropic', status: 'inactive', schedulable: false,
  parent_account_id: parent,
  runtime_state: { state: blocked ? 'balance_stopped' : 'stopped', balance_blocked: blocked, stop_reason: blocked ? 'balance' : 'manual' }
} as Account)
const open = (blocked: boolean, parent: number | null = null) => mount(AccountActionMenu, {
  props: { show: true, account: account(blocked, parent), anchorRect: new DOMRect(100, 100, 24, 24) },
  attachTo: document.body
})

describe('manual supplier balance controls', () => {
  it.each([true, false])('shows an explicit manual action for blocked=%s', async blocked => {
    const wrapper = open(blocked)
    const key = blocked ? 'admin.accounts.runtime.clearBalanceStop' : 'admin.accounts.runtime.markBalanceStop'
    const button = Array.from(document.body.querySelectorAll('button')).find(b => b.textContent?.includes(key))
    expect(button).toBeDefined()
    button!.click()
    await wrapper.vm.$nextTick()
    expect(wrapper.emitted('manual-balance-stop')).toEqual([[account(blocked)]])
    expect(wrapper.emitted('close')).toBeTruthy()
    wrapper.unmount()
  })
  it('offers recheck regardless of the unpaid label', () => {
    const wrapper = open(true)
    expect(document.body.textContent).toContain('admin.accounts.runtime.recheck')
    expect(document.body.textContent).not.toContain('admin.accounts.recoverState')
    wrapper.unmount()
  })
  it('keeps debt ownership on the credential parent for shadows', () => {
    const wrapper = open(false, 4)
    expect(document.body.textContent).not.toContain('admin.accounts.runtime.markBalanceStop')
    expect(document.body.textContent).not.toContain('admin.accounts.runtime.clearBalanceStop')
    wrapper.unmount()
  })
  it('allows a historical shadow balance marker to be cleared separately', () => {
    const wrapper = open(true, 4)
    expect(document.body.textContent).toContain('admin.accounts.runtime.clearBalanceStop')
    expect(document.body.textContent).not.toContain('admin.accounts.runtime.markBalanceStop')
    wrapper.unmount()
  })
})
