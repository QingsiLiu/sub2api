<template>
  <section class="card space-y-4 p-5">
    <h3 class="text-lg font-semibold">{{ t('admin.accounts.keyIntake.connectionTitle') }}</h3>
    <p class="text-sm text-gray-500">{{ t('admin.accounts.keyIntake.linkControl') }}</p>
    <p v-if="message" role="status" class="text-sm">{{ message }}</p>
    <template v-if="state">
      <SubmissionProxyEditorGeili v-model="input" :has-preset="state.has_preset" :has-password="state.has_password" :disabled="busy" />
      <button class="btn btn-primary" :disabled="busy" @click="save">{{ t('common.save') }}</button>
    </template>
    <button class="btn btn-secondary" :disabled="busy" @click="load">{{ t('admin.accounts.keyIntake.refreshUsage') }}</button>
  </section>
</template>
<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import SubmissionProxyEditorGeili from './SubmissionProxyEditorGeili.vue'
import { getSubmissionConnection, updateSubmissionConnection, SubmissionRequestError, type SubmissionConnection, type SubmissionConnectionInput } from '@/api/accountSubmission'
const props = defineProps<{ token: string; accountRef?: string }>()
const { t } = useI18n()
const state = ref<SubmissionConnection | null>(null), input = ref<SubmissionConnectionInput>({ mode: 'direct' }), busy = ref(false), message = ref('')
let epoch = 0
// One request ID per attempt; an uncertain save is confirmed by refreshing, never replayed with new secrets.
function accept(value: SubmissionConnection) { state.value = value; input.value = { mode: value.mode, ...(value.proxy ? { proxy: { ...value.proxy, password: '' } } : {}) } }
async function load() {
  if (busy.value) return
  busy.value = true; const e = epoch
  try { const value = await getSubmissionConnection(props.token, props.accountRef); if (e === epoch) { accept(value); message.value = '' } }
  catch { if (e === epoch) message.value = t('admin.accounts.keyIntake.failed') }
  finally { if (e === epoch) busy.value = false }
}
async function save() {
  if (busy.value || !state.value) return
  busy.value = true; const e = epoch
  try { const value = await updateSubmissionConnection(props.token, state.value.version, crypto.randomUUID(), input.value, props.accountRef); if (e === epoch) { accept(value); message.value = t('admin.accounts.keyIntake.connectionSaved') } }
  catch (error) { if (e === epoch) { input.value = { mode: 'direct' }; state.value = null; message.value = t(error instanceof SubmissionRequestError && error.status === 409 ? 'admin.accounts.keyIntake.connectionConflict' : 'admin.accounts.keyIntake.connectionUncertain') } }
  finally { if (e === epoch) busy.value = false }
}
watch(() => [props.token, props.accountRef], () => { epoch++; input.value = { mode: 'direct' }; state.value = null; busy.value = false; message.value = ''; void load() }, { immediate: true })
onBeforeUnmount(() => { epoch++; input.value = { mode: 'direct' }; state.value = null })
</script>
