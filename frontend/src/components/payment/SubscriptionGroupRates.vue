<template>
  <div v-if="rates.length" class="card p-4">
    <div class="mb-3">
      <p class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('payment.groupRates.title') }}</p>
      <p class="mt-0.5 text-xs text-gray-400 dark:text-gray-500">{{ t('payment.groupRates.hint') }}</p>
    </div>
    <div class="space-y-3">
      <section v-for="section in sections" :key="section.panel || 'other'">
        <p class="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">
          {{ section.label }}
        </p>
        <div class="divide-y divide-gray-100 overflow-hidden rounded-xl border border-gray-100 dark:divide-dark-700 dark:border-dark-700">
          <div
            v-for="row in section.rows"
            :key="row.id"
            class="flex items-center justify-between gap-3 bg-white px-3 py-2 dark:bg-dark-800"
          >
            <span class="min-w-0 truncate text-sm text-gray-700 dark:text-gray-200">{{ row.name }}</span>
            <span class="shrink-0 text-sm font-semibold tabular-nums text-gray-900 dark:text-white">×{{ formatRate(row.subscription_rate_multiplier) }}</span>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CheckoutGroupRate } from '@/types/payment'

const PANEL_ORDER = ['gpt', 'grok', 'claude', 'national', 'gemini'] as const

const props = defineProps<{ rates: CheckoutGroupRate[] }>()
const { t } = useI18n()

const sections = computed(() => {
  const buckets = new Map<string, CheckoutGroupRate[]>()
  for (const row of props.rates) {
    const panel = PANEL_ORDER.includes(row.usage_panel as (typeof PANEL_ORDER)[number]) ? row.usage_panel : ''
    const list = buckets.get(panel) ?? []
    list.push(row)
    buckets.set(panel, list)
  }
  const keys = [...PANEL_ORDER.filter(panel => buckets.has(panel)), ...(buckets.has('') ? [''] : [])]
  return keys.map(panel => ({
    panel,
    label: panel ? t(`keys.usagePanel.${panel}`) : t('payment.groupRates.other'),
    rows: buckets.get(panel) ?? [],
  }))
})

function formatRate(value: number) {
  if (!Number.isFinite(value)) return '1'
  return Number(value.toFixed(2)).toString()
}
</script>
