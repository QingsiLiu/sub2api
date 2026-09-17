<template>
  <div v-if="rates.length" class="card p-4">
    <div class="mb-3">
      <p class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('payment.groupRates.title') }}</p>
      <p class="mt-0.5 text-xs text-gray-400 dark:text-gray-500">{{ t('payment.groupRates.hint') }}</p>
    </div>
    <div class="space-y-3">
      <section v-for="section in sections" :key="section.panel || 'other'">
        <p class="mb-1.5 text-xs font-medium text-gray-500 dark:text-gray-400">{{ section.label }}</p>
        <div class="flex flex-wrap gap-1.5">
          <GroupBadge
            v-for="row in section.rows"
            :key="row.id"
            :name="row.name"
            :platform="row.platform"
            :rate-multiplier="row.subscription_rate_multiplier"
            always-show-rate
          />
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import GroupBadge from '@/components/common/GroupBadge.vue'
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
</script>
