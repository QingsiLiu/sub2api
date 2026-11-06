<template>
  <div class="min-h-screen bg-gray-100 py-6 print:min-h-0 print:bg-white print:py-0">
    <div class="mx-auto mb-4 flex max-w-[210mm] print:hidden flex-wrap items-center justify-between gap-3 px-2">
      <button class="btn btn-secondary" @click="router.push({ name: 'AdminInvoices' })">
        <Icon name="arrowLeft" size="md" class="mr-1" />
        {{ t('invoice.sheet.back') }}
      </button>
      <div class="flex items-center gap-3">
        <span class="hidden text-xs text-gray-500 md:inline">{{ t('invoice.sheet.printHint') }}</span>
        <button class="btn btn-primary" :disabled="!doc" @click="printNow">
          <Icon name="document" size="md" class="mr-1" />
          {{ t('invoice.sheet.print') }}
        </button>
      </div>
    </div>

    <div v-if="loading" class="flex justify-center py-20">
      <Icon name="refresh" size="lg" class="animate-spin text-gray-400" />
    </div>

    <article v-else-if="doc" class="invoice-sheet relative mx-auto bg-white text-[13px] leading-relaxed text-gray-900 shadow-lg print:shadow-none">
      <div v-if="inv.voided_at" class="pointer-events-none absolute inset-0 flex items-center justify-center overflow-hidden">
        <span class="-rotate-[30deg] rounded-xl border-[6px] border-red-500/60 px-10 py-2 text-[96px] font-black tracking-widest text-red-500/60">VOID</span>
      </div>

      <header class="flex items-start justify-between gap-8 border-b-2 border-gray-900 pb-6">
        <div class="min-w-0">
          <h1 class="text-2xl font-bold tracking-wide">{{ inv.issuer_name }}</h1>
          <p v-if="inv.issuer_address" class="mt-1 whitespace-pre-line text-gray-600">{{ inv.issuer_address }}</p>
          <p v-if="inv.issuer_contact" class="mt-1 whitespace-pre-line text-gray-600">{{ inv.issuer_contact }}</p>
        </div>
        <div class="shrink-0 text-right">
          <h2 class="text-3xl font-light tracking-[0.25em] text-gray-700">INVOICE</h2>
          <table class="ml-auto mt-3 text-left">
            <tbody>
              <tr>
                <td class="pr-4 text-gray-500">Invoice No.</td>
                <td class="font-mono font-semibold">{{ inv.invoice_no }}</td>
              </tr>
              <tr>
                <td class="pr-4 text-gray-500">Date</td>
                <td>{{ formatSheetDate(inv.issue_date) }}</td>
              </tr>
              <tr>
                <td class="pr-4 text-gray-500">Currency</td>
                <td>{{ inv.currency }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </header>

      <section v-if="inv.bill_to_name || inv.bill_to_address" class="mt-6">
        <h3 class="text-xs font-semibold uppercase tracking-wider text-gray-500">Bill To</h3>
        <p v-if="inv.bill_to_name" class="mt-1 font-semibold">{{ inv.bill_to_name }}</p>
        <p v-if="inv.bill_to_address" class="whitespace-pre-line text-gray-600">{{ inv.bill_to_address }}</p>
      </section>

      <table class="mt-8 w-full border-collapse">
        <thead>
          <tr class="border-b border-gray-300 text-left text-xs uppercase tracking-wider text-gray-500">
            <th class="w-12 py-2 font-semibold">#</th>
            <th class="py-2 font-semibold">Description</th>
            <th class="py-2 text-right font-semibold">Amount ({{ inv.currency }})</th>
          </tr>
        </thead>
        <tbody>
          <tr class="border-b border-gray-200 align-top">
            <td class="py-3 text-gray-500">1</td>
            <td class="whitespace-pre-line py-3">{{ inv.description }}</td>
            <td class="py-3 text-right font-mono">{{ formatInvoiceAmount(inv.amount) }}</td>
          </tr>
        </tbody>
        <tfoot>
          <tr>
            <td></td>
            <td class="pt-4 text-right font-semibold uppercase tracking-wider">Total Due</td>
            <td class="pt-4 text-right font-mono text-lg font-bold">{{ inv.currency }} {{ formatInvoiceAmount(inv.amount) }}</td>
          </tr>
        </tfoot>
      </table>

      <section v-if="inv.notes" class="mt-8">
        <h3 class="text-xs font-semibold uppercase tracking-wider text-gray-500">Notes</h3>
        <p class="mt-1 whitespace-pre-line">{{ inv.notes }}</p>
      </section>

      <section class="mt-8 rounded border border-gray-300 p-4">
        <h3 class="text-xs font-semibold uppercase tracking-wider text-gray-500">Payment Details ({{ inv.currency }})</h3>
        <p class="mt-2 whitespace-pre-line font-mono text-[12px]">{{ inv.payment_details }}</p>
      </section>

      <section class="mt-8 flex justify-end">
        <div class="relative w-64 text-center">
          <p class="text-left text-gray-600">For and on behalf of</p>
          <p class="text-left font-semibold">{{ inv.issuer_name }}</p>
          <div class="relative h-32">
            <img v-if="sealSrc" :src="sealSrc" alt="" class="absolute left-1/2 top-1 h-32 w-32 -translate-x-1/2 object-contain" />
          </div>
          <div class="border-t border-gray-700 pt-1 text-xs text-gray-600">Authorized Signature</div>
        </div>
      </section>

      <footer v-if="inv.footer_note" class="mt-8 border-t border-gray-200 pt-3 text-center text-xs text-gray-500">
        <p class="whitespace-pre-line">{{ inv.footer_note }}</p>
      </footer>
    </article>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { formatInvoiceAmount, invoicesAPI, type Invoice, type InvoiceDocument } from '@/api/admin/invoices'
import { extractApiErrorMessage } from '@/utils/apiError'
import { sanitizeUrl } from '@/utils/url'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const appStore = useAppStore()

const loading = ref(true)
const doc = ref<InvoiceDocument | null>(null)
const inv = computed(() => doc.value?.invoice as Invoice)
const sealSrc = computed(() => sanitizeUrl(doc.value?.seal_image || '', { allowDataUrl: true }))

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
function formatSheetDate(date: string): string {
  const [y, m, d] = date.split('-')
  return `${d} ${MONTHS[Number(m) - 1] ?? m} ${y}`
}

// The browser uses the document title as the default PDF file name.
function printNow() {
  if (!doc.value) return
  const previous = document.title
  document.title = `${inv.value.invoice_no} ${inv.value.issuer_name}`
  window.print()
  document.title = previous
}

onMounted(async () => {
  const id = Number(route.params.id)
  try {
    doc.value = await invoicesAPI.get(id)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('invoice.failedToLoad')))
  } finally {
    loading.value = false
  }
})
</script>

<style>
.invoice-sheet {
  width: 210mm;
  min-height: 297mm;
  padding: 14mm 16mm;
  box-sizing: border-box;
  print-color-adjust: exact;
  -webkit-print-color-adjust: exact;
}

@media print {
  @page {
    size: A4;
    margin: 0;
  }
  html,
  body {
    background: #fff !important;
  }
  /* Global overlays (toasts, announcements) also render here; print only the sheet. */
  body * {
    visibility: hidden;
  }
  .invoice-sheet,
  .invoice-sheet * {
    visibility: visible;
  }
  .invoice-sheet {
    position: absolute;
    left: 0;
    top: 0;
    min-height: auto;
  }
}
</style>
