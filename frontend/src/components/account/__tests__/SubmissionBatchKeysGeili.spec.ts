import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
vi.mock('@/api/client', () => ({ apiClient: {} }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
import Component from '../SubmissionBatchKeysGeili.vue'
import ProxyEditor from '../SubmissionProxyEditorGeili.vue'
const render = () => mount(Component, { props: { hasPreset: false }, global: { stubs: { SubmissionProxyEditorGeili: true } } })
const paste = async (w: ReturnType<typeof render>, text: string) => w.get('input[type=password]').trigger('paste', { clipboardData: { getData: () => text } })
describe('advanced form key rows', () => {
 it('pastes masked keys, reports the first key to the shared form and wipes on clear', async () => {
  const w=render();await paste(w,'synthetic-a\r\n synthetic-b \n\nsynthetic-c')
  expect(w.findAll('input[type=password]')).toHaveLength(3)
  expect(w.vm.items().map(i=>i.api_key)).toEqual(['synthetic-a','synthetic-b','synthetic-c'])
  expect(w.emitted('primary-key')?.at(-1)).toEqual(['synthetic-a'])
  w.vm.clear();expect(()=>w.vm.items()).toThrow();w.unmount()
 })
 it('keeps key/proxy pairs after blank rows, deletion and paste without copying proxy passwords', async () => {
  const w=render();await paste(w,'synthetic-a\nsynthetic-empty\nsynthetic-c')
  const p={name:'A',protocol:'http',host:'8.8.8.8',port:8080,username:'',password:'synthetic-password-a'}
  const editors=w.findAllComponents(ProxyEditor);editors[0].vm.$emit('update:modelValue',{mode:'custom',proxy:p});editors[2].vm.$emit('update:modelValue',{mode:'custom',proxy:{...p,host:'1.1.1.1'}})
  await w.findAll('input[type=password]')[1].setValue('')
  expect(w.vm.items().map(i=>[i.api_key,i.connection.proxy?.host])).toEqual([['synthetic-a','8.8.8.8'],['synthetic-c','1.1.1.1']]);expect(w.vm.connection().proxy?.host).toBe('8.8.8.8')
  await paste(w,'synthetic-d\nsynthetic-e')
  expect(w.findAllComponents(ProxyEditor)[0].props('modelValue')).toEqual({mode:'custom',proxy:p})
  expect(w.findAllComponents(ProxyEditor)[1].props('modelValue')).toEqual({mode:'direct'})
  await w.findAll('button').find(b=>b.attributes('aria-label')?.includes('batchRemove'))!.trigger('click')
  expect(w.vm.items().map(i=>[i.api_key,i.connection.proxy?.host])).toEqual([['synthetic-e',undefined],['synthetic-c','1.1.1.1']]);w.unmount()
 })
 it('rejects duplicate keys and more than100 pasted rows', async () => {
  const w=render();await paste(w,'synthetic-a\nsynthetic-a');expect(()=>w.vm.items()).toThrow()
  await paste(w,Array.from({length:101},(_,n)=>`synthetic-${n}`).join('\n'))
  expect(w.findAll('input[type=password]')).toHaveLength(2);expect(w.text()).toContain('batchInvalid');w.unmount()
 })
})
