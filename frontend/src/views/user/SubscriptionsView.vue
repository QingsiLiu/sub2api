<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Loading State -->
      <div v-if="loading" class="flex justify-center py-12">
        <div
          class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent"
        ></div>
      </div>

      <!-- Empty State -->
      <div v-else-if="subscriptions.length === 0" class="card p-12 text-center">
        <div
          class="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-gray-100 dark:bg-dark-700"
        >
          <Icon name="creditCard" size="xl" class="text-gray-400" />
        </div>
        <h3 class="mb-2 text-lg font-semibold text-gray-900 dark:text-white">
          {{ t('userSubscriptions.noActiveSubscriptions') }}
        </h3>
        <p class="text-gray-500 dark:text-dark-400">
          {{ t('userSubscriptions.noActiveSubscriptionsDesc') }}
        </p>
      </div>

      <!-- Subscriptions Grid -->
      <div v-else class="grid gap-6 lg:grid-cols-2">
        <div
          v-for="subscription in subscriptions"
          :key="subscription.id"
          class="overflow-hidden rounded-2xl border bg-white dark:bg-dark-800"
          :class="platformBorderClass(subscription.group?.platform || '')"
        >
          <!-- Header -->
          <div
            class="flex flex-col items-stretch gap-3 border-b border-gray-100 p-4 dark:border-dark-700"
          >
            <div class="flex min-w-0 items-start gap-3">
              <div :class="['h-1.5 w-1.5 shrink-0 rounded-full', platformAccentDotClass(subscription.group?.platform || '')]" />
              <div class="min-w-0 flex-1 break-words">
                <div class="flex items-center gap-2">
                  <h3 class="font-semibold text-gray-900 dark:text-white">
                    {{ subscriptionLabel(subscription, t) }}
                  </h3>
                  <span v-if="!subscription.plan" :class="['rounded-md border px-2 py-0.5 text-[11px] font-medium', platformBadgeClass(subscription.group?.platform || '')]">
                    {{ platformLabel(subscription.group?.platform || '') }}
                  </span>
                </div>
                <p v-if="subscription.group?.description" class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
                  {{ subscription.group.description }}
                </p>
                <p v-if="!subscription.plan && (subscription.entitled_group_ids?.length || 0) > 1" class="mt-1 text-xs text-gray-500 dark:text-dark-400">
                  {{ t('userSubscriptions.includedGroups') }}: {{ subscription.entitled_group_ids?.join(', ') }}
                </p>
                <div class="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-gray-400 dark:text-gray-500">
                  <span v-if="!subscription.plan">{{ t('payment.planCard.rate') }}: ×{{ subscription.group?.rate_multiplier ?? 1 }}</span>
                  <span v-if="subscriptionHasPeakRate(subscription)" class="text-amber-700 dark:text-amber-300">
                    {{ t('payment.planCard.peakRate') }}: {{ subscriptionPeakRateLabel(subscription) }}
                  </span>
                </div>
                <p v-if="subscription.contract?.mode === 'legacy_daily' && !campaignOnly(subscription)" class="mt-2 text-xs text-amber-700 dark:text-amber-300">{{ t('subscriptionRights.compatibilityHint') }}</p>
                <div v-if="subscription.quota_summary" class="mt-2 rounded-md bg-gray-50 px-2 py-1.5 text-xs text-gray-600 dark:bg-dark-700/50 dark:text-gray-300">
                  <span>{{ t('subscriptionRights.active', { count: subscription.quota_summary.active_lot_count }) }}</span>
                  <span v-if="subscription.quota_summary.next_expiry_at" class="ml-3">{{ t('subscriptionRights.nextExpiry', { time: formatDateTimeToMinute(subscription.quota_summary.next_expiry_at) }) }}</span>
                  <details v-if="subscription.entitlements?.length" class="mt-2">
                    <summary class="cursor-pointer font-medium">{{ t('subscriptionRights.details', { count: subscription.entitlements.length }) }}</summary>
                    <ul class="mt-2 space-y-2">
                      <li v-for="lot in subscription.entitlements" :key="lot.id" class="rounded border border-gray-200 p-2 dark:border-dark-600">
                        <p v-if="lot.source_type === 'campaign'" class="font-medium">{{ t('subscriptionRights.campaignGift') }} · {{ t('subscriptionRights.campaignPriority') }}</p>
                        <p>#{{ lot.id }} · {{ t('subscriptionRights.status') }}: {{ t(`subscriptionRights.status_${lot.status}`) }}</p>
                        <p>{{ t('subscriptionRights.created') }}: {{ formatDateTimeToMinute(lot.created_at) }}</p>
                        <p>{{ formatDateTimeToMinute(lot.starts_at) }} → {{ formatDateTimeToMinute(lot.expires_at) }}</p>
                        <p v-if="lot.source_order_id">{{ t('subscriptionRights.sourceOrder') }} #{{ lot.source_order_id }}</p>
                        <p v-for="period in (subscription.contract ? ['daily'] as const : ['daily', 'weekly', 'monthly'] as const)" :key="period">{{ t(`subscriptionRights.${period}`) }}: {{ lot[`${period}_limit_usd`] == null || lot[`${period}_limit_usd`] === 0 ? t('subscriptionRights.unlimited') : '$' + lot[`${period}_limit_usd`] }}</p>
                      </li>
                    </ul>
                    <h4 v-if="subscription.entitlement_operations?.length" class="mt-3 font-medium">{{ t('subscriptionRights.history') }}</h4>
                    <ul class="mt-1 space-y-1"><li v-for="op in subscription.entitlement_operations" :key="op.id">{{ formatDateTimeToMinute(op.created_at) }} · #{{ op.entitlement_id }} · {{ t(`subscriptionRights.operation_${op.operation}`) }} <span v-if="op.source_type === 'payment' && op.source_reference">· {{ t('subscriptionRights.sourceOrder') }} #{{ op.source_reference }}</span> <span v-if="op.after_expires_at">→ {{ formatDateTimeToMinute(op.after_expires_at) }}</span></li></ul>
                  </details>
                </div>
              </div>
            </div>
            <div class="flex shrink-0 items-center justify-end gap-2 whitespace-nowrap">
              <span
                :class="[
                  'rounded-full px-2 py-0.5 text-xs font-medium',
                  subscription.status === 'active'
                    ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
                    : subscription.status === 'expired'
                      ? 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-400'
                      : 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
                ]"
              >
                {{ t(`userSubscriptions.status.${subscription.status}`) }}
              </span>
              <template v-if="subscription.status === 'active' && subscription.contract?.mode === 'v2'">
                <button v-for="operation in (subscription.contract.unit_daily_usd < 180 ? ['stack', 'renew', 'upgrade'] as const : ['stack', 'renew'] as const)" :key="operation" class="btn btn-primary px-3 py-1.5 text-xs" @click="router.push({ path: '/purchase', query: { tab: 'subscription', ...(operation === 'upgrade' ? {} : { plan: String(subscription.contract.plan_id) }), operation } })">{{ t(`subscriptionRights.${operation}`) }}</button>
              </template>
            </div>
          </div>

          <!-- Usage Progress -->
          <div class="space-y-4 p-4">
            <!-- Expiration Info -->
            <div v-if="subscription.expires_at" class="flex items-center justify-between text-sm">
              <span class="text-gray-500 dark:text-dark-400">{{
                t('userSubscriptions.expires')
              }}</span>
              <span :class="getExpirationClass(subscription.expires_at)">
                {{ formatExpirationDate(subscription.expires_at) }}
              </span>
            </div>
            <div v-else class="flex items-center justify-between text-sm">
              <span class="text-gray-500 dark:text-dark-400">{{
                t('userSubscriptions.expires')
              }}</span>
              <span class="text-gray-700 dark:text-gray-300">{{
                t('userSubscriptions.noExpiration')
              }}</span>
            </div>

            <div v-if="subscription.contract" class="space-y-1 text-sm">
              <p class="font-medium text-gray-900 dark:text-white">{{ t('subscriptionRights.remaining') }}: {{ subscriptionQuota(subscription).remaining == null ? t('subscriptionRights.unlimited') : '$' + subscriptionQuota(subscription).remaining!.toFixed(4) }}</p>
              <p class="text-xs text-gray-500">{{ t('subscriptionRights.dailyReset') }}</p>
            </div>
            <!-- Daily Usage -->
            <div v-if="subscriptionQuota(subscription).daily" class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('userSubscriptions.daily') }}
                </span>
                <span class="text-sm text-gray-500 dark:text-dark-400">
                  ${{ (subscriptionQuota(subscription).dailyUsed || 0).toFixed(2) }} / ${{
                    subscriptionQuota(subscription).daily!.toFixed(2)
                  }}
                </span>
              </div>
              <div class="relative h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                <div
                  class="absolute inset-y-0 left-0 rounded-full transition-all duration-300"
                  :class="
                    getProgressBarClass(
                      subscriptionQuota(subscription).dailyUsed,
                      subscriptionQuota(subscription).daily
                    )
                  "
                  :style="{
                    width: getProgressWidth(
                      subscriptionQuota(subscription).dailyUsed,
                      subscriptionQuota(subscription).daily
                    )
                  }"
                ></div>
              </div>
              <p
                v-if="getSubscriptionResetAt(subscription, 'daily')"
                class="text-xs text-gray-500 dark:text-dark-400"
              >
                {{ formatDailyUsageWindow(subscription) }}
              </p>
            </div>

            <!-- Weekly Usage -->
            <div v-if="subscriptionQuota(subscription).weekly" class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('userSubscriptions.weekly') }}
                </span>
                <span class="text-sm text-gray-500 dark:text-dark-400">
                  ${{ (subscriptionQuota(subscription).weeklyUsed || 0).toFixed(2) }} / ${{
                    subscriptionQuota(subscription).weekly!.toFixed(2)
                  }}
                </span>
              </div>
              <div class="relative h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                <div
                  class="absolute inset-y-0 left-0 rounded-full transition-all duration-300"
                  :class="
                    getProgressBarClass(
                      subscriptionQuota(subscription).weeklyUsed,
                      subscriptionQuota(subscription).weekly
                    )
                  "
                  :style="{
                    width: getProgressWidth(
                      subscriptionQuota(subscription).weeklyUsed,
                      subscriptionQuota(subscription).weekly
                    )
                  }"
                ></div>
              </div>
              <p
                v-if="getSubscriptionResetAt(subscription, 'weekly')"
                class="text-xs text-gray-500 dark:text-dark-400"
              >
                {{
                  t('userSubscriptions.resetIn', {
                    time: formatResetTime(subscription, 'weekly')
                  })
                }}
              </p>
            </div>

            <!-- Monthly Usage -->
            <div v-if="subscriptionQuota(subscription).monthly" class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('userSubscriptions.monthly') }}
                </span>
                <span class="text-sm text-gray-500 dark:text-dark-400">
                  ${{ (subscriptionQuota(subscription).monthlyUsed || 0).toFixed(2) }} / ${{
                    subscriptionQuota(subscription).monthly!.toFixed(2)
                  }}
                </span>
              </div>
              <div class="relative h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                <div
                  class="absolute inset-y-0 left-0 rounded-full transition-all duration-300"
                  :class="
                    getProgressBarClass(
                      subscriptionQuota(subscription).monthlyUsed,
                      subscriptionQuota(subscription).monthly
                    )
                  "
                  :style="{
                    width: getProgressWidth(
                      subscriptionQuota(subscription).monthlyUsed,
                      subscriptionQuota(subscription).monthly
                    )
                  }"
                ></div>
              </div>
              <p
                v-if="getSubscriptionResetAt(subscription, 'monthly')"
                class="text-xs text-gray-500 dark:text-dark-400"
              >
                {{
                  t('userSubscriptions.resetIn', {
                    time: formatResetTime(subscription, 'monthly')
                  })
                }}
              </p>
            </div>

            <!-- No limits configured - Unlimited badge -->
            <div
              v-if="
                (!subscription.quota_summary || subscription.quota_summary.active_lot_count > 0) &&
                !subscriptionQuota(subscription).daily &&
                !subscriptionQuota(subscription).weekly &&
                !subscriptionQuota(subscription).monthly
              "
              class="flex items-center justify-center rounded-xl bg-gradient-to-r from-emerald-50 to-teal-50 py-6 dark:from-emerald-900/20 dark:to-teal-900/20"
            >
              <div class="flex items-center gap-3">
                <span class="text-4xl text-emerald-600 dark:text-emerald-400">∞</span>
                <div>
                  <p class="text-sm font-medium text-emerald-700 dark:text-emerald-300">
                    {{ t('userSubscriptions.unlimited') }}
                  </p>
                  <p class="text-xs text-emerald-600/70 dark:text-emerald-400/70">
                    {{ t('userSubscriptions.unlimitedDesc') }}
                  </p>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { subscriptionQuota, subscriptionLabel, campaignOnly, subscriptionRefreshDelay } from '@/utils/subscriptionV2'
import { ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import subscriptionsAPI from '@/api/subscriptions'
import type { UserSubscription } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTimeToMinute } from '@/utils/format'
import { hasPeakRate, formatPeakRateWindow, serverTimezoneLabel } from '@/utils/peak-rate'
import { platformBorderClass, platformBadgeClass, platformLabel } from '@/utils/platformColors'
import {
  getExpirationDateRelation,
  getRemainingDurationParts,
  getSubscriptionResetAt,
  type SubscriptionQuotaPeriod,
  isOneTimeDailyQuota,
  type RemainingDurationParts
} from '@/utils/subscriptionQuota'

function platformAccentDotClass(p: string): string {
  switch (p) {
    case 'anthropic': return 'bg-orange-500'
    case 'openai': return 'bg-emerald-500'
    case 'antigravity': return 'bg-purple-500'
    case 'gemini': return 'bg-blue-500'
    default: return 'bg-gray-400'
  }
}

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()

const subscriptions = ref<UserSubscription[]>([])
const loading = ref(true)
let refreshTimeout: ReturnType<typeof setTimeout> | undefined
let unmounted = false

function subscriptionHasPeakRate(subscription: UserSubscription): boolean {
  return hasPeakRate(subscription.group)
}

function subscriptionPeakRateLabel(subscription: UserSubscription): string {
  return formatPeakRateWindow(subscription.group, serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset))
}

async function loadSubscriptions(showSpinner = true) {
  try {
    if (showSpinner) loading.value = true
    subscriptions.value = await subscriptionsAPI.getMySubscriptions()
  } catch (error) {
    console.error('Failed to load subscriptions:', error)
    appStore.showError(t('userSubscriptions.failedToLoad'))
  } finally {
    loading.value = false
    if (!unmounted) refreshTimeout = setTimeout(() => loadSubscriptions(false), Math.min(300_000, subscriptionRefreshDelay(subscriptions.value) ?? 300_000))
  }
}

function getProgressWidth(used: number | undefined, limit: number | null | undefined): string {
  if (!limit || limit === 0) return '0%'
  const percentage = Math.min(((used || 0) / limit) * 100, 100)
  return `${percentage}%`
}

function getProgressBarClass(used: number | undefined, limit: number | null | undefined): string {
  if (!limit || limit === 0) return 'bg-gray-400'
  const percentage = ((used || 0) / limit) * 100
  if (percentage >= 90) return 'bg-red-500'
  if (percentage >= 70) return 'bg-orange-500'
  return 'bg-green-500'
}

function formatExpirationDate(expiresAt: string): string {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diff = expires.getTime() - now.getTime()
  const days = Math.ceil(diff / (1000 * 60 * 60 * 24))
  const relation = getExpirationDateRelation(expires, now)

  if (relation === null) return ''

  if (relation === 'expired') {
    return t('userSubscriptions.status.expired')
  }

  const dateStr = formatDateTimeToMinute(expires)

  if (relation === 'today') {
    return `${dateStr} (${t('common.today')})`
  }
  if (relation === 'tomorrow') {
    return `${dateStr} (${t('common.tomorrow')})`
  }

  return t('userSubscriptions.daysRemaining', { days }) + ` (${dateStr})`
}

function getExpirationClass(expiresAt: string): string {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diff = expires.getTime() - now.getTime()
  const days = Math.ceil(diff / (1000 * 60 * 60 * 24))

  if (diff <= 0) return 'text-red-600 dark:text-red-400 font-medium'
  if (days <= 3) return 'text-red-600 dark:text-red-400'
  if (days <= 7) return 'text-orange-600 dark:text-orange-400'
  return 'text-gray-700 dark:text-gray-300'
}

function formatDurationParts(parts: RemainingDurationParts): string {
  if (parts.days > 0) {
    return `${parts.days}d ${parts.hours}h`
  }

  if (parts.hours > 0) {
    return `${parts.hours}h ${parts.minutes}m`
  }

  return `${parts.minutes}m`
}

function formatDailyUsageWindow(subscription: UserSubscription): string {
  if (!subscription.contract && isOneTimeDailyQuota(subscription) && subscription.expires_at) {
    const parts = getRemainingDurationParts(subscription.expires_at)
    if (!parts) return t('userSubscriptions.windowNotActive')
    return t('userSubscriptions.quotaEndsIn', { time: formatDurationParts(parts) })
  }

  return t('userSubscriptions.resetIn', {
    time: formatResetTime(subscription, 'daily')
  })
}

function formatResetTime(subscription: UserSubscription, period: SubscriptionQuotaPeriod): string {
  const resetAt = getSubscriptionResetAt(subscription, period)
  const parts = resetAt ? getRemainingDurationParts(resetAt) : null
  return parts ? formatDurationParts(parts) : t('userSubscriptions.windowNotActive')
}

onUnmounted(() => { unmounted = true; if (refreshTimeout) clearTimeout(refreshTimeout) })

onMounted(() => {
  loadSubscriptions()
})
</script>
