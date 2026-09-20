<template>
  <article class="receipt-sheet overflow-hidden rounded-xl border border-slate-200 bg-white text-slate-900 shadow-sm">
    <header class="px-6 pt-6">
      <div class="flex items-start justify-between gap-4">
        <p class="text-sm font-semibold text-teal-700">{{ model.merchant }}</p>
        <p class="text-sm font-medium" :class="model.refunded ? 'text-amber-700' : 'text-teal-700'">{{ model.statusLabel }}</p>
      </div>
      <div class="mt-3 flex flex-wrap items-end justify-between gap-3">
        <h3 class="text-3xl font-bold tracking-tight">{{ copy.title }}</h3>
        <div class="text-right text-xs text-slate-500">
          <p>{{ copy.receiptNo }} {{ model.receiptNo }}</p>
          <p>{{ copy.issuedAt }} {{ model.issuedAt }}</p>
        </div>
      </div>
    </header>
    <div class="mt-5 h-1.5 bg-teal-700" />
    <section class="grid gap-6 px-6 py-6 sm:grid-cols-2">
      <div>
        <p class="text-xs text-slate-500">{{ copy.received }}</p>
        <p class="mt-1 text-3xl font-bold tracking-tight">{{ model.amountText }}</p>
        <p class="mt-2 text-xs text-slate-500">{{ copy.amountWords }}：{{ model.amountWords }}</p>
      </div>
      <div class="sm:text-right">
        <p class="text-xs text-slate-500">{{ copy.payer }}</p>
        <p class="mt-1 break-all text-sm font-medium">{{ model.payerEmail }}</p>
        <p class="mt-1 text-xs text-slate-500">{{ copy.payerName }}：{{ model.payerName }}</p>
      </div>
    </section>
    <section class="px-6 pb-2">
      <h4 class="mb-2 text-sm font-semibold">{{ copy.sectionTrade }}</h4>
      <div class="overflow-hidden rounded-lg border border-slate-200">
        <dl class="grid grid-cols-1 text-sm sm:grid-cols-2">
          <div class="flex justify-between gap-3 border-b border-slate-200 bg-slate-50 px-3 py-2.5 sm:border-r">
            <dt class="shrink-0 text-slate-500">{{ copy.paidAt }}</dt>
            <dd class="text-right">{{ model.paidAt }}</dd>
          </div>
          <div class="flex justify-between gap-3 border-b border-slate-200 bg-slate-50 px-3 py-2.5">
            <dt class="shrink-0 text-slate-500">{{ copy.method }}</dt>
            <dd class="text-right">{{ model.paymentMethod }}</dd>
          </div>
          <div class="flex justify-between gap-3 border-b border-slate-200 px-3 py-2.5 sm:border-r">
            <dt class="shrink-0 text-slate-500">{{ copy.tradeNo }}</dt>
            <dd class="break-all text-right font-mono text-xs">{{ model.tradeNo }}</dd>
          </div>
          <div class="flex justify-between gap-3 border-b border-slate-200 px-3 py-2.5">
            <dt class="shrink-0 text-slate-500">{{ copy.settled }}</dt>
            <dd class="text-right">{{ model.settledLabel }}</dd>
          </div>
          <div class="flex justify-between gap-3 px-3 py-2.5 sm:border-r sm:border-slate-200">
            <dt class="shrink-0 text-slate-500">{{ copy.merchant }}</dt>
            <dd class="text-right">{{ model.merchant }}</dd>
          </div>
          <div class="flex justify-between gap-3 px-3 py-2.5">
            <dt class="shrink-0 text-slate-500">{{ copy.currency }}</dt>
            <dd class="text-right">{{ model.currency }}</dd>
          </div>
        </dl>
      </div>
    </section>
    <section class="px-6 py-4">
      <h4 class="mb-2 text-sm font-semibold">{{ copy.sectionItems }}</h4>
      <div class="overflow-x-auto rounded-lg border border-slate-200">
        <table class="min-w-full text-left text-sm">
          <thead class="bg-slate-50 text-xs text-slate-500">
            <tr>
              <th class="px-3 py-2 font-medium">{{ copy.itemName }}</th>
              <th class="px-3 py-2 font-medium">{{ copy.itemDesc }}</th>
              <th class="px-3 py-2 font-medium">{{ copy.qty }}</th>
              <th class="px-3 py-2 font-medium">{{ copy.unitPrice }}</th>
              <th class="px-3 py-2 text-right font-medium">{{ copy.lineAmount }}</th>
            </tr>
          </thead>
          <tbody>
            <tr class="border-t border-slate-200">
              <td class="px-3 py-2.5">{{ model.itemName }}</td>
              <td class="px-3 py-2.5 text-slate-500">{{ model.itemDesc }}</td>
              <td class="px-3 py-2.5">{{ model.quantity }}</td>
              <td class="px-3 py-2.5">{{ model.unitPrice }}</td>
              <td class="px-3 py-2.5 text-right">{{ model.lineAmount }}</td>
            </tr>
          </tbody>
          <tfoot>
            <tr class="border-t border-slate-200 bg-slate-50 font-semibold">
              <td class="px-3 py-2.5" colspan="4">{{ copy.total }}</td>
              <td class="px-3 py-2.5 text-right">{{ model.lineAmount }}</td>
            </tr>
          </tfoot>
        </table>
      </div>
    </section>
    <footer class="grid gap-6 px-6 pb-6 sm:grid-cols-2">
      <div>
        <h4 class="text-sm font-semibold">{{ copy.notesTitle }}</h4>
        <ol class="mt-2 list-decimal space-y-1 pl-4 text-xs leading-5 text-slate-500">
          <li>{{ copy.noteProof }}</li>
          <li>{{ copy.noteRefund }}</li>
        </ol>
      </div>
      <div>
        <h4 class="text-sm font-semibold">{{ copy.issuerTitle }}</h4>
        <p class="mt-2 text-sm">{{ model.merchant }}</p>
        <p class="text-xs text-slate-500">{{ model.siteUrl }}</p>
        <p v-if="model.contactInfo" class="text-xs text-slate-500">{{ model.contactInfo }}</p>
      </div>
    </footer>
  </article>
</template>

<script setup lang="ts">
import type { ReceiptCopy, ReceiptModel } from './receipt'

defineProps<{
  model: ReceiptModel
  copy: ReceiptCopy
}>()
</script>
