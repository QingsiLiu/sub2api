<template>
  <form class="space-y-4" @submit.prevent="submit">
    <p class="text-sm text-gray-500">{{ t('admin.accounts.keyIntake.batchHint', { max: MAX_KEYS }) }}</p>
    <div class="grid gap-3 sm:grid-cols-2">
      <label class="input-label">{{ t('admin.accounts.keyIntake.batchName') }}<input v-model="name" class="input mt-1 w-full" maxlength="95" required :disabled="busy || uncertain" /></label>
      <label class="input-label">{{ t('admin.accounts.keyIntake.batchConcurrency') }}<input v-model.number="concurrency" class="input mt-1 w-full" type="number" min="1" max="1000" required :disabled="busy || uncertain" /></label>
    </div>
    <div class="max-h-80 space-y-2 overflow-y-auto">
      <div v-for="(row, index) in rows" :key="row.id" class="flex items-center gap-2">
        <label :for="`batch-key-${row.id}`" class="w-8 shrink-0 text-sm text-gray-500">{{ index + 1 }}</label>
        <input :id="`batch-key-${row.id}`" v-model="row.key" type="password" class="input min-w-0 flex-1" :aria-label="t('admin.accounts.keyIntake.batchKey', { n: index + 1 })" autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="2048" :disabled="busy || uncertain" @paste="paste($event, index)" />
        <button type="button" class="btn btn-secondary shrink-0" :aria-label="t('admin.accounts.keyIntake.batchRemove', { n: index + 1 })" :disabled="busy || uncertain || rows.length === 1" @click="remove(index)">×</button>
      </div>
    </div>
    <button type="button" class="btn btn-secondary" :disabled="busy || uncertain || rows.length >= MAX_KEYS" @click="rows.push(newRow())">{{ t('admin.accounts.keyIntake.batchAdd') }}</button>
    <SubmissionProxyEditorGeili v-model="connection" :has-preset="!!invite.has_preset_proxy" :disabled="busy || uncertain" />
    <p v-if="message" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ message }}</p>
    <button v-if="uncertain" type="button" class="btn btn-secondary w-full" :disabled="busy" @click="confirm">{{ t('admin.accounts.keyIntake.batchConfirm') }}</button>
    <button v-else class="btn btn-primary w-full" :disabled="busy">{{ t('admin.accounts.keyIntake.batchSubmit', { count: keys.length }) }}</button>
  </form>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import SubmissionProxyEditorGeili from './SubmissionProxyEditorGeili.vue'
import { inspectSubmissionInvite, submitAccountKeyBatch, SubmissionRequestError, type PublicSubmissionInvite, type SubmissionConnectionInput } from '@/api/accountSubmission'
const props = defineProps<{ token: string; invite: PublicSubmissionInvite }>()
const emit = defineEmits<{ created: [token: string] }>()
const { t } = useI18n()
const MAX_KEYS = 100
let nextID = 0, epoch = 0
const newRow = (key = '') => ({ id: nextID++, key })
const rows = ref([newRow()])
const name = ref(props.invite.name || '')
const concurrency = ref(props.invite.concurrency || 1)
const connection = ref<SubmissionConnectionInput>({ mode: props.invite.has_preset_proxy ? 'preset' : 'direct' })
const busy = ref(false), uncertain = ref(false), message = ref('')
const keys = computed(() => rows.value.map(row => row.key.trim()).filter(Boolean))
function clearSecrets() { rows.value = [newRow()]; connection.value = { mode: 'direct' } }
function remove(index: number) { rows.value[index].key = ''; rows.value.splice(index, 1) }
function paste(event: ClipboardEvent, index: number) {
  const text = event.clipboardData?.getData('text') || ''
  if (!/[\r\n]/.test(text)) return
  event.preventDefault()
  const values = text.split(/\r\n|\n|\r/).map(value => value.trim()).filter(Boolean)
  if (!values.length) return
  if (rows.value.length - 1 + values.length > MAX_KEYS || values.some(value => value.length > 2048 || /\s/.test(value))) {
    message.value = t('admin.accounts.keyIntake.batchInvalid'); return
  }
  rows.value[index].key = ''
  rows.value.splice(index, 1, ...values.map(newRow))
  message.value = ''
}
async function confirm() {
  if (busy.value) return
  busy.value = true
  const e = epoch, token = props.token
  try {
    const state = await inspectSubmissionInvite(token)
    if (e !== epoch) return
    if (state.status === 'submitted' && state.view_available !== false) { clearSecrets(); emit('created', token) }
    else { uncertain.value = false; message.value = t('admin.accounts.keyIntake.batchRetry') }
  } catch { if (e === epoch) message.value = t('admin.accounts.keyIntake.batchUncertain') }
  finally { if (e === epoch) busy.value = false }
}
async function submit() {
  if (busy.value || uncertain.value) return
  const values = keys.value
  if (!values.length || values.length > MAX_KEYS || new Set(values).size !== values.length || values.some(key => key.length > 2048 || /[\s\p{Cc}]/u.test(key))) {
    message.value = t('admin.accounts.keyIntake.batchInvalid'); return
  }
  busy.value = true; message.value = ''
  const e = epoch, token = props.token
  try {
    await submitAccountKeyBatch(token, values, { name: name.value, concurrency: concurrency.value, connection: connection.value })
    if (e === epoch) { clearSecrets(); emit('created', token) }
  } catch (cause) {
    if (e !== epoch) return
    clearSecrets()
    const status = cause instanceof SubmissionRequestError ? cause.status : 0
    uncertain.value = status === 0 || status >= 500
    message.value = t(uncertain.value ? 'admin.accounts.keyIntake.batchUncertain' : status === 429 ? 'admin.accounts.keyIntake.rateLimited' : 'admin.accounts.keyIntake.batchFailed')
  } finally { if (e === epoch) busy.value = false }
}
onBeforeUnmount(() => { epoch++; clearSecrets() })
</script>
