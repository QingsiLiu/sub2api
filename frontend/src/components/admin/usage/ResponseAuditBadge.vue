<template>
  <span class="inline-flex rounded px-2 py-0.5 text-xs" :class="badgeClass" :title="audit ? t(`responseAudit.reasons.${audit.reason}`) : t('responseAudit.unobservedHint')" data-testid="response-audit-badge">
    {{ audit ? t(`responseAudit.status.${audit.status}`) : t('responseAudit.unobserved') }}
  </span>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ResponseAudit } from '@/api/admin/responseAudit'
const props = defineProps<{ audit?: ResponseAudit | null }>()
const { t } = useI18n()
const badgeClass = computed(() => props.audit?.status === 'success'
  ? 'bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-300'
  : props.audit?.status === 'failed' || props.audit?.status === 'partial_failure'
    ? 'bg-rose-100 text-rose-800 dark:bg-rose-900/30 dark:text-rose-300'
    : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300')
</script>
