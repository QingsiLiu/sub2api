<template>
  <ul class="mt-3 space-y-2 text-xs text-gray-700 dark:text-gray-300">
    <li v-for="(line, index) in lines" :key="index" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
      <p class="font-medium">{{ line.entitlement_id ? '#' + line.entitlement_id : t('subscriptionRights.legacyNewUnit') }}</p>
      <p v-if="line.before">{{ t('subscriptionRights.before') }}: ${{ line.before.daily_limit_usd }}/{{ t('subscriptionRights.daily') }} · {{ formatDateTimeToMinute(line.before.expires_at) }}</p>
      <p>{{ t('subscriptionRights.after') }}: ${{ line.after.daily_limit_usd }}/{{ t('subscriptionRights.daily') }} · {{ formatDateTimeToMinute(line.after.expires_at) }}</p>
      <p>{{ t('subscriptionRights.legacyLinePrice') }}: ${{ line.amount }} · {{ line.billable_days }} {{ t('payment.days') }}</p>
    </li>
  </ul>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { LegacyEntitlementChange } from '@/types/payment'
import { formatDateTimeToMinute } from '@/utils/format'
defineProps<{ lines: LegacyEntitlementChange[] }>()
const { t } = useI18n()
</script>
