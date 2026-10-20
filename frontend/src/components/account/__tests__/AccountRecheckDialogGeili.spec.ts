import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import AccountRecheckDialogGeili from '../AccountRecheckDialogGeili.vue'
import type { Account } from '@/types'

const { recheck } = vi.hoisted(() => ({ recheck: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { recheck } } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const BaseDialog = defineComponent({ props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' })
const ConfirmDialog = defineComponent({ props: ['show'], emits: ['confirm', 'cancel'], template: '<div v-if="show" data-test="confirm"><button @click="$emit(\'confirm\')">confirm</button><button @click="$emit(\'cancel\')">cancel</button></div>' })
const account = (id = 1, blocked = false) => ({ id, name: `account-${id}`, runtime_state: { state: blocked ? 'balance_stopped' : 'stopped', balance_blocked: blocked, stop_reason: 'manual' } } as Account)
const open = (blocked = false) => mount(AccountRecheckDialogGeili, { props: { show: true, account: account(1, blocked) }, global: { stubs: { BaseDialog, ConfirmDialog } } })

describe('AccountRecheckDialogGeili', () => {
  it('requires an explicit model and sends no default or arbitrary prompt', async () => {
    recheck.mockReset().mockResolvedValue(account())
    const wrapper = open()
    expect(wrapper.find('button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.find('input').setValue(' claude-opus-5 ')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(recheck).toHaveBeenCalledWith(1, { model: 'claude-opus-5' })
    expect(wrapper.emitted('checked')).toEqual([[account()]])
    wrapper.unmount()
  })
  it('allows recheck without clearing the independent unpaid label', async () => {
    recheck.mockReset().mockResolvedValue(account())
    const wrapper = open(true)
    await wrapper.find('input').setValue('gpt-6')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(recheck).toHaveBeenCalledWith(1, { model: 'gpt-6' })
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="confirm"]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('keeps the failed account untouched and allows another explicit check', async () => {
    recheck.mockReset().mockRejectedValue(new Error('unhealthy'))
    const wrapper = open()
    await wrapper.find('input').setValue('gpt-6')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.emitted('checked')).toBeUndefined()
    expect(wrapper.find('[role="alert"]').text()).toBe('unhealthy')
    expect(wrapper.find('button[type="submit"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
  it('ignores a late result after switching accounts', async () => {
    let finish!: (value: Account) => void
    recheck.mockReset().mockImplementation(() => new Promise<Account>(resolve => { finish = resolve }))
    const wrapper = open()
    await wrapper.find('input').setValue('gpt-6')
    await wrapper.find('form').trigger('submit')
    await wrapper.setProps({ account: account(2) })
    finish(account(1))
    await flushPromises()
    expect(wrapper.emitted('checked')).toBeUndefined()
    expect(wrapper.find('input').element.value).toBe('')
    wrapper.unmount()
  })
})
