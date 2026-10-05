<template>
  <div class="space-y-4 p-4" data-testid="response-audit-panel">
    <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('responseAudit.observationHint') }}</p>
    <form class="flex flex-wrap items-end gap-3" @submit.prevent="refresh">
      <label class="text-xs">{{ t('responseAudit.range') }}<select v-model="hours" class="input mt-1"><option :value="24">24h</option><option :value="72">72h</option><option :value="168">7d</option></select></label>
      <label class="text-xs">{{ t('responseAudit.result') }}<select v-model="status" class="input mt-1"><option value="">{{ t('common.all') }}</option><option v-for="s in statuses" :key="s" :value="s">{{ t(`responseAudit.status.${s}`) }}</option></select></label>
      <label v-for="field in fields" :key="field" class="text-xs">{{ t(`responseAudit.fields.${field}`) }}<input v-model="filters[field]" class="input mt-1 w-36" :maxlength="field === 'model' ? 100 : 64" /></label>
      <button type="submit" class="btn btn-primary" :disabled="loading">{{ t('common.refresh') }}</button>
    </form>
    <p v-if="error" role="alert" class="text-sm text-rose-600">{{ error }}</p>
    <div v-if="stats" class="space-y-2">
      <div class="grid grid-cols-2 gap-3 lg:grid-cols-5">
        <div v-for="s in statuses" :key="s" class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
          <div class="text-xs text-gray-500">{{ t(`responseAudit.status.${s}`) }}</div>
          <div class="text-xl font-semibold">{{ stats.counts[s] }} <span class="text-xs font-normal text-gray-500">{{ percent(stats.counts[s]) }}%</span></div>
        </div>
      </div>
      <p class="text-xs text-gray-500">{{ t('responseAudit.summary', { total: stats.total, missing: stats.missing_terminal, failed: stats.write_failed, charged: stats.empty_charged_receipts }) }}</p>
      <p class="text-xs text-gray-500">{{ t('responseAudit.coverage', { start: stats.observation_started_at || '—', dropped: stats.dropped_since_start, failed: stats.write_failures_since_start, process: stats.process_started_at }) }}</p>
    </div>
    <DataTable :columns="columns" :data="rows" :loading="loading">
      <template #cell-status="{ row }"><ResponseAuditBadge :audit="row" /></template>
      <template #cell-model="{ row }"><div class="break-all text-xs">{{ row.model || '—' }}<div class="text-gray-500">{{ row.endpoint }} · {{ row.protocol }} · #{{ row.turn }}</div></div></template>
      <template #cell-identity="{ row }"><span class="text-xs">{{ row.user_id }} / {{ row.api_key_id }} / {{ row.account_id || '—' }}</span></template>
      <template #cell-cost="{ row }"><span class="text-xs">{{ t(`responseAudit.settlement.${row.settlement_state}`) }}<span v-if="row.charged_amount != null"> · ${{ row.charged_amount }}</span></span></template>
      <template #cell-action="{ row }"><button type="button" class="text-sm text-primary-600" @click="openDetail(row.id)">{{ t('responseAudit.details') }}</button></template>
    </DataTable>
    <Pagination v-if="total > 0" :page="page" :page-size="50" :total="total" @update:page="changePage" />
    <BaseDialog :show="detail != null" :title="t('responseAudit.details')" width="wide" @close="detail = null">
      <div v-if="detail" class="space-y-4">
        <ResponseAuditBadge :audit="detail" />
        <p class="text-xs text-gray-500">{{ t('responseAudit.writeHint') }}</p>
        <dl class="grid grid-cols-1 gap-2 text-xs sm:grid-cols-2">
          <div v-for="field in detailFields" :key="field" class="break-all"><dt class="text-gray-500">{{ t(`responseAudit.fields.${field}`) }}</dt><dd>{{ formatValue(detail[field]) }}</dd></div>
        </dl>
        <p class="text-xs">{{ t(`responseAudit.settlement.${detail.settlement_state}`) }} · {{ t('responseAudit.receipt') }} #{{ detail.receipt_id ?? '—' }} · {{ detail.charged_amount == null ? '—' : '$' + detail.charged_amount }}</p>
        <button class="btn btn-secondary" :disabled="logsLoading" @click="loadLogs">{{ t('responseAudit.linkedLogs') }}</button>
        <p v-if="logsError" class="text-xs text-rose-600">{{ logsError }}</p>
        <div v-for="log in logs" :key="log.id" class="break-all text-xs"><span class="text-gray-500">{{ log.created_at }}</span> · {{ log.message }}</div>
        <button v-for="entry in errors" :key="entry.id" class="block text-left text-xs text-primary-600" @click="$emit('openError', entry.id)">#{{ entry.id }} · {{ entry.status_code }} · {{ entry.phase }} · {{ entry.request_id }}</button>
        <p v-if="logsLoaded && !logs.length && !errors.length" class="text-xs text-gray-500">{{ t('responseAudit.noLinkedLogs') }}</p>
      </div>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ResponseAuditBadge from './ResponseAuditBadge.vue'
import { responseAuditAPI, type ResponseAudit, type ResponseAuditStats, type ResponseAuditStatus, type ResponseAuditQuery } from '@/api/admin/responseAudit'
import { listSystemLogs, listRequestErrors, type OpsSystemLog, type OpsErrorLog } from '@/api/admin/ops'

defineEmits<{ openError: [id: number] }>()
const { t } = useI18n()
const statuses: ResponseAuditStatus[] = ['success', 'partial_failure', 'empty', 'failed', 'unknown']
const fields = ['user_id', 'api_key_id', 'account_id', 'model', 'request_id'] as const
const filters = ref<Record<typeof fields[number], string>>({ user_id: '', api_key_id: '', account_id: '', model: '', request_id: '' })
const hours = ref(24), status = ref<ResponseAuditStatus | ''>(''), page = ref(1), total = ref(0), loading = ref(false), error = ref('')
const rows = ref<ResponseAudit[]>([]), stats = ref<ResponseAuditStats | null>(null), detail = ref<ResponseAudit | null>(null)
const logs = ref<OpsSystemLog[]>([]), errors = ref<OpsErrorLog[]>([]), logsLoading = ref(false), logsLoaded = ref(false), logsError = ref('')
const detailFields = ['request_id', 'client_request_id', 'usage_request_id', 'upstream_request_id', 'started_at', 'finished_at', 'http_status', 'upstream_content_seen', 'text_written', 'reasoning_written', 'complete_tool_written', 'upstream_terminal', 'terminal', 'terminal_written', 'write_failed', 'client_disconnected', 'usage_present', 'first_output_ms'] as const
const columns = computed(() => [
  { key: 'finished_at', label: t('responseAudit.fields.finished_at') },
  { key: 'status', label: t('responseAudit.result') },
  { key: 'model', label: t('responseAudit.fields.model') },
  { key: 'identity', label: t('responseAudit.identities') },
  { key: 'cost', label: t('responseAudit.existingSettlement') },
  { key: 'action', label: t('responseAudit.details') },
])
const percent = (n: number) => stats.value?.total ? (100 * n / stats.value.total).toFixed(1) : '0.0'
const formatValue = (v: unknown) => typeof v === 'boolean' ? t(v ? 'common.yes' : 'common.no') : v ?? '—'
let generation = 0
async function load() {
  const id = ++generation
  error.value = ''; loading.value = true
  try {
    const now = new Date()
    const query: ResponseAuditQuery = { from: new Date(now.getTime() - hours.value * 3600000).toISOString(), to: now.toISOString(), page: page.value, page_size: 50 }
    if (status.value) query.status = status.value
    for (const field of fields) {
      const value = filters.value[field].trim()
      if (!value) continue
      if (field === 'model' || field === 'request_id') query[field] = value
      else { if (!/^\d+$/.test(value) || Number(value) <= 0 || !Number.isSafeInteger(Number(value))) throw new Error(t('responseAudit.invalidId')); query[field] = Number(value) }
    }
    const [list, counts] = await Promise.all([responseAuditAPI.list(query), responseAuditAPI.stats(query)])
    if (id !== generation) return
    rows.value = list.items; total.value = list.total; stats.value = counts
  } catch (e) { if (id === generation) { error.value = e instanceof Error ? e.message : t('responseAudit.unavailable'); rows.value = []; stats.value = null; total.value = 0 } }
  finally { if (id === generation) loading.value = false }
}
function refresh() { page.value = 1; void load() }
function changePage(n: number) { page.value = n; void load() }
async function openDetail(id: number) {
  logs.value = []; errors.value = []; logsError.value = ''; logsLoaded.value = false
  try { detail.value = await responseAuditAPI.get(id) } catch { error.value = t('responseAudit.unavailable') }
}
async function loadLogs() {
  const target = detail.value
  if (!target) return
  logsLoading.value = true; logsError.value = ''
  const start = new Date(new Date(target.started_at).getTime() - 30000).toISOString()
  const end = new Date(new Date(target.finished_at).getTime() + 30000).toISOString()
  const results = await Promise.allSettled([
    listSystemLogs({ request_id: target.request_id, user_id: target.user_id, api_key_id: target.api_key_id, start_time: start, end_time: end, page: 1, page_size: 100 }),
    listRequestErrors({ q: target.request_id, user_id: target.user_id, api_key_id: target.api_key_id, start_time: start, end_time: end, page: 1, page_size: 100, view: 'all' }),
  ])
  if (detail.value?.id === target.id) {
    logs.value = results[0].status === 'fulfilled' ? results[0].value.items : []
    errors.value = results[1].status === 'fulfilled' ? results[1].value.items : []
    if (results.some(r => r.status === 'rejected')) logsError.value = t('responseAudit.logsUnavailable')
    logsLoaded.value = true
  }
  logsLoading.value = false
}
onMounted(load)
</script>
