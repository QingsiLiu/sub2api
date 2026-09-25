<template>
  <button type="button" class="btn btn-secondary min-h-11 px-3 py-2 text-xs" @click="show = true">
    {{ t('subscriptionRights.actionHelp') }}
  </button>
  <BaseDialog :show="show" :title="t('subscriptionRights.selfServiceUnavailable')" width="normal" @close="show = false">
    <div class="space-y-3 text-sm text-gray-700 dark:text-gray-300">
      <p>{{ t(reason) }}</p>
      <p v-if="resubscribeAt" class="font-medium">
        {{ t('subscriptionRights.resubscribeAt', { time: formatDateTimeToMinute(resubscribeAt) }) }}
      </p>
      <p v-if="contactInfo" class="whitespace-pre-wrap break-words">
        {{ t('subscriptionRights.supportContact') }}: {{ contactInfo }}
      </p>
    </div>
    <template #footer>
      <button type="button" class="btn btn-primary min-h-11" @click="show = false">{{ t('common.close') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UserSubscription } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { formatDateTimeToMinute } from '@/utils/format'

const props = defineProps<{ reason: string; subscriptions: UserSubscription[]; contactInfo?: string }>()
const { t } = useI18n()
const show = ref(false)

// Explain the existing eligibility gate; never merge or change purchased rights.
const resubscribeAt = computed(() => {
  if (!['subscriptionRights.compatibilityHint', 'subscriptionRights.campaignCompatibilityHint'].includes(props.reason)) return null
  const active = props.subscriptions.filter(sub => (sub.status === 'active' || sub.status === 'suspended') && (!sub.expires_at || !Number.isFinite(Date.parse(sub.expires_at)) || Date.parse(sub.expires_at) > Date.now()))
  const expiries = active.map(sub => Date.parse(sub.expires_at ?? ''))
  if (!active.length || active.some(sub => sub.status === 'suspended') || expiries.some(at => !Number.isFinite(at))) return null
  return new Date(Math.max(...expiries))
})
</script>
