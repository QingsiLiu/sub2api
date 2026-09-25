<template>
  <div v-if="row.record_source || row.record_completeness || row.detail_pending" class="mt-1 flex max-w-xs flex-wrap gap-1 text-[11px] leading-4" data-testid="financial-record-status">
    <span v-if="row.record_source === 'historical_recovery'" class="rounded bg-amber-50 px-1.5 py-0.5 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300">{{ t('financial.sourceRecovery') }}</span>
    <span v-if="row.record_completeness === 'partial'" class="text-amber-700 dark:text-amber-300" :title="unknownFields">{{ t('financial.partial') }}</span>
    <span v-if="row.record_completeness === 'amount_unknown'" class="text-amber-700 dark:text-amber-300" :title="unknownFields">{{ t('financial.amountPending') }}</span>
    <span v-if="row.detail_pending" class="text-blue-700 dark:text-blue-300">{{ t('financial.pending') }}</span>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UsageLog } from '@/types'
const props = defineProps<{ row: Pick<UsageLog, 'record_source' | 'record_completeness' | 'detail_pending' | 'unknown_fields'> }>()
const { t } = useI18n()
const unknownFields = computed(() => props.row.unknown_fields?.length ? `${t('financial.unknownFields')}: ${props.row.unknown_fields.join(', ')}` : undefined)
</script>
