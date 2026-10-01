<template>
  <BaseDialog
    :show="show"
    :title="t('admin.subscriptions.grant.title')"
    width="wide"
    :close-on-escape="!submitting"
    :show-close-button="!submitting"
    @close="handleClose"
  >
    <form id="admin-grant-form" class="space-y-5" novalidate @submit.prevent="submit">
      <p class="rounded-lg bg-blue-50 p-3 text-sm text-blue-800 dark:bg-blue-900/20 dark:text-blue-200">
        {{ t('admin.subscriptions.grant.intro') }}
      </p>

      <fieldset :disabled="locked" class="space-y-4">
        <div>
          <label class="input-label" for="admin-grant-user">{{ t('admin.subscriptions.form.user') }}</label>
          <div class="relative">
            <input
              id="admin-grant-user"
              v-model="keyword"
              type="text"
              autocomplete="off"
              class="input pr-8"
              :placeholder="t('admin.usage.searchUserPlaceholder')"
              @input="debounceSearch"
              @focus="showDropdown = true"
            />
            <button
              v-if="selectedUser && !locked"
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
              :aria-label="t('common.clear')"
              @click="clearUser"
            >
              <Icon name="x" size="sm" :stroke-width="2" />
            </button>
            <div
              v-if="showDropdown && !selectedUser && keyword.trim()"
              class="absolute z-50 mt-1 max-h-60 w-full overflow-auto rounded-lg border border-gray-200 bg-white shadow-lg dark:border-dark-700 dark:bg-dark-800"
            >
              <div v-if="searching" class="px-4 py-3 text-sm text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</div>
              <div v-else-if="results.length === 0" class="px-4 py-3 text-sm text-gray-500 dark:text-gray-400">{{ t('common.noOptionsFound') }}</div>
              <button
                v-for="user in results"
                :key="user.id"
                type="button"
                class="w-full px-4 py-2 text-left text-sm hover:bg-gray-100 dark:hover:bg-dark-700"
                @click="selectUser(user)"
              >
                <span class="font-medium text-gray-900 dark:text-white">{{ user.email }}</span>
                <span class="ml-2 text-gray-500 dark:text-gray-400">#{{ user.id }}</span>
              </button>
            </div>
          </div>
        </div>

        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="input-label" for="admin-grant-daily">{{ t('admin.subscriptions.grant.dailyLimit') }}</label>
            <input id="admin-grant-daily" v-model.number="daily" type="number" min="0.01" max="10000" step="0.01" class="input" />
          </div>
          <div>
            <label class="input-label" for="admin-grant-days">{{ t('admin.subscriptions.grant.days') }}</label>
            <input id="admin-grant-days" v-model.number="days" type="number" min="1" max="3650" step="1" class="input" />
          </div>
        </div>
        <p class="input-hint -mt-2">{{ t('admin.subscriptions.grant.limitsHint') }}</p>

        <div>
          <label class="input-label" for="admin-grant-reason">{{ t('admin.subscriptions.grant.reason') }}</label>
          <textarea
            id="admin-grant-reason"
            v-model="reason"
            rows="2"
            maxlength="500"
            class="input"
            :placeholder="t('admin.subscriptions.grant.reasonPlaceholder')"
          />
          <p class="input-hint">{{ t('admin.subscriptions.grant.reasonHint') }}</p>
        </div>
      </fieldset>

      <p v-if="validationError && showValidation && !preview" role="alert" class="text-sm text-red-600 dark:text-red-400">
        {{ validationError }}
      </p>

      <section v-if="preview" class="space-y-4" aria-live="polite" data-test="grant-preview">
        <div class="grid gap-3 rounded-lg border border-gray-200 p-3 text-sm dark:border-dark-600 sm:grid-cols-3">
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.subscriptions.grant.pool') }}</p>
            <p class="font-medium text-gray-900 dark:text-gray-100">
              {{ preview.created_pool && !preview.applied ? t('admin.subscriptions.grant.newPool') : `#${preview.subscription_id}` }}
            </p>
          </div>
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.subscriptions.grant.todayLimit') }}</p>
            <p class="font-medium text-gray-900 dark:text-gray-100" data-test="grant-limit-change">
              {{ money(preview.current_daily_limit_usd) }} → {{ money(preview.after_daily_limit_usd) }}
            </p>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.subscriptions.grant.todayUsed', { amount: money(preview.daily_usage_usd) }) }}</p>
          </div>
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.subscriptions.grant.grantExpires') }}</p>
            <p class="font-medium text-gray-900 dark:text-gray-100">{{ formatDateTimeToMinute(preview.expires_at) }}</p>
          </div>
        </div>

        <div>
          <h4 class="mb-2 text-sm font-medium text-gray-900 dark:text-gray-100">{{ t('admin.subscriptions.grant.timeline') }}</h4>
          <ol class="space-y-1 text-sm" data-test="grant-timeline">
            <li v-for="segment in preview.timeline" :key="segment.starts_at" class="flex flex-wrap items-center gap-x-2 rounded bg-gray-50 px-3 py-1.5 dark:bg-dark-700">
              <span class="text-gray-600 dark:text-gray-300">{{ formatDateTimeToMinute(segment.starts_at) }} → {{ formatDateTimeToMinute(segment.ends_at) }}</span>
              <span class="font-medium text-gray-900 dark:text-gray-100">{{ t('admin.subscriptions.grant.perDay', { amount: money(segment.daily_limit_usd) }) }}</span>
            </li>
          </ol>
        </div>

        <div>
          <h4 class="mb-2 text-sm font-medium text-gray-900 dark:text-gray-100">{{ t('admin.subscriptions.lots.title') }}</h4>
          <SubscriptionLotTable :lots="preview.lots" />
        </div>

        <ul v-if="preview.warnings.length && !preview.applied" class="space-y-2" data-test="grant-warnings">
          <li v-for="warning in preview.warnings" :key="warning" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
            {{ t(`admin.subscriptions.grant.warning_${warning}`) }}
          </li>
        </ul>

        <p v-if="preview.applied" class="rounded-lg bg-emerald-50 p-3 text-sm font-medium text-emerald-800 dark:bg-emerald-900/20 dark:text-emerald-200" data-test="grant-done">
          {{ t('admin.subscriptions.grant.done', { id: preview.entitlement_id, subscription: preview.subscription_id }) }}
        </p>
      </section>

      <div v-if="requestError" role="alert" class="space-y-2 rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
        <p>{{ requestError }}</p>
        <p v-if="pending">{{ t('admin.subscriptions.grant.retryHint') }}</p>
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="submitting" @click="handleClose">
          {{ preview?.applied ? t('common.close') : t('common.cancel') }}
        </button>
        <button v-if="preview && !preview.applied && !pending" type="button" class="btn btn-secondary" :disabled="submitting" @click="backToForm">
          {{ t('admin.subscriptions.grant.edit') }}
        </button>
        <button
          v-if="!preview?.applied"
          type="submit"
          form="admin-grant-form"
          class="btn btn-primary"
          data-test="grant-submit"
          :disabled="submitting || (!preview && !!validationError && showValidation)"
        >
          {{ submitLabel }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import type { AdminGrantRequest, AdminGrantResult, AdminUser } from '@/types'
import { formatDateTimeToMinute } from '@/utils/format'
import SubscriptionLotTable from './SubscriptionLotTable.vue'
import {
  completeAdminGrantOperation,
  isDefiniteFailure,
  prepareAdminGrantOperation,
  type AdminGrantOperation,
  type ApiFailure
} from './adminGrantOperation'

defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: []; completed: [result: AdminGrantResult] }>()

const { t } = useI18n()
const keyword = ref('')
const results = ref<AdminUser[]>([])
const searching = ref(false)
const showDropdown = ref(false)
const selectedUser = ref<AdminUser | null>(null)
const daily = ref<number | string>(360)
const days = ref<number | string>(30)
const reason = ref('')
const showValidation = ref(false)
const submitting = ref(false)
const requestError = ref('')
const preview = shallowRef<AdminGrantResult | null>(null)
const pending = shallowRef<AdminGrantOperation<AdminGrantRequest> | null>(null)
let searchTimer: ReturnType<typeof setTimeout> | null = null

// Terms are frozen once previewed: the snapshot binds exactly what was reviewed.
const locked = computed(() => submitting.value || !!preview.value)
const validationError = computed(() => {
  if (!selectedUser.value) return t('admin.subscriptions.grant.userRequired')
  const d = Number(daily.value)
  if (!Number.isFinite(d) || d <= 0 || d > 10000) return t('admin.subscriptions.grant.invalidDaily')
  const n = Number(days.value)
  if (!Number.isInteger(n) || n < 1 || n > 3650) return t('admin.subscriptions.grant.invalidDays')
  const r = reason.value.trim()
  if (!r || r.length > 500) return t('admin.subscriptions.grant.reasonRequired')
  return ''
})
const submitLabel = computed(() => {
  if (submitting.value) return t('common.processing')
  if (!preview.value) return t('admin.subscriptions.grant.preview')
  return pending.value ? t('admin.subscriptions.grant.retry') : t('admin.subscriptions.grant.confirm')
})

function money(value: number | null | undefined) {
  if (value == null) return t('subscriptionRights.unlimited')
  return `$${Number(value.toFixed(2))}`
}

function request(): AdminGrantRequest {
  return {
    user_id: selectedUser.value!.id,
    daily_limit_usd: Number(daily.value),
    days: Number(days.value),
    reason: reason.value.trim()
  }
}

function debounceSearch() {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(searchUsers, 300)
}

async function searchUsers() {
  const value = keyword.value.trim()
  if (!value) {
    results.value = []
    return
  }
  searching.value = true
  try {
    results.value = (await adminAPI.users.list(1, 30, { search: value, sort_by: 'email', sort_order: 'asc' })).items
  } catch {
    results.value = []
  } finally {
    searching.value = false
  }
}

function selectUser(user: AdminUser) {
  selectedUser.value = user
  keyword.value = user.email
  showDropdown.value = false
}

function clearUser() {
  selectedUser.value = null
  keyword.value = ''
  results.value = []
}

function backToForm() {
  if (submitting.value || pending.value) return
  preview.value = null
  requestError.value = ''
}

function handleClose() {
  if (!submitting.value) emit('close')
}

function failureMessage(error: ApiFailure) {
  if (error?.reason === 'ADMIN_GRANT_SNAPSHOT_MISMATCH') return t('admin.subscriptions.grant.snapshotChanged')
  return error?.message || t('admin.subscriptions.grant.requestFailed')
}

async function submit() {
  if (submitting.value || preview.value?.applied) return
  if (!preview.value) {
    showValidation.value = true
    if (validationError.value) return
    submitting.value = true
    requestError.value = ''
    try {
      preview.value = await adminAPI.subscriptions.previewGrant(request())
    } catch (error: unknown) {
      requestError.value = failureMessage(error as ApiFailure)
    } finally {
      submitting.value = false
    }
    return
  }

  // The key is bound to the terms, not the snapshot: if an earlier attempt
  // landed unseen, a re-preview with the same terms replays that grant.
  if (!pending.value) pending.value = prepareAdminGrantOperation('grant', request())
  const operation = pending.value
  submitting.value = true
  requestError.value = ''
  try {
    const result = await adminAPI.subscriptions.applyGrant({ ...operation.payload, expected_snapshot: preview.value.snapshot }, operation.key)
    completeAdminGrantOperation(operation)
    pending.value = null
    preview.value = result
    emit('completed', result)
  } catch (error: unknown) {
    const failure = error as ApiFailure
    requestError.value = failureMessage(failure)
    if (isDefiniteFailure(failure)) {
      completeAdminGrantOperation(operation)
      pending.value = null
      // The user's rights changed or the grant was refused: review again.
      preview.value = null
    } else {
      operation.outcomeUncertain = true
    }
  } finally {
    submitting.value = false
  }
}

onUnmounted(() => {
  if (searchTimer) clearTimeout(searchTimer)
})
</script>
