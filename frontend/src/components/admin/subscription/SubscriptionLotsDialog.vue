<template>
  <BaseDialog
    :show="show"
    :title="t('admin.subscriptions.lots.dialogTitle', { id: subscriptionId })"
    width="extra-wide"
    :close-on-escape="!terminating"
    :show-close-button="!terminating"
    @close="handleClose"
  >
    <div class="space-y-5">
      <p v-if="email" class="text-sm text-gray-600 dark:text-gray-300">{{ email }}</p>
      <div v-if="loading" class="py-8 text-center text-sm text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</div>
      <div v-else-if="loadError" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
        {{ loadError }}
        <button type="button" class="btn btn-secondary btn-sm ml-3" @click="load">{{ t('admin.subscriptions.lots.reload') }}</button>
      </div>

      <template v-else-if="view">
        <div class="grid gap-3 rounded-lg border border-gray-200 p-3 text-sm dark:border-dark-600 sm:grid-cols-4">
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.subscriptions.lots.mode') }}</p>
            <p class="font-medium text-gray-900 dark:text-gray-100">
              {{ view.contract ? t(`admin.subscriptions.lots.mode_${view.contract.mode}`) : '-' }}
            </p>
          </div>
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.subscriptions.lots.todayLimit') }}</p>
            <p class="font-medium text-gray-900 dark:text-gray-100" data-test="lots-daily-limit">{{ money(view.summary.daily_limit_usd) }}</p>
          </div>
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('subscriptionRights.remaining') }}</p>
            <p class="font-medium text-gray-900 dark:text-gray-100">{{ money(view.summary.remaining_usd ?? null) }}</p>
          </div>
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.subscriptions.lots.poolExpires') }}</p>
            <p class="font-medium text-gray-900 dark:text-gray-100">{{ view.summary.expires_at ? formatDateTimeToMinute(view.summary.expires_at) : '-' }}</p>
          </div>
        </div>

        <div v-if="view.timeline.length">
          <h4 class="mb-2 text-sm font-medium text-gray-900 dark:text-gray-100">{{ t('admin.subscriptions.grant.timeline') }}</h4>
          <ol class="space-y-1 text-sm">
            <li v-for="segment in view.timeline" :key="segment.starts_at" class="flex flex-wrap items-center gap-x-2 rounded bg-gray-50 px-3 py-1.5 dark:bg-dark-700">
              <span class="text-gray-600 dark:text-gray-300">{{ formatDateTimeToMinute(segment.starts_at) }} → {{ formatDateTimeToMinute(segment.ends_at) }}</span>
              <span class="font-medium text-gray-900 dark:text-gray-100">{{ t('admin.subscriptions.grant.perDay', { amount: money(segment.daily_limit_usd) }) }}</span>
            </li>
          </ol>
        </div>

        <div>
          <h4 class="mb-2 text-sm font-medium text-gray-900 dark:text-gray-100">{{ t('admin.subscriptions.lots.title') }}</h4>
          <SubscriptionLotTable :lots="view.lots">
            <template #actions="{ lot, live }">
              <button
                v-if="live && lot.source_type === 'admin_grant'"
                type="button"
                class="btn btn-danger btn-sm"
                :data-test="`terminate-${lot.id}`"
                :disabled="!!terminateTarget"
                @click="startTerminate(lot.id)"
              >
                {{ t('admin.subscriptions.lots.terminate') }}
              </button>
            </template>
          </SubscriptionLotTable>
          <p class="input-hint mt-2">{{ t('admin.subscriptions.lots.adjustHint') }}</p>
        </div>

        <form
          v-if="terminateTarget"
          class="space-y-3 rounded-lg border border-red-200 p-3 dark:border-red-900/50"
          data-test="terminate-form"
          novalidate
          @submit.prevent="terminate"
        >
          <p class="text-sm font-medium text-red-700 dark:text-red-300">{{ t('admin.subscriptions.lots.terminateTitle', { id: terminateTarget }) }}</p>
          <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.subscriptions.lots.terminateHint') }}</p>
          <textarea
            v-model="terminateReason"
            rows="2"
            maxlength="500"
            class="input"
            :disabled="terminating || !!terminatePending"
            :placeholder="t('admin.subscriptions.grant.reasonPlaceholder')"
          />
          <div v-if="terminateError" role="alert" class="space-y-1 text-sm text-red-600 dark:text-red-400">
            <p>{{ terminateError }}</p>
            <p v-if="terminatePending">{{ t('admin.subscriptions.grant.retryHint') }}</p>
          </div>
          <div class="flex justify-end gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="terminating || !!terminatePending" @click="cancelTerminate">{{ t('common.cancel') }}</button>
            <button type="submit" class="btn btn-danger btn-sm" :disabled="terminating || !terminateReason.trim()">
              {{ terminating ? t('common.processing') : terminatePending ? t('admin.subscriptions.grant.retry') : t('admin.subscriptions.lots.terminateConfirm') }}
            </button>
          </div>
        </form>

        <div>
          <h4 class="mb-2 text-sm font-medium text-gray-900 dark:text-gray-100">{{ t('subscriptionRights.history') }}</h4>
          <p v-if="!view.operations.length" class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.subscriptions.lots.noHistory') }}</p>
          <ul v-else class="max-h-64 space-y-1 overflow-y-auto text-sm" data-test="lots-history">
            <li v-for="op in view.operations" :key="op.id" class="rounded bg-gray-50 px-3 py-1.5 dark:bg-dark-700">
              <span class="text-gray-500 dark:text-gray-400">{{ formatDateTimeToMinute(op.created_at) }}</span>
              · #{{ op.entitlement_id }} · {{ operationLabel(op.operation) }}
              <span v-if="op.actor_email" class="text-gray-500 dark:text-gray-400">· {{ op.actor_email }}</span>
              <span v-if="op.source_type === 'payment' && op.source_reference" class="text-gray-500 dark:text-gray-400">· {{ t('subscriptionRights.sourceOrder') }} #{{ op.source_reference }}</span>
              <p v-if="op.reason" class="mt-0.5 break-words text-gray-700 dark:text-gray-200">{{ t('admin.subscriptions.lots.reason', { reason: op.reason }) }}</p>
            </li>
          </ul>
        </div>
      </template>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" :disabled="terminating" @click="handleClose">{{ t('common.close') }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { onMounted, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { AdminEntitlementView } from '@/types'
import { formatDateTimeToMinute } from '@/utils/format'
import SubscriptionLotTable from './SubscriptionLotTable.vue'
import {
  completeAdminGrantOperation,
  isDefiniteFailure,
  prepareAdminGrantOperation,
  type AdminGrantOperation,
  type ApiFailure
} from './adminGrantOperation'

const props = defineProps<{ show: boolean; subscriptionId: number; email?: string }>()
const emit = defineEmits<{ close: []; changed: [] }>()

const { t, te } = useI18n()
const loading = ref(false)
const loadError = ref('')
const view = shallowRef<AdminEntitlementView | null>(null)
const terminateTarget = ref<number | null>(null)
const terminateReason = ref('')
const terminateError = ref('')
const terminating = ref(false)
const terminatePending = shallowRef<AdminGrantOperation<{ subscription_id: number; entitlement_id: number; reason: string }> | null>(null)

function money(value: number | null | undefined) {
  if (value == null) return t('subscriptionRights.unlimited')
  return `$${Number(value.toFixed(2))}`
}

function operationLabel(operation: string) {
  const key = `subscriptionRights.operation_${operation}`
  return te(key) ? t(key) : operation
}

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    view.value = await adminAPI.subscriptions.getEntitlements(props.subscriptionId)
  } catch (error: unknown) {
    loadError.value = (error as ApiFailure)?.message || t('admin.subscriptions.lots.loadFailed')
  } finally {
    loading.value = false
  }
}

function handleClose() {
  if (!terminating.value) emit('close')
}

function startTerminate(id: number) {
  terminateTarget.value = id
  terminateReason.value = ''
  terminateError.value = ''
}

function cancelTerminate() {
  if (terminating.value || terminatePending.value) return
  terminateTarget.value = null
}

async function terminate() {
  if (terminating.value || terminateTarget.value == null) return
  const reason = terminateReason.value.trim()
  if (!terminatePending.value) {
    if (!reason) return
    terminatePending.value = prepareAdminGrantOperation('terminate', {
      subscription_id: props.subscriptionId,
      entitlement_id: terminateTarget.value,
      reason
    })
  }
  const operation = terminatePending.value
  terminating.value = true
  terminateError.value = ''
  try {
    await adminAPI.subscriptions.terminateGrant(
      operation.payload.subscription_id,
      operation.payload.entitlement_id,
      operation.payload.reason,
      operation.key
    )
    completeAdminGrantOperation(operation)
    terminatePending.value = null
    terminateTarget.value = null
    emit('changed')
    await load()
  } catch (error: unknown) {
    const failure = error as ApiFailure
    terminateError.value = failure?.message || t('admin.subscriptions.grant.requestFailed')
    if (isDefiniteFailure(failure)) {
      completeAdminGrantOperation(operation)
      terminatePending.value = null
    } else {
      operation.outcomeUncertain = true
    }
  } finally {
    terminating.value = false
  }
}

onMounted(load)
</script>
