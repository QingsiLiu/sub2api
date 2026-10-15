<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatCard :title="t('supplier.usage.stats.requests')" :value="formatNumber(stats.total_requests)" />
        <StatCard :title="t('supplier.usage.stats.tokens')" :value="formatTokensK(stats.total_tokens)" />
        <StatCard :title="t('supplier.usage.stats.accountCost')" :value="'$' + formatCostFixed(stats.total_account_cost)" />
        <StatCard :title="t('supplier.usage.stats.avgDuration')" :value="formatDuration(stats.average_duration_ms)" />
      </div>

      <TablePageLayout>
        <template #filters>
          <div class="space-y-3">
            <div class="flex flex-wrap items-center gap-3">
              <DateRangePicker v-model:start-date="startDate" v-model:end-date="endDate" @change="reload" />
              <Select v-model="accountFilter" :options="accountOptions" class="w-56" @change="reload" />
              <div class="sm:max-w-56">
                <input v-model="modelFilter" type="text" class="input" :placeholder="t('supplier.usage.modelPlaceholder')" @input="debouncedReload" />
              </div>
              <div class="flex flex-1 justify-end">
                <button class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="loadAll">
                  <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
                </button>
              </div>
            </div>
            <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('supplier.usage.costHint') }}</p>
          </div>
        </template>

        <template #table>
          <DataTable :columns="columns" :data="logs" :loading="loading">
            <template #cell-created_at="{ value }">
              <span class="text-sm text-gray-600 dark:text-gray-300">{{ formatDateTime(value) }}</span>
            </template>
            <template #cell-account_name="{ row }">
              <span class="text-sm text-gray-900 dark:text-white">{{ row.account_name || '#' + row.account_id }}</span>
            </template>
            <template #cell-model="{ row }">
              <div class="flex items-center gap-2">
                <span class="font-mono text-sm text-gray-700 dark:text-gray-300">{{ row.model }}</span>
                <span v-if="row.stream" class="badge badge-gray">{{ t('supplier.usage.stream') }}</span>
              </div>
            </template>
            <template #cell-input_tokens="{ value }">
              <span class="font-mono text-sm">{{ formatNumber(value) }}</span>
            </template>
            <template #cell-output_tokens="{ value }">
              <span class="font-mono text-sm">{{ formatNumber(value) }}</span>
            </template>
            <template #cell-cache="{ row }">
              <span class="font-mono text-sm text-gray-500 dark:text-dark-400">
                {{ formatNumber(row.cache_read_tokens) }} / {{ formatNumber(row.cache_creation_tokens) }}
              </span>
            </template>
            <template #cell-account_cost="{ value }">
              <span class="font-mono text-sm text-gray-900 dark:text-white">${{ formatCostFixed(value, 6) }}</span>
            </template>
            <template #cell-duration_ms="{ row }">
              <span class="font-mono text-sm text-gray-500 dark:text-dark-400">{{ formatDuration(row.duration_ms) }}</span>
            </template>
            <template #empty>
              <EmptyState :title="t('empty.noData')" />
            </template>
          </DataTable>
        </template>

        <template #pagination>
          <Pagination
            v-if="pagination.total > 0"
            :page="pagination.page"
            :total="pagination.total"
            :page-size="pagination.page_size"
            @update:page="handlePageChange"
            @update:pageSize="handlePageSizeChange"
          />
        </template>
      </TablePageLayout>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import {
  supplierAPI,
  type SupplierAccount,
  type SupplierUsageLog,
  type SupplierUsageQuery,
  type SupplierUsageStats
} from '@/api/supplier'
import { formatCostFixed, formatDateLocalInput, formatDateTime, formatNumber, formatTokensK } from '@/utils/format'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Column } from '@/components/common/types'

import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Select from '@/components/common/Select.vue'
import StatCard from '@/components/common/StatCard.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()

const today = new Date()
const weekAgo = new Date(today)
weekAgo.setDate(today.getDate() - 6)
const startDate = ref(formatDateLocalInput(weekAgo))
const endDate = ref(formatDateLocalInput(today))
const accountFilter = ref<number | ''>('')
const modelFilter = ref('')

const logs = ref<SupplierUsageLog[]>([])
const accounts = ref<SupplierAccount[]>([])
const loading = ref(false)
const pagination = reactive({ page: 1, page_size: getPersistedPageSize(), total: 0 })
const emptyStats = (): SupplierUsageStats => ({
  total_requests: 0,
  total_input_tokens: 0,
  total_output_tokens: 0,
  total_cache_creation_tokens: 0,
  total_cache_read_tokens: 0,
  total_tokens: 0,
  total_account_cost: 0,
  average_duration_ms: 0
})
const stats = ref<SupplierUsageStats>(emptyStats())

const columns = computed<Column[]>(() => [
  { key: 'created_at', label: t('supplier.usage.columns.time') },
  { key: 'account_name', label: t('supplier.usage.columns.account') },
  { key: 'model', label: t('supplier.usage.columns.model') },
  { key: 'input_tokens', label: t('supplier.usage.columns.input') },
  { key: 'output_tokens', label: t('supplier.usage.columns.output') },
  { key: 'cache', label: t('supplier.usage.columns.cache') },
  { key: 'account_cost', label: t('supplier.usage.columns.accountCost') },
  { key: 'duration_ms', label: t('supplier.usage.columns.duration') }
])

const accountOptions = computed(() => [
  { value: '', label: t('supplier.usage.allAccounts') },
  ...accounts.value.map((a) => ({ value: a.id, label: `${a.name} (#${a.id})` }))
])

function formatDuration(ms: number | null | undefined): string {
  if (!ms) return '-'
  return ms >= 1000 ? `${(ms / 1000).toFixed(2)}s` : `${Math.round(ms)}ms`
}

function query(): SupplierUsageQuery {
  return {
    start_date: startDate.value,
    end_date: endDate.value,
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    account_id: accountFilter.value === '' ? undefined : Number(accountFilter.value),
    model: modelFilter.value.trim() || undefined
  }
}

let seq = 0
async function loadAll() {
  const current = ++seq
  loading.value = true
  const q = query()
  try {
    const [page, summary] = await Promise.all([
      supplierAPI.listUsage({ ...q, page: pagination.page, page_size: pagination.page_size }),
      supplierAPI.getUsageStats(q)
    ])
    if (current !== seq) return
    logs.value = page.items
    pagination.total = page.total
    stats.value = summary
  } catch (error) {
    if (current === seq) appStore.showError(extractApiErrorMessage(error, t('supplier.usage.failedToLoad')))
  } finally {
    if (current === seq) loading.value = false
  }
}

async function loadPage() {
  const current = ++seq
  loading.value = true
  try {
    const page = await supplierAPI.listUsage({ ...query(), page: pagination.page, page_size: pagination.page_size })
    if (current !== seq) return
    logs.value = page.items
    pagination.total = page.total
  } catch (error) {
    if (current === seq) appStore.showError(extractApiErrorMessage(error, t('supplier.usage.failedToLoad')))
  } finally {
    if (current === seq) loading.value = false
  }
}

function reload() {
  pagination.page = 1
  void loadAll()
}

let modelTimer: ReturnType<typeof setTimeout> | undefined
function debouncedReload() {
  if (modelTimer) clearTimeout(modelTimer)
  modelTimer = setTimeout(reload, 400)
}

function handlePageChange(page: number) {
  pagination.page = page
  void loadPage()
}

function handlePageSizeChange(size: number) {
  pagination.page_size = size
  pagination.page = 1
  void loadPage()
}

onMounted(async () => {
  void loadAll()
  try {
    accounts.value = (await supplierAPI.listAccounts(1, 100)).items
  } catch {
    accounts.value = []
  }
})

onUnmounted(() => {
  if (modelTimer) clearTimeout(modelTimer)
})
</script>
