<template>
  <section class="space-y-4" :aria-label="t('admin.accounts.keyIntake.usageTitle')">
    <h3 class="text-lg font-semibold">{{ t('admin.accounts.keyIntake.usageTitle') }}</h3>
    <p class="text-xs text-gray-500">{{ t('admin.accounts.keyIntake.viewHint') }}</p>
    <form class="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-end" @submit.prevent="load">
      <div class="min-w-0 sm:w-48">
        <label class="input-label" for="usage-period">{{ t('admin.accounts.keyIntake.period') }}</label>
        <select id="usage-period" v-model="period" class="input w-full" @change="setPeriod">
          <option value="1">{{ t('admin.accounts.keyIntake.today') }}</option>
          <option value="7">{{ t('admin.accounts.keyIntake.sevenDays') }}</option>
          <option value="30">{{ t('admin.accounts.keyIntake.thirtyDays') }}</option>
          <option value="custom">{{ t('admin.accounts.keyIntake.custom') }}</option>
        </select>
      </div>
      <div v-if="period === 'custom'" class="grid min-w-0 grid-cols-2 gap-2 sm:flex-1">
        <label class="min-w-0 text-xs">{{ t('admin.accounts.keyIntake.from') }}<input v-model="start" type="date" class="input w-full min-w-0" :max="end" required /></label>
        <label class="min-w-0 text-xs">{{ t('admin.accounts.keyIntake.to') }}<input v-model="end" type="date" class="input w-full min-w-0" :min="start" :max="beijingDate()" required /></label>
      </div>
      <button class="btn btn-secondary w-full sm:w-auto" :disabled="loading || unavailable">{{ t('admin.accounts.keyIntake.refreshUsage') }}</button>
    </form>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <p v-if="loading" role="status" class="text-sm text-gray-500">{{ t('admin.accounts.keyIntake.loadingUsage') }}</p>
    <template v-if="usage && !error">
      <p class="break-words text-sm font-medium">{{ usage.name }} · {{ usage.platform === 'openai' ? 'OpenAI' : 'Claude' }} · {{ t(usage.enabled ? 'admin.accounts.keyIntake.enabled' : 'admin.accounts.keyIntake.inactive') }}</p>
      <p class="text-xs text-gray-500">{{ usage.start_date }} — {{ usage.end_date }} · {{ usage.timezone }}</p>
      <dl class="grid grid-cols-2 gap-3 rounded-lg border border-gray-200 p-3 dark:border-dark-700 sm:grid-cols-3 xl:grid-cols-6">
        <div v-for="metric in metrics" :key="metric.label"><dt class="text-xs text-gray-500">{{ t(metric.label) }}</dt><dd class="break-all text-sm font-semibold">{{ metric.value }}</dd></div>
      </dl>
      <p v-if="usage.summary.requests === 0" class="text-sm text-gray-500">{{ t('admin.accounts.keyIntake.noUsage') }}</p>
      <TokenUsageTrend v-if="usage.summary.requests > 0" :trend-data="trend" />
      <h4 v-if="usage.models.length" class="text-sm font-semibold">{{ t('admin.accounts.keyIntake.models') }}</h4>
      <ul v-if="usage.models.length" class="grid gap-2 sm:grid-cols-2">
        <li v-for="model in usage.models" :key="model.model" class="rounded-lg border border-gray-200 p-2 text-xs dark:border-dark-700">
          <p class="break-all font-medium">{{ model.model }}</p>
          <p>{{ model.requests.toLocaleString() }} {{ t('admin.accounts.keyIntake.requests') }} · ${{ model.reference_cost.toFixed(6) }}</p>
        </li>
      </ul>
      <p class="text-xs text-gray-500">{{ t('admin.accounts.keyIntake.updated') }} {{ new Date(usage.updated_at).toLocaleString() }}</p>
    </template>
    <p class="text-xs text-gray-500">{{ t('admin.accounts.keyIntake.costNotice') }}</p>
  </section>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'
import { getSubmissionUsage, SubmissionRequestError, type SubmissionUsage } from '@/api/accountSubmission'
const props = defineProps<{ token: string }>()
const { t } = useI18n()
const usage = ref<SubmissionUsage | null>(null)
const error = ref('')
const loading = ref(false)
const unavailable = ref(false)
const period = ref('7')
const start = ref('')
const end = ref('')
let epoch = 0
let timer: ReturnType<typeof setInterval> | undefined
function beijingDate(offset = 0) {
  return new Date(Date.now() + 8 * 3600000 + offset * 86400000).toISOString().slice(0, 10)
}
function setPeriod() {
  if (period.value !== 'custom') { start.value = beijingDate(1 - Number(period.value)); end.value = beijingDate() }
  // Invalidate older responses before loading the new selection.
  epoch++; loading.value = false; usage.value = null
  if (period.value !== 'custom') void load()
}
async function load() {
  if (loading.value || unavailable.value) return
  const days = (Date.parse(end.value) - Date.parse(start.value)) / 86400000
  if (!start.value || !end.value || !Number.isFinite(days) || days < 0 || days >= 90 || end.value > beijingDate()) {
    error.value = t('admin.accounts.keyIntake.dateInvalid'); return
  }
  const version = ++epoch
  loading.value = true; error.value = ''
  try {
    const result = await getSubmissionUsage(props.token, start.value, end.value)
    if (version === epoch) usage.value = result
  } catch (cause) {
    if (version !== epoch) return
    usage.value = null
    const status = cause instanceof SubmissionRequestError ? cause.status : 0
    unavailable.value = status === 410
    error.value = t(status === 410 ? 'admin.accounts.keyIntake.viewUnavailable' : status === 429 ? 'admin.accounts.keyIntake.rateLimited' : 'admin.accounts.keyIntake.usageFailed')
  } finally { if (version === epoch) loading.value = false }
}
const metrics = computed(() => {
  const s = usage.value?.summary
  if (!s) return []
  return [
    ['requests', s.requests.toLocaleString()], ['referenceCost', `$${s.reference_cost.toFixed(6)}`],
    ['inputTokens', s.input_tokens.toLocaleString()], ['outputTokens', s.output_tokens.toLocaleString()],
    ['cacheCreation', s.cache_creation_tokens.toLocaleString()], ['cacheRead', s.cache_read_tokens.toLocaleString()]
  ].map(([label, value]) => ({ label: `admin.accounts.keyIntake.${label}`, value }))
})
const trend = computed(() => (usage.value?.daily || []).map(d => ({ ...d, date: d.date || '', total_tokens: d.input_tokens + d.output_tokens + d.cache_creation_tokens + d.cache_read_tokens, cost: d.reference_cost, actual_cost: 0 })))
watch(() => props.token, () => { epoch++; usage.value = null; unavailable.value = false; loading.value = false; setPeriod() }, { immediate: true })
onMounted(() => { timer = setInterval(() => { if (document.visibilityState === 'visible') { if (period.value !== 'custom') setPeriod(); else void load() } }, 60000) })
onBeforeUnmount(() => { epoch++; if (timer) clearInterval(timer); usage.value = null })
</script>
