<template>
  <AppLayout>
    <div class="space-y-4">
      <!-- Filters -->
      <div class="card p-4">
        <div class="flex flex-wrap items-center gap-3">
          <Select v-model="currentFilter" :options="statusFilters" class="w-36" @change="fetchOrders" />
          <div class="flex flex-1 items-center justify-end gap-2">
            <button @click="fetchOrders" :disabled="loading" class="btn btn-secondary" :title="t('common.refresh')">
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button class="btn btn-primary" @click="router.push('/purchase')">{{ t('payment.result.backToRecharge') }}</button>
          </div>
        </div>
      </div>

      <!-- Table -->
      <OrderTable :orders="orders" :loading="loading">
        <template #actions="{ row }">
          <div class="flex items-center gap-2">
            <button v-if="row.status === 'PENDING'" @click="handleCancel(row.id)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-yellow-600 hover:bg-yellow-50 dark:text-yellow-400 dark:hover:bg-yellow-900/20">
              <Icon name="x" size="sm" />
              <span>{{ t('payment.orders.cancel') }}</span>
            </button>
            <button v-if="canViewReceipt(row)" @click="openReceipt(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-teal-700 hover:bg-teal-50 dark:text-teal-300 dark:hover:bg-teal-900/20">
              <Icon name="document" size="sm" />
              <span>{{ t('payment.orders.viewReceipt') }}</span>
            </button>
            <button v-if="canRequestRefund(row)" @click="openRefundDialog(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-purple-600 hover:bg-purple-50 dark:text-purple-400 dark:hover:bg-purple-900/20">
              <Icon name="dollar" size="sm" />
              <span>{{ t('payment.orders.requestRefund') }}</span>
            </button>
          </div>
        </template>
      </OrderTable>

      <!-- Pagination -->
      <Pagination
        v-if="pagination.total > 0"
        :page="pagination.page"
        :total="pagination.total"
        :page-size="pagination.page_size"
        @update:page="handlePageChange"
        @update:pageSize="handlePageSizeChange"
      />
    </div>

    <!-- Cancel Confirm Dialog -->
    <BaseDialog :show="!!cancelTargetId" :title="t('payment.orders.cancel')" width="narrow" @close="cancelTargetId = null">
      <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('payment.confirmCancel') }}</p>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button class="btn btn-secondary" @click="cancelTargetId = null">{{ t('common.cancel') }}</button>
          <button class="btn btn-danger" :disabled="actionLoading" @click="confirmCancel">{{ actionLoading ? t('common.processing') : t('payment.orders.cancel') }}</button>
        </div>
      </template>
    </BaseDialog>

    <!-- Refund Dialog -->
    <BaseDialog :show="!!refundTarget" :title="t('payment.orders.requestRefund')" @close="refundTarget = null">
      <div v-if="refundTarget" class="space-y-4">
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
          <div class="flex justify-between text-sm">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
            <span class="font-mono text-gray-900 dark:text-white">#{{ refundTarget.id }}</span>
          </div>
          <div class="mt-2 flex justify-between text-sm">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.amount') }}</span>
            <span class="text-gray-900 dark:text-white">${{ refundTarget.amount.toFixed(2) }}</span>
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('payment.refundReason') }}</label>
          <textarea v-model="refundReason" rows="3" class="input mt-1 w-full" :placeholder="t('payment.refundReasonPlaceholder')" />
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button class="btn btn-secondary" @click="refundTarget = null">{{ t('common.cancel') }}</button>
          <button class="btn btn-primary" :disabled="actionLoading || !refundReason.trim()" @click="confirmRefund">{{ actionLoading ? t('common.processing') : t('payment.orders.requestRefund') }}</button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog :show="!!receiptOrder" :title="t('payment.receipt.title')" width="wide" @close="receiptOrder = null">
      <PaymentReceipt v-if="receiptModel" :model="receiptModel" :copy="receiptCopy" />
      <template #footer>
        <div class="flex justify-end gap-3">
          <button class="btn btn-secondary" @click="receiptOrder = null">{{ t('common.close') }}</button>
          <button class="btn btn-primary inline-flex items-center gap-2" :disabled="receiptDownloading" @click="downloadCurrentReceipt">
            <Icon name="download" size="sm" />
            <span>{{ receiptDownloading ? t('common.processing') : t('payment.orders.downloadReceipt') }}</span>
          </button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAuthStore, useAppStore } from '@/stores'
import { paymentAPI } from '@/api/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'
import type { PaymentOrder } from '@/types/payment'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import OrderTable from '@/components/payment/OrderTable.vue'
import PaymentReceipt from '@/components/payment/PaymentReceipt.vue'
import { buildReceiptModel, canViewReceipt, type ReceiptCopy } from '@/components/payment/receipt'
import { downloadReceiptPdf } from '@/components/payment/receiptPdf'

const { t, locale } = useI18n()
const router = useRouter()
const appStore = useAppStore()
const authStore = useAuthStore()

const loading = ref(false)
const actionLoading = ref(false)
const orders = ref<PaymentOrder[]>([])
const refundEligibleProviders = ref<Set<string>>(new Set())
const currentFilter = ref('')
const cancelTargetId = ref<number | null>(null)
const refundTarget = ref<PaymentOrder | null>(null)
const refundReason = ref('')
const receiptOrder = ref<PaymentOrder | null>(null)
const receiptDownloading = ref(false)
const pagination = reactive({ page: 1, page_size: 20, total: 0 })

const receiptCopy = computed((): ReceiptCopy => ({
  title: t('payment.receipt.title'),
  paid: t('payment.receipt.paid'),
  refunded: t('payment.receipt.refunded'),
  receiptNo: t('payment.receipt.receiptNo'),
  issuedAt: t('payment.receipt.issuedAt'),
  received: t('payment.receipt.received'),
  amountWords: t('payment.receipt.amountWords'),
  payer: t('payment.receipt.payer'),
  payerName: t('payment.receipt.payerName'),
  sectionTrade: t('payment.receipt.sectionTrade'),
  paidAt: t('payment.receipt.paidAt'),
  method: t('payment.receipt.method'),
  tradeNo: t('payment.receipt.tradeNo'),
  settled: t('payment.receipt.settled'),
  settledYes: t('payment.receipt.settledYes'),
  settledRefunded: t('payment.receipt.settledRefunded'),
  merchant: t('payment.receipt.merchant'),
  currency: t('payment.receipt.currency'),
  sectionItems: t('payment.receipt.sectionItems'),
  itemName: t('payment.receipt.itemName'),
  itemDesc: t('payment.receipt.itemDesc'),
  qty: t('payment.receipt.qty'),
  unitPrice: t('payment.receipt.unitPrice'),
  lineAmount: t('payment.receipt.lineAmount'),
  total: t('payment.receipt.total'),
  notesTitle: t('payment.receipt.notesTitle'),
  noteProof: t('payment.receipt.noteProof'),
  noteNotInvoice: t('payment.receipt.noteNotInvoice'),
  noteRefund: t('payment.receipt.noteRefund'),
  issuerTitle: t('payment.receipt.issuerTitle'),
  itemBalance: t('payment.receipt.itemBalance'),
  itemBalanceDesc: t('payment.receipt.itemBalanceDesc'),
  itemSubscription: t('payment.receipt.itemSubscription'),
  itemSubscriptionDesc: t('payment.receipt.itemSubscriptionDesc'),
}))

const receiptModel = computed(() => {
  if (!receiptOrder.value) return null
  return buildReceiptModel({
    order: receiptOrder.value,
    payer: authStore.user,
    siteName: appStore.siteName,
    siteUrl: window.location.origin,
    contactInfo: appStore.contactInfo,
    locale: locale.value,
    copy: receiptCopy.value,
    paymentMethodLabel: t('payment.methods.' + receiptOrder.value.payment_type, receiptOrder.value.payment_type),
  })
})

const statusFilters = computed(() => [
  { value: '', label: t('common.all') },
  { value: 'PENDING', label: t('payment.status.pending') },
  { value: 'COMPLETED', label: t('payment.status.completed') },
  { value: 'FAILED', label: t('payment.status.failed') },
  { value: 'REFUNDED', label: t('payment.status.refunded') },
])

async function fetchOrders() {
  loading.value = true
  try {
    const res = await paymentAPI.getMyOrders({
      page: pagination.page,
      page_size: pagination.page_size,
      status: currentFilter.value || undefined,
    })
    orders.value = res.data.items || []
    pagination.total = res.data.total || 0
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    loading.value = false
  }
}

function handlePageChange(page: number) { pagination.page = page; fetchOrders() }
function handlePageSizeChange(size: number) { pagination.page_size = size; pagination.page = 1; fetchOrders() }

function handleCancel(orderId: number) { cancelTargetId.value = orderId }

async function confirmCancel() {
  if (!cancelTargetId.value) return
  actionLoading.value = true
  try {
    await paymentAPI.cancelOrder(cancelTargetId.value)
    appStore.showSuccess(t('common.success'))
    cancelTargetId.value = null
    await fetchOrders()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    actionLoading.value = false
  }
}

function openRefundDialog(order: PaymentOrder) { refundTarget.value = order; refundReason.value = '' }

async function confirmRefund() {
  if (!refundTarget.value || !refundReason.value.trim()) return
  actionLoading.value = true
  try {
    await paymentAPI.requestRefund(refundTarget.value.id, { reason: refundReason.value.trim() })
    appStore.showSuccess(t('common.success'))
    refundTarget.value = null
    refundReason.value = ''
    await fetchOrders()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    actionLoading.value = false
  }
}

function canRequestRefund(order: PaymentOrder): boolean {
  if (order.status !== 'COMPLETED') return false
  if (!order.provider_instance_id) return false
  return refundEligibleProviders.value.has(order.provider_instance_id)
}

async function loadRefundEligibility() {
  try {
    const res = await paymentAPI.getRefundEligibleProviders()
    refundEligibleProviders.value = new Set(res.data.provider_instance_ids || [])
  } catch { /* ignore — default to hiding refund button */ }
}

function openReceipt(order: PaymentOrder) {
  receiptOrder.value = order
}

function downloadCurrentReceipt() {
  if (!receiptModel.value) return
  receiptDownloading.value = true
  try {
    downloadReceiptPdf(receiptModel.value, receiptCopy.value)
  } catch {
    appStore.showError(t('payment.receipt.downloadFailed'))
  } finally {
    receiptDownloading.value = false
  }
}

onMounted(() => { fetchOrders(); loadRefundEligibility() })
</script>
