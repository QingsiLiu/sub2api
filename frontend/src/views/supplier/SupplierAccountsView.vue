<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="space-y-3">
          <p class="rounded-lg bg-amber-50 px-4 py-2 text-sm text-amber-700 dark:bg-amber-900/20 dark:text-amber-300">
            {{ t('supplier.accounts.reviewNotice') }}
          </p>
          <div class="flex flex-wrap items-center gap-3">
            <div class="flex-1 sm:max-w-64">
              <input
                v-model="filters.search"
                type="text"
                class="input"
                :placeholder="t('supplier.accounts.searchPlaceholder')"
                @input="debouncedReload"
              />
            </div>
            <Select v-model="filters.platform" :options="platformFilterOptions" class="w-40" @change="reload" />
            <div class="flex flex-1 flex-wrap items-center justify-end gap-2">
              <button class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="load">
                <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
              </button>
              <button class="btn btn-primary" @click="openCreate">
                <Icon name="plus" size="md" class="mr-1" />
                {{ t('supplier.accounts.add') }}
              </button>
            </div>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="accounts" :loading="loading">
          <template #cell-name="{ row }">
            <div class="min-w-0">
              <div class="truncate font-medium text-gray-900 dark:text-white">{{ row.name }}</div>
              <div class="mt-0.5 flex items-center gap-2 text-xs text-gray-500 dark:text-dark-400">
                <span>#{{ row.id }}</span>
                <span class="font-mono">{{ row.api_key_hint }}</span>
              </div>
              <div v-if="row.notes" class="mt-0.5 max-w-xs truncate text-xs text-gray-500 dark:text-dark-400" :title="row.notes">{{ row.notes }}</div>
            </div>
          </template>

          <template #cell-platform="{ row }">
            <span class="text-sm text-gray-700 dark:text-gray-300">{{ platformLabel(row.platform) }}</span>
          </template>

          <template #cell-review_state="{ row }">
            <div class="flex flex-col gap-1">
              <span :class="['badge self-start', stateBadgeClass(row.review_state)]">
                {{ t('supplier.accounts.states.' + row.review_state) }}
              </span>
              <span
                v-if="row.review_state === 'error' && row.error_message"
                class="max-w-xs truncate text-xs text-red-500"
                :title="row.error_message"
              >{{ row.error_message }}</span>
            </div>
          </template>

          <template #cell-groups="{ row }">
            <span v-if="row.groups.length" class="text-sm text-gray-700 dark:text-gray-300">{{ row.groups.join(', ') }}</span>
            <span v-else class="text-sm text-gray-400 dark:text-dark-500">{{ t('supplier.accounts.noGroups') }}</span>
          </template>

          <template #cell-rate_multiplier="{ value }">
            <span class="font-mono text-sm text-gray-700 dark:text-gray-300">×{{ value }}</span>
          </template>

          <template #cell-last_used_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">{{ value ? formatDateTime(value) : '-' }}</span>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">{{ formatDateTime(value) }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <button class="action-btn hover:bg-blue-50 hover:text-blue-600 dark:hover:bg-blue-900/20" :title="t('supplier.accounts.test')" @click="openTest(row)">
                <Icon name="play" size="sm" />
                <span class="text-xs">{{ t('supplier.accounts.test') }}</span>
              </button>
              <button class="action-btn hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-600" :title="t('common.edit')" @click="openEdit(row)">
                <Icon name="edit" size="sm" />
                <span class="text-xs">{{ t('common.edit') }}</span>
              </button>
              <button
                v-if="row.schedulable"
                class="action-btn hover:bg-orange-50 hover:text-orange-600 dark:hover:bg-orange-900/20"
                :title="t('supplier.accounts.pause')"
                @click="pausingAccount = row"
              >
                <Icon name="ban" size="sm" />
                <span class="text-xs">{{ t('supplier.accounts.pause') }}</span>
              </button>
              <button class="action-btn hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20" :title="t('supplier.accounts.delete')" @click="deletingAccount = row">
                <Icon name="trash" size="sm" />
                <span class="text-xs">{{ t('supplier.accounts.delete') }}</span>
              </button>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('supplier.accounts.emptyTitle')"
              :description="t('supplier.accounts.emptyDescription')"
              :action-text="t('supplier.accounts.add')"
              @action="openCreate"
            />
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

    <!-- Create / Edit -->
    <BaseDialog
      :show="showForm"
      :title="editingAccount ? t('supplier.accounts.edit') : t('supplier.accounts.add')"
      width="normal"
      @close="closeForm"
    >
      <form id="supplier-account-form" class="space-y-4" @submit.prevent="save">
        <div>
          <label class="input-label">{{ t('supplier.accounts.form.platform') }}</label>
          <Select v-model="form.platform" :options="platformOptions" :disabled="!!editingAccount" />
        </div>
        <div>
          <label class="input-label">{{ t('supplier.accounts.form.name') }}</label>
          <input v-model="form.name" type="text" maxlength="100" class="input" required />
        </div>
        <div>
          <label class="input-label">{{ t('supplier.accounts.form.apiKey') }}</label>
          <input
            v-model="form.api_key"
            type="password"
            autocomplete="new-password"
            class="input font-mono"
            :required="!editingAccount"
          />
          <p v-if="editingAccount" class="input-hint">
            {{ t('supplier.accounts.form.apiKeyEditHint', { hint: editingAccount.api_key_hint }) }}
          </p>
        </div>
        <div>
          <label class="input-label">{{ t('supplier.accounts.form.baseUrl') }}</label>
          <input v-model="form.base_url" type="url" class="input font-mono" placeholder="https://" />
          <p class="input-hint">{{ t('supplier.accounts.form.baseUrlHint') }}</p>
        </div>
        <div>
          <label class="input-label">{{ t('supplier.accounts.form.concurrency') }}</label>
          <input v-model.number="form.concurrency" type="number" min="1" max="1000" step="1" class="input" required />
          <p class="input-hint">{{ t('supplier.accounts.form.concurrencyHint') }}</p>
        </div>
        <div>
          <label class="input-label">{{ t('supplier.accounts.form.notes') }}</label>
          <textarea v-model="form.notes" rows="2" class="input"></textarea>
        </div>
      </form>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="closeForm">{{ t('common.cancel') }}</button>
          <button type="submit" form="supplier-account-form" class="btn btn-primary" :disabled="saving">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Test -->
    <BaseDialog
      :show="testingAccount !== null"
      :title="t('supplier.accounts.testTitle', { name: testingAccount?.name || '' })"
      width="wide"
      @close="closeTest"
    >
      <div class="space-y-4">
        <div class="flex flex-wrap items-end gap-3">
          <div class="min-w-[240px] flex-1">
            <label class="input-label">{{ t('supplier.accounts.testModel') }}</label>
            <input v-model="testModel" type="text" class="input font-mono" :placeholder="t('supplier.accounts.testModelPlaceholder')" />
          </div>
          <button class="btn btn-primary" :disabled="testStatus === 'running'" @click="runTest">
            {{ testStatus === 'running' ? t('supplier.accounts.testing') : t('supplier.accounts.startTest') }}
          </button>
        </div>
        <div
          v-if="testStatus !== 'idle'"
          :class="['badge', testStatus === 'success' ? 'badge-success' : testStatus === 'error' ? 'badge-danger' : 'badge-gray']"
        >
          {{ testStatus === 'success' ? t('supplier.accounts.testSuccess') : testStatus === 'error' ? t('supplier.accounts.testFailed') : t('supplier.accounts.testing') }}
        </div>
        <pre
          v-if="testOutput"
          class="max-h-80 overflow-auto whitespace-pre-wrap rounded-lg bg-gray-900 p-4 font-mono text-xs text-gray-100"
        >{{ testOutput }}</pre>
      </div>
    </BaseDialog>

    <ConfirmDialog
      :show="pausingAccount !== null"
      :title="t('supplier.accounts.pause')"
      :message="t('supplier.accounts.pauseConfirm', { name: pausingAccount?.name || '' })"
      :confirm-text="t('supplier.accounts.pause')"
      :cancel-text="t('common.cancel')"
      @confirm="confirmPause"
      @cancel="pausingAccount = null"
    />
    <ConfirmDialog
      :show="deletingAccount !== null"
      :title="t('supplier.accounts.delete')"
      :message="t('supplier.accounts.deleteConfirm', { name: deletingAccount?.name || '' })"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="confirmDelete"
      @cancel="deletingAccount = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { supplierAPI, type SupplierAccount, type SupplierAccountPayload, type SupplierReviewState } from '@/api/supplier'
import { formatDateTime } from '@/utils/format'
import { platformLabel } from '@/utils/platformColors'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Column } from '@/components/common/types'

import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()

const accounts = ref<SupplierAccount[]>([])
const loading = ref(false)
const platforms = ref<string[]>([])
const filters = reactive({ search: '', platform: '' })
const pagination = reactive({ page: 1, page_size: getPersistedPageSize(), total: 0 })

const columns = computed<Column[]>(() => [
  { key: 'name', label: t('supplier.accounts.columns.name') },
  { key: 'platform', label: t('supplier.accounts.columns.platform') },
  { key: 'review_state', label: t('supplier.accounts.columns.state') },
  { key: 'concurrency', label: t('supplier.accounts.columns.concurrency') },
  { key: 'groups', label: t('supplier.accounts.columns.groups') },
  { key: 'rate_multiplier', label: t('supplier.accounts.columns.rateMultiplier') },
  { key: 'last_used_at', label: t('supplier.accounts.columns.lastUsed') },
  { key: 'created_at', label: t('supplier.accounts.columns.createdAt') },
  { key: 'actions', label: t('supplier.accounts.columns.actions') }
])

const platformOptions = computed(() => platforms.value.map((p) => ({ value: p, label: platformLabel(p) })))
const platformFilterOptions = computed(() => [
  { value: '', label: t('supplier.accounts.allPlatforms') },
  ...platformOptions.value
])

function stateBadgeClass(state: SupplierReviewState): string {
  switch (state) {
    case 'running':
      return 'badge-success'
    case 'paused':
    case 'pending':
      return 'badge-warning'
    case 'error':
      return 'badge-danger'
    default:
      return 'badge-gray'
  }
}

let loadSeq = 0
async function load() {
  const seq = ++loadSeq
  loading.value = true
  try {
    const res = await supplierAPI.listAccounts(pagination.page, pagination.page_size, {
      platform: filters.platform || undefined,
      search: filters.search.trim() || undefined
    })
    if (seq !== loadSeq) return
    accounts.value = res.items
    pagination.total = res.total
  } catch (error) {
    if (seq === loadSeq) appStore.showError(extractApiErrorMessage(error, t('supplier.accounts.failedToLoad')))
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

function reload() {
  pagination.page = 1
  void load()
}

let searchTimer: ReturnType<typeof setTimeout> | undefined
function debouncedReload() {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(reload, 300)
}

function handlePageChange(page: number) {
  pagination.page = page
  void load()
}

function handlePageSizeChange(size: number) {
  pagination.page_size = size
  reload()
}

// ---- create / edit ----
const showForm = ref(false)
const saving = ref(false)
const editingAccount = ref<SupplierAccount | null>(null)
const form = reactive({ platform: '', name: '', api_key: '', base_url: '', concurrency: 3, notes: '' })

function openCreate() {
  editingAccount.value = null
  Object.assign(form, { platform: platforms.value[0] || '', name: '', api_key: '', base_url: '', concurrency: 3, notes: '' })
  showForm.value = true
}

function openEdit(account: SupplierAccount) {
  editingAccount.value = account
  Object.assign(form, {
    platform: account.platform,
    name: account.name,
    api_key: '',
    base_url: account.base_url || '',
    concurrency: account.concurrency,
    notes: account.notes || ''
  })
  showForm.value = true
}

function closeForm() {
  showForm.value = false
  form.api_key = ''
}

async function save() {
  if (saving.value) return
  saving.value = true
  try {
    if (editingAccount.value) {
      const payload: SupplierAccountPayload = {
        name: form.name,
        notes: form.notes,
        concurrency: form.concurrency,
        base_url: form.base_url.trim()
      }
      if (form.api_key.trim()) payload.api_key = form.api_key.trim()
      await supplierAPI.updateAccount(editingAccount.value.id, payload)
      appStore.showSuccess(t('supplier.accounts.updated'))
    } else {
      await supplierAPI.createAccount({
        platform: form.platform,
        name: form.name,
        notes: form.notes || null,
        api_key: form.api_key.trim(),
        base_url: form.base_url.trim(),
        concurrency: form.concurrency
      })
      appStore.showSuccess(t('supplier.accounts.created'))
    }
    closeForm()
    void load()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('supplier.accounts.failedToSave')))
  } finally {
    saving.value = false
  }
}

// ---- pause / delete ----
const pausingAccount = ref<SupplierAccount | null>(null)
const deletingAccount = ref<SupplierAccount | null>(null)

async function confirmPause() {
  const account = pausingAccount.value
  pausingAccount.value = null
  if (!account) return
  try {
    await supplierAPI.pauseAccount(account.id)
    appStore.showSuccess(t('supplier.accounts.pausedDone'))
    void load()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('supplier.accounts.failedToSave')))
  }
}

async function confirmDelete() {
  const account = deletingAccount.value
  deletingAccount.value = null
  if (!account) return
  try {
    await supplierAPI.deleteAccount(account.id)
    appStore.showSuccess(t('supplier.accounts.deleted'))
    void load()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('supplier.accounts.failedToSave')))
  }
}

// ---- test (same SSE events as the admin test) ----
const testingAccount = ref<SupplierAccount | null>(null)
const testModel = ref('')
const testOutput = ref('')
const testStatus = ref<'idle' | 'running' | 'success' | 'error'>('idle')
let testAbort: AbortController | null = null

function openTest(account: SupplierAccount) {
  testingAccount.value = account
  testModel.value = ''
  testOutput.value = ''
  testStatus.value = 'idle'
}

function closeTest() {
  testAbort?.abort()
  testAbort = null
  testingAccount.value = null
}

function handleTestEvent(event: { type: string; text?: string; model?: string; success?: boolean; error?: string }) {
  switch (event.type) {
    case 'test_start':
      if (event.model) testOutput.value += `model: ${event.model}\n\n`
      break
    case 'content':
    case 'status':
      if (event.text) testOutput.value += event.text
      break
    case 'test_complete':
      testStatus.value = event.success ? 'success' : 'error'
      if (!event.success && event.error) testOutput.value += `\n${event.error}`
      break
    case 'error':
      testStatus.value = 'error'
      if (event.error) testOutput.value += `\n${event.error}`
      break
  }
}

async function runTest() {
  const account = testingAccount.value
  if (!account) return
  testAbort?.abort()
  testAbort = new AbortController()
  testOutput.value = ''
  testStatus.value = 'running'
  try {
    const response = await fetch(supplierAPI.testAccountUrl(account.id), {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${localStorage.getItem('auth_token')}`,
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ model_id: testModel.value.trim() }),
      signal: testAbort.signal
    })
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    const reader = response.body?.getReader()
    if (!reader) throw new Error('No response body')
    const decoder = new TextDecoder()
    let buffer = ''
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      for (const line of lines) {
        if (!line.startsWith('data: ')) continue
        const raw = line.slice(6).trim()
        if (!raw) continue
        try {
          handleTestEvent(JSON.parse(raw))
        } catch {
          // ignore malformed SSE frames
        }
      }
    }
    if (testStatus.value === 'running') testStatus.value = 'error'
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') return
    testStatus.value = 'error'
    testOutput.value += `\n${error instanceof Error ? error.message : String(error)}`
  }
}

onMounted(async () => {
  try {
    platforms.value = (await supplierAPI.getMeta()).platforms
  } catch {
    platforms.value = []
  }
  void load()
})

onUnmounted(() => {
  if (searchTimer) clearTimeout(searchTimer)
  testAbort?.abort()
})
</script>

<style scoped>
.action-btn {
  @apply flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors;
}
</style>
