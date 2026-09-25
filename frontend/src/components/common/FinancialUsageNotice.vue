<template>
  <div v-if="hasIncomplete" class="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-xs leading-5 text-amber-900 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200" role="status" data-testid="financial-usage-notice">
    <p v-if="label" class="font-medium">{{ label }}</p>
    <p v-if="stats.detail_pending_count">{{ t('financial.detailPending', { count: stats.detail_pending_count }) }}</p>
    <p v-if="stats.unknown_amount_count">{{ t('financial.amountUnknown', { count: stats.unknown_amount_count }) }}</p>
    <p v-if="stats.incomplete_record_count">{{ t('financial.incomplete', { count: stats.incomplete_record_count }) }}</p>
    <p v-if="!stats.incomplete_record_count && (stats.standard_cost_complete === false || stats.token_counts_complete === false)">{{ t('financial.partialTotal') }}</p>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { FinancialStatsMetadata } from '@/types'
const props = defineProps<{ stats: FinancialStatsMetadata; label?: string }>()
const { t } = useI18n()
const hasIncomplete = computed(() => (props.stats.detail_pending_count ?? 0) > 0 || (props.stats.unknown_amount_count ?? 0) > 0 || (props.stats.incomplete_record_count ?? 0) > 0 || props.stats.standard_cost_complete === false || props.stats.token_counts_complete === false)
</script>
