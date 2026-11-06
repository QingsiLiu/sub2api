<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="space-y-3">
          <p
            v-if="issuerLoaded && !issuerReady"
            class="rounded-lg bg-amber-50 px-4 py-2 text-sm text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
          >
            {{ t('invoice.issuerNotReady') }}
          </p>
          <div class="flex flex-wrap items-center justify-end gap-2">
            <button class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="load">
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button class="btn btn-secondary" @click="showIssuer = true">
              <Icon name="cog" size="md" class="mr-1" />
              {{ t('invoice.issuerSettings') }}
            </button>
            <button class="btn btn-primary" @click="openCreate">
              <Icon name="plus" size="md" class="mr-1" />
              {{ t('invoice.issue') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="invoices" :loading="loading">
          <template #cell-invoice_no="{ row }">
            <span class="font-mono text-sm text-gray-900 dark:text-white">{{ row.invoice_no }}</span>
          </template>
          <template #cell-bill_to_name="{ row }">
            <div class="min-w-0">
              <div class="max-w-xs truncate text-sm text-gray-700 dark:text-gray-300">{{ row.bill_to_name || '-' }}</div>
              <div class="max-w-xs truncate text-xs text-gray-500 dark:text-dark-400" :title="row.description">{{ row.description }}</div>
            </div>
          </template>
          <template #cell-amount="{ row }">
            <span class="font-mono text-sm text-gray-900 dark:text-white">{{ row.currency }} {{ formatInvoiceAmount(row.amount) }}</span>
          </template>
          <template #cell-status="{ row }">
            <span :class="['badge', row.voided_at ? 'badge-gray' : 'badge-success']">
              {{ row.voided_at ? t('invoice.status.voided') : t('invoice.status.issued') }}
            </span>
          </template>
          <template #cell-created_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">{{ formatDateTime(value) }}</span>
          </template>
          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <button class="action-btn hover:bg-blue-50 hover:text-blue-600 dark:hover:bg-blue-900/20" @click="openPrint(row)">
                <Icon name="document" size="sm" />
                <span class="text-xs">{{ t('invoice.print') }}</span>
              </button>
              <button
                v-if="!row.voided_at"
                class="action-btn hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
                @click="voidingInvoice = row"
              >
                <Icon name="ban" size="sm" />
                <span class="text-xs">{{ t('invoice.void') }}</span>
              </button>
            </div>
          </template>
          <template #empty>
            <EmptyState
              :title="t('invoice.emptyTitle')"
              :description="t('invoice.emptyDescription')"
              :action-text="t('invoice.issue')"
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

    <BaseDialog :show="showCreate" :title="t('invoice.issue')" width="normal" @close="showCreate = false">
      <form id="invoice-create-form" class="space-y-4" @submit.prevent="create">
        <div class="grid grid-cols-3 gap-3">
          <div>
            <label class="input-label">{{ t('invoice.form.currency') }}</label>
            <Select v-model="form.currency" :options="currencyOptions" />
          </div>
          <div class="col-span-2">
            <label class="input-label">{{ t('invoice.form.amount') }}</label>
            <input
              v-model="form.amount"
              type="text"
              inputmode="decimal"
              pattern="(0|[1-9][0-9]{0,11})(\.[0-9]{1,2})?"
              class="input font-mono"
              placeholder="0.00"
              required
            />
            <p class="input-hint">{{ t('invoice.form.amountHint') }}</p>
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('invoice.form.issueDate') }}</label>
          <input v-model="form.issue_date" type="date" class="input" required />
        </div>
        <div>
          <label class="input-label">{{ t('invoice.form.description') }}</label>
          <input v-model="form.description" type="text" maxlength="2000" class="input" required />
        </div>
        <div>
          <label class="input-label">{{ t('invoice.form.billToName') }}</label>
          <input v-model="form.bill_to_name" type="text" maxlength="200" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('invoice.form.billToAddress') }}</label>
          <textarea v-model="form.bill_to_address" rows="2" maxlength="2000" class="input"></textarea>
        </div>
        <div>
          <label class="input-label">{{ t('invoice.form.notes') }}</label>
          <textarea v-model="form.notes" rows="2" maxlength="2000" class="input"></textarea>
        </div>
        <div>
          <label class="input-label">{{ t('invoice.form.paymentPreview') }}</label>
          <pre
            v-if="selectedPayment"
            class="whitespace-pre-wrap rounded-lg bg-gray-50 p-3 font-mono text-xs text-gray-700 dark:bg-dark-700 dark:text-gray-300"
          >{{ selectedPayment }}</pre>
          <p v-else class="rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-700 dark:bg-amber-900/20 dark:text-amber-300">
            {{ t('invoice.form.paymentMissing') }}
          </p>
        </div>
      </form>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="showCreate = false">{{ t('common.cancel') }}</button>
          <button type="submit" form="invoice-create-form" class="btn btn-primary" :disabled="creating || !selectedPayment">
            {{ creating ? t('common.saving') : t('invoice.form.submit') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <InvoiceIssuerDialog :show="showIssuer" @close="showIssuer = false" @saved="onIssuerSaved" />

    <ConfirmDialog
      :show="voidingInvoice !== null"
      :title="t('invoice.void')"
      :message="t('invoice.voidConfirm', { no: voidingInvoice?.invoice_no || '' })"
      :confirm-text="t('invoice.void')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="confirmVoid"
      @cancel="voidingInvoice = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import {
  INVOICE_CURRENCIES,
  formatInvoiceAmount,
  invoicesAPI,
  type Invoice,
  type InvoiceCurrency,
  type InvoiceIssuer
} from '@/api/admin/invoices'
import { formatDateTime } from '@/utils/format'
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
import InvoiceIssuerDialog from './InvoiceIssuerDialog.vue'

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()

const invoices = ref<Invoice[]>([])
const loading = ref(false)
const pagination = reactive({ page: 1, page_size: getPersistedPageSize(), total: 0 })

const columns = computed<Column[]>(() => [
  { key: 'invoice_no', label: t('invoice.columns.invoiceNo') },
  { key: 'issue_date', label: t('invoice.columns.issueDate') },
  { key: 'bill_to_name', label: t('invoice.columns.billTo') },
  { key: 'amount', label: t('invoice.columns.amount') },
  { key: 'status', label: t('invoice.columns.status') },
  { key: 'created_at', label: t('invoice.columns.createdAt') },
  { key: 'actions', label: t('invoice.columns.actions') }
])

const errorMap = computed(() => ({
  INVOICE_ISSUER_NOT_READY: t('invoice.errors.notReady'),
  INVOICE_INVALID: t('invoice.errors.invalid')
}))

let loadSeq = 0
async function load() {
  const seq = ++loadSeq
  loading.value = true
  try {
    const res = await invoicesAPI.list(pagination.page, pagination.page_size)
    if (seq !== loadSeq) return
    invoices.value = res.items
    pagination.total = res.total
  } catch (error) {
    if (seq === loadSeq) appStore.showError(extractApiErrorMessage(error, t('invoice.failedToLoad')))
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

function handlePageChange(page: number) {
  pagination.page = page
  void load()
}

function handlePageSizeChange(size: number) {
  pagination.page_size = size
  pagination.page = 1
  void load()
}

// ---- issuer ----
const issuer = ref<InvoiceIssuer | null>(null)
const issuerLoaded = ref(false)
const showIssuer = ref(false)
const issuerReady = computed(
  () => !!issuer.value?.company_name && INVOICE_CURRENCIES.some((c) => !!issuer.value?.payment[c])
)

async function loadIssuer() {
  try {
    issuer.value = await invoicesAPI.getIssuer()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('invoice.failedToLoad')))
  } finally {
    issuerLoaded.value = true
  }
}

function onIssuerSaved(saved: InvoiceIssuer) {
  issuer.value = saved
}

// ---- create ----
const showCreate = ref(false)
const creating = ref(false)
const currencyOptions = INVOICE_CURRENCIES.map((c) => ({ value: c, label: c }))
const form = reactive({
  currency: 'USD' as InvoiceCurrency,
  amount: '',
  issue_date: '',
  description: '',
  bill_to_name: '',
  bill_to_address: '',
  notes: ''
})
const selectedPayment = computed(() => issuer.value?.payment[form.currency] || '')

function localToday(): string {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

function openCreate() {
  Object.assign(form, {
    amount: '',
    issue_date: localToday(),
    description: t('invoice.form.defaultDescription'),
    bill_to_name: '',
    bill_to_address: '',
    notes: ''
  })
  // Keep the last chosen currency, but move off one that has no payment details.
  if (issuer.value && !issuer.value.payment[form.currency]) {
    form.currency = INVOICE_CURRENCIES.find((c) => !!issuer.value?.payment[c]) || form.currency
  }
  showCreate.value = true
}

async function create() {
  if (creating.value) return
  creating.value = true
  try {
    const invoice = await invoicesAPI.create({
      currency: form.currency,
      amount: form.amount.trim(),
      issue_date: form.issue_date,
      description: form.description.trim(),
      bill_to_name: form.bill_to_name.trim(),
      bill_to_address: form.bill_to_address.trim(),
      notes: form.notes.trim()
    })
    appStore.showSuccess(t('invoice.form.created', { no: invoice.invoice_no }))
    showCreate.value = false
    void router.push({ name: 'AdminInvoicePrint', params: { id: invoice.id } })
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error'), errorMap.value))
  } finally {
    creating.value = false
  }
}

// ---- print / void ----
function openPrint(invoice: Invoice) {
  void router.push({ name: 'AdminInvoicePrint', params: { id: invoice.id } })
}

const voidingInvoice = ref<Invoice | null>(null)
async function confirmVoid() {
  const target = voidingInvoice.value
  voidingInvoice.value = null
  if (!target) return
  try {
    await invoicesAPI.void(target.id)
    appStore.showSuccess(t('invoice.voided'))
    void load()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  }
}

onMounted(() => {
  void load()
  void loadIssuer()
})
</script>
