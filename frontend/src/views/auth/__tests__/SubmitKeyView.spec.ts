import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const { inspect } = vi.hoisted(() => ({ inspect: vi.fn() }))
vi.mock('@/api/accountSubmission', async () => ({...(await vi.importActual<object>('@/api/accountSubmission')),inspectSubmissionInvite: inspect}))
vi.mock('@/api/client', () => ({ apiClient: {} }))
vi.mock('vue-i18n', async () => ({...(await vi.importActual<object>('vue-i18n')),useI18n: () => ({t: (key: string) => key})}))
import SubmitKeyView from '../SubmitKeyView.vue'
import { SubmissionRequestError } from '@/api/accountSubmission'
const Form = defineComponent({name:'SubmissionAccountFormGeili',props:['token','invite'],emits:['created'],template:'<button @click="$emit(\'created\', token)">Complete official form</button>'})
const render = () => mount(SubmitKeyView,{global:{stubs:{AuthLayout:{template:'<main><slot /></main>'},SubmissionAccountFormGeili:Form,SubmissionBatchFormGeili:Form,AccountSubmissionUsage:true,SubmissionConnectionGeili:true}}})
const switchLink = () => {window.history.replaceState({},'', '/submit-key#token=second-token');window.dispatchEvent(new HashChangeEvent('hashchange'))}
beforeEach(() => {vi.clearAllMocks();window.history.replaceState({},'', '/submit-key#token=synthetic-token');inspect.mockResolvedValue({platform:'anthropic',status:'pending',name:'Holder',concurrency:1})})
afterEach(() => window.history.replaceState({},'', '/'))
describe('invitation page delegates to the full official form', () => {
 it('passes the capability and defaults, then opens usage and proxy management', async () => {
  const w=render();await flushPromises();expect(w.getComponent(Form).props('token')).toBe('synthetic-token');expect(w.getComponent(Form).props('invite').name).toBe('Holder')
  await w.getComponent(Form).get('button').trigger('click');await flushPromises();expect(w.text()).toContain('keyIntake.success');expect(w.findComponent(Form).exists()).toBe(false);expect(window.location.hash).toContain('synthetic-token');w.unmount()
 })
 it('a revoked invitation cannot mount the credential form', async () => {
  inspect.mockRejectedValueOnce(new SubmissionRequestError(410));const w=render();await flushPromises();expect(w.findComponent(Form).exists()).toBe(false);expect(w.text()).toContain('keyIntake.invalid');w.unmount()
 })
 it('an old consumed link opens usage without mounting the form', async () => {
  inspect.mockResolvedValueOnce({platform:'openai',status:'submitted'});const w=render();await flushPromises();expect(w.findComponent(Form).exists()).toBe(false);expect(w.text()).toContain('keyIntake.success');w.unmount()
 })
 it('ignores an inspection for the old capability after fragment navigation', async () => {
  let complete!: (state: unknown)=>void;inspect.mockReturnValueOnce(new Promise(resolve=>{complete=resolve}));const w=render();switchLink();await flushPromises();complete({status:'submitted',platform:'anthropic'});await flushPromises();expect(w.getComponent(Form).props('token')).toBe('second-token');expect(w.text()).not.toContain('keyIntake.success');w.unmount()
 })
 it('ignores an old credential form completing after fragment navigation', async () => {
  const w=render();await flushPromises();const old=w.getComponent(Form);switchLink();await flushPromises();old.vm.$emit('created','synthetic-token');await flushPromises();expect(w.getComponent(Form).props('token')).toBe('second-token');expect(w.text()).not.toContain('keyIntake.success');w.unmount()
 })
})
