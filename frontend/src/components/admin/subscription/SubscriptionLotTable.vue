<template>
  <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
    <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-600">
      <thead class="bg-gray-50 text-left text-xs text-gray-500 dark:bg-dark-700 dark:text-gray-400">
        <tr>
          <th class="px-3 py-2 font-medium">#</th>
          <th class="px-3 py-2 font-medium">{{ t('admin.subscriptions.lots.source') }}</th>
          <th class="px-3 py-2 font-medium">{{ t('subscriptionRights.daily') }}</th>
          <th class="px-3 py-2 font-medium">{{ t('admin.subscriptions.lots.period') }}</th>
          <th class="px-3 py-2 font-medium">{{ t('subscriptionRights.status') }}</th>
          <th v-if="$slots.actions" class="px-3 py-2"></th>
        </tr>
      </thead>
      <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
        <tr v-for="lot in lots" :key="lot.id || 'new'" :class="isNew(lot) ? 'bg-primary-50 dark:bg-primary-900/20' : ''">
          <td class="whitespace-nowrap px-3 py-2 text-gray-500 dark:text-gray-400">{{ isNew(lot) ? t('admin.subscriptions.lots.new') : lot.id }}</td>
          <td class="whitespace-nowrap px-3 py-2">
            <span :class="['rounded-full px-2 py-0.5 text-xs font-medium', sourceClass(lot.source_type)]">
              {{ t(`admin.subscriptions.lots.source_${sourceKey(lot.source_type)}`) }}
            </span>
          </td>
          <td class="whitespace-nowrap px-3 py-2 text-gray-900 dark:text-gray-100">
            {{ lot.daily_limit_usd == null || lot.daily_limit_usd === 0 ? t('subscriptionRights.unlimited') : `$${lot.daily_limit_usd}` }}
          </td>
          <td class="whitespace-nowrap px-3 py-2 text-gray-600 dark:text-gray-300">
            {{ formatDateTimeToMinute(lot.starts_at) }} → {{ formatDateTimeToMinute(lot.expires_at) }}
          </td>
          <td class="whitespace-nowrap px-3 py-2 text-gray-600 dark:text-gray-300">{{ t(`subscriptionRights.status_${effectiveStatus(lot)}`) }}</td>
          <td v-if="$slots.actions" class="whitespace-nowrap px-3 py-2 text-right">
            <slot name="actions" :lot="lot" :live="effectiveStatus(lot) === 'active'" />
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { formatDateTimeToMinute } from '@/utils/format'

export interface SubscriptionLotRow {
  id: number
  source_type: string
  status: string
  starts_at: string
  expires_at: string
  daily_limit_usd: number | null
  new?: boolean
}

defineProps<{ lots: SubscriptionLotRow[] }>()
defineSlots<{ actions?: (props: { lot: SubscriptionLotRow; live: boolean }) => unknown }>()

const { t } = useI18n()
const knownSources = ['payment', 'campaign', 'admin_grant'] as const

function isNew(lot: SubscriptionLotRow) {
  return !!lot.new
}

function sourceKey(source: string) {
  return (knownSources as readonly string[]).includes(source) ? source : 'legacy'
}

function sourceClass(source: string) {
  switch (source) {
    case 'admin_grant':
      return 'bg-violet-100 text-violet-700 dark:bg-violet-900/40 dark:text-violet-300'
    case 'campaign':
      return 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
    case 'payment':
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
    default:
      return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
  }
}

function effectiveStatus(lot: SubscriptionLotRow) {
  if (lot.status === 'active') {
    if (Date.parse(lot.expires_at) <= Date.now()) return 'expired'
  }
  return lot.status
}
</script>
