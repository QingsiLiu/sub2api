<template>
  <div class="space-y-3">
    <p class="input-hint">{{ t('admin.accounts.keyIntake.advancedBatchHint', { max: MAX_KEYS }) }}</p>
    <section v-for="(row, index) in rows" :key="row.id" class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700" :aria-label="t('admin.accounts.keyIntake.batchKey', { n: index + 1 })">
      <div class="flex items-center gap-2">
        <label :for="`${id}-${row.id}`" class="shrink-0 text-sm font-medium">Key {{ index + 1 }}</label>
        <input :id="`${id}-${row.id}`" v-model="row.key" type="password" class="input min-w-0 flex-1" :aria-label="t('admin.accounts.keyIntake.batchKey', { n: index + 1 })" autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="2048" :disabled="disabled" @paste="paste($event, index)" />
        <button type="button" class="btn btn-secondary shrink-0" :aria-label="t('admin.accounts.keyIntake.batchRemove', { n: index + 1 })" :disabled="disabled || rows.length === 1" @click="remove(index)">×</button>
      </div>
      <SubmissionProxyEditorGeili v-model="row.connection" :has-preset="hasPreset" :disabled="disabled" />
    </section>
    <button type="button" class="btn btn-secondary" :disabled="disabled || rows.length >= MAX_KEYS" @click="rows.push(newRow())">{{ t('admin.accounts.keyIntake.batchAdd') }}</button>
    <p v-if="message" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ message }}</p>
  </div>
</template>
<script setup lang="ts">
import { onBeforeUnmount, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import SubmissionProxyEditorGeili from './SubmissionProxyEditorGeili.vue'
import { SubmissionRequestError, type SubmissionBatchItem, type SubmissionConnectionInput } from '@/api/accountSubmission'
const props = defineProps<{ hasPreset: boolean; disabled?: boolean }>()
const emit = defineEmits<{ 'primary-key': [key: string] }>()
const { t } = useI18n()
const id = useId(), MAX_KEYS = 100
let nextID = 0
const defaultConnection = (): SubmissionConnectionInput => ({ mode: props.hasPreset ? 'preset' : 'direct' })
const newRow = (key = '') => ({ id: nextID++, key, connection: defaultConnection() })
const rows = ref([newRow()]), message = ref('')
watch(rows, () => emit('primary-key', rows.value.find(row => row.key.trim())?.key.trim() || ''), { deep: true, immediate: true })
function clear() {
  for (const row of rows.value) { row.key = ''; row.connection = defaultConnection() }
  rows.value = [newRow()]; message.value = ''
}
function remove(index: number) { rows.value[index].key = ''; rows.value[index].connection = defaultConnection(); rows.value.splice(index, 1) }
function paste(event: ClipboardEvent, index: number) {
  const text = event.clipboardData?.getData('text') || ''
  if (!/[\r\n]/.test(text)) return
  event.preventDefault()
  const values = text.split(/\r\n|\n|\r/).map(value => value.trim()).filter(Boolean)
  if (!values.length) return
  if (rows.value.length - 1 + values.length > MAX_KEYS || values.some(value => value.length > 2048 || /[\s\p{Cc}]/u.test(value))) {
    message.value = t('admin.accounts.keyIntake.batchInvalid'); return
  }
  const first = { ...rows.value[index], key: values[0] }
  rows.value[index].key = ''
  rows.value.splice(index, 1, first, ...values.slice(1).map(newRow)); message.value = ''
}
function items(): SubmissionBatchItem[] {
  const values = rows.value.filter(row => row.key.trim())
  const keys = values.map(row => row.key.trim())
  if (!keys.length || keys.length > MAX_KEYS || new Set(keys).size !== keys.length || keys.some(key => key.length > 2048 || /[\s\p{Cc}]/u.test(key))) {
    message.value = t('admin.accounts.keyIntake.batchInvalid'); throw new SubmissionRequestError(400)
  }
  return values.map(row => ({ api_key: row.key.trim(), connection: { ...row.connection, ...(row.connection.proxy ? { proxy: { ...row.connection.proxy } } : {}) } }))
}
function connection(): SubmissionConnectionInput {
  const row = rows.value.find(row => row.key.trim()) || rows.value[0]
  return { ...row.connection, ...(row.connection.proxy ? { proxy: { ...row.connection.proxy } } : {}) }
}
defineExpose({ items, clear, connection })
onBeforeUnmount(clear)
</script>
