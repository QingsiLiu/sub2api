<template>
  <div class="space-y-6">
    <section class="relative overflow-hidden rounded-3xl border border-gray-200/80 bg-white px-4 py-8 text-center shadow-card dark:border-dark-700 dark:bg-dark-900 sm:px-8 sm:py-12">
      <div class="pointer-events-none absolute inset-x-0 top-0 h-72 bg-[radial-gradient(circle_at_20%_15%,rgba(20,184,166,0.18),transparent_42%),radial-gradient(circle_at_82%_12%,rgba(59,130,246,0.12),transparent_38%)] dark:opacity-60"></div>
      <div class="relative mx-auto max-w-3xl">
        <div class="flex flex-wrap items-center justify-center gap-2">
          <span v-if="catalog" class="badge" :class="isStale ? 'badge-warning' : 'badge-success'">
            <span class="h-1.5 w-1.5 rounded-full bg-current"></span>
            {{ isStale ? t('pricing.status.stale') : t('pricing.status.live') }}
          </span>
          <span v-if="catalog" class="badge badge-gray">{{ t('pricing.modelCount', { count: catalog.models.length }) }}</span>
        </div>
        <h1 class="mt-3 text-3xl font-bold tracking-tight text-gray-900 dark:text-white sm:text-5xl">
          {{ t('pricing.title') }}
        </h1>
        <p class="mx-auto mt-3 max-w-2xl text-sm leading-6 text-gray-500 dark:text-dark-400 sm:text-base">
          {{ t('pricing.marketDescription') }}
        </p>
        <div class="relative mx-auto mt-6 max-w-2xl text-left">
          <Icon name="search" size="md" class="absolute left-4 top-1/2 -translate-y-1/2 text-gray-400" />
          <input
            v-model="search"
            class="input h-12 rounded-xl bg-white/90 pl-11 pr-10 shadow-sm dark:bg-dark-950/80"
            :placeholder="t('pricing.searchPlaceholder')"
          />
          <button
            v-if="search"
            type="button"
            class="absolute right-3 top-1/2 -translate-y-1/2 rounded-md p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-800 dark:hover:text-white"
            :aria-label="t('pricing.clearSearch')"
            @click="search = ''"
          >
            <Icon name="x" size="sm" />
          </button>
        </div>
      </div>
    </section>

    <div v-if="loading && !catalog" class="card p-12 text-center">
      <Icon name="refresh" size="xl" class="mx-auto animate-spin text-primary-500" />
      <p class="mt-3 text-sm text-gray-500 dark:text-dark-400">{{ t('pricing.loading') }}</p>
    </div>

    <div v-else-if="!catalog" class="card border-red-200 p-8 text-center dark:border-red-900/50">
      <Icon name="exclamationTriangle" size="xl" class="mx-auto text-red-500" />
      <h2 class="mt-3 text-lg font-semibold text-gray-900 dark:text-white">{{ t('pricing.unavailableTitle') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ errorMessage || t('pricing.unavailableDescription') }}</p>
      <button class="btn btn-primary mt-5" @click="loadCatalog(true)">{{ t('pricing.retry') }}</button>
    </div>

    <template v-else>
      <div v-if="isStale" class="flex items-start gap-3 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900/50 dark:bg-amber-900/20 dark:text-amber-200">
        <Icon name="exclamationTriangle" size="md" class="mt-0.5 flex-shrink-0" />
        <span>{{ t('pricing.lastGoodWarning', { time: formatDateTime(catalog.generated_at) }) }}</span>
      </div>

      <section class="card overflow-hidden">
        <div class="grid gap-px bg-gray-100 dark:bg-dark-700 sm:grid-cols-2 xl:grid-cols-4">
          <div class="bg-white p-4 dark:bg-dark-800/80 sm:p-5">
            <p class="text-xs font-medium uppercase tracking-wide text-gray-400">{{ t('pricing.billing.quotaTitle') }}</p>
            <p class="mt-2 font-mono text-xl font-bold text-primary-600 dark:text-primary-400">
              ¥1 = ${{ catalog.exchange.quota_usd_per_cny }} {{ t('pricing.billing.quotaUnit') }}
            </p>
            <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('pricing.billing.quotaHint') }}</p>
          </div>
          <div class="bg-white p-4 dark:bg-dark-800/80 sm:p-5">
            <p class="text-xs font-medium uppercase tracking-wide text-gray-400">{{ t('pricing.billing.fxTitle') }}</p>
            <template v-if="catalog.exchange.usd_cny_reference && catalog.exchange.source && catalog.exchange.effective_date">
              <p class="mt-2 font-mono text-xl font-bold text-gray-900 dark:text-white">
                1 USD = ¥{{ catalog.exchange.usd_cny_reference }}
              </p>
              <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">
                {{ catalog.exchange.source }} · {{ catalog.exchange.effective_date }}
              </p>
            </template>
            <template v-else>
              <p class="mt-2 text-sm font-medium text-amber-700 dark:text-amber-300">{{ t('pricing.billing.fxMissing') }}</p>
              <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('pricing.billing.fxMissingHint') }}</p>
            </template>
          </div>
          <div class="bg-white p-4 dark:bg-dark-800/80 sm:p-5">
            <p class="text-xs font-medium uppercase tracking-wide text-gray-400">{{ t('pricing.billing.overseasTitle') }}</p>
            <p class="mt-2 font-mono text-xs leading-6 text-gray-700 dark:text-dark-200">
              {{ t('pricing.billing.overseasFormula') }}
            </p>
          </div>
          <div class="bg-white p-4 dark:bg-dark-800/80 sm:p-5">
            <p class="text-xs font-medium uppercase tracking-wide text-gray-400">{{ t('pricing.billing.nativeTitle') }}</p>
            <p class="mt-2 font-mono text-xs leading-6 text-gray-700 dark:text-dark-200">
              {{ t('pricing.billing.nativeFormula') }}
            </p>
          </div>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-2 border-t border-gray-100 px-4 py-3 text-xs text-gray-500 dark:border-dark-700 dark:text-dark-400 sm:px-5">
          <span>{{ t('pricing.generatedAt') }}：{{ formatDateTime(catalog.generated_at) }}</span>
          <span class="font-mono">v{{ catalog.data_version }}</span>
        </div>
      </section>

      <div class="grid gap-5 xl:grid-cols-[280px_minmax(0,1fr)]">
        <aside class="hidden self-start rounded-2xl border border-gray-200 bg-white p-4 shadow-card dark:border-dark-700 dark:bg-dark-900 xl:sticky xl:top-4 xl:block">
          <div class="flex items-start justify-between gap-3">
            <div>
              <h2 class="text-sm font-bold text-gray-900 dark:text-white">{{ t('pricing.filters') }}</h2>
              <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('pricing.filtersHint') }}</p>
            </div>
            <button
              type="button"
              class="rounded-lg px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-800 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
              @click="resetFilters"
            >
              {{ t('pricing.reset') }}
            </button>
          </div>

          <section class="mt-4 border-t border-gray-100 pt-4 dark:border-dark-700">
            <h3 class="text-xs font-semibold uppercase tracking-wide text-gray-400">{{ t('pricing.groupFilter') }}</h3>
            <div class="mt-2 space-y-1.5">
              <button
                v-for="group in catalog.groups"
                :key="group.id"
                type="button"
                class="flex w-full items-center justify-between gap-3 rounded-xl border px-3 py-2.5 text-left transition-colors"
                :class="selectedGroupId === group.id
                  ? 'border-primary-200 bg-primary-50 text-primary-800 dark:border-primary-800 dark:bg-primary-900/20 dark:text-primary-200'
                  : 'border-transparent text-gray-600 hover:border-gray-200 hover:bg-gray-50 dark:text-dark-300 dark:hover:border-dark-700 dark:hover:bg-dark-800'"
                @click="selectedGroupId = group.id"
              >
                <span class="min-w-0">
                  <span class="block truncate text-xs font-medium">{{ group.name }}</span>
                  <span class="mt-0.5 block text-[10px] opacity-60">{{ t('pricing.modelCount', { count: groupModelCount(group.id) }) }}</span>
                </span>
                <span class="flex flex-shrink-0 items-center gap-1">
                  <span class="font-mono text-[11px]">×{{ group.effective_multiplier }}</span>
                  <span v-if="group.multiplier_source === 'user'" class="h-1.5 w-1.5 rounded-full bg-primary-500" :title="t('pricing.personal')"></span>
                </span>
              </button>
            </div>
          </section>

          <section class="mt-4 border-t border-gray-100 pt-4 dark:border-dark-700">
            <h3 class="text-xs font-semibold uppercase tracking-wide text-gray-400">{{ t('pricing.providerFilter') }}</h3>
            <div class="mt-2 flex flex-wrap gap-1.5">
              <button
                v-for="provider in providers"
                :key="provider.value"
                type="button"
                class="inline-flex items-center gap-1.5 rounded-lg border px-2 py-1.5 text-xs font-medium transition-colors"
                :class="selectedProvider === provider.value
                  ? 'border-primary-200 bg-primary-50 text-primary-700 dark:border-primary-800 dark:bg-primary-900/20 dark:text-primary-300'
                  : 'border-gray-200 text-gray-500 hover:bg-gray-50 hover:text-gray-800 dark:border-dark-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white'"
                @click="selectedProvider = provider.value"
              >
                {{ provider.label }}
                <span class="rounded bg-gray-100 px-1 text-[10px] dark:bg-dark-800">{{ providerCount(provider.value) }}</span>
              </button>
            </div>
          </section>
        </aside>

        <main class="min-w-0 space-y-4">
          <section class="sticky top-16 z-30 rounded-2xl border border-gray-200/80 bg-white/95 p-3 shadow-card backdrop-blur-xl dark:border-dark-700 dark:bg-dark-900/95 lg:top-0 sm:p-4">
            <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
              <div class="flex items-center justify-between gap-3">
                <p class="text-sm text-gray-500 dark:text-dark-400">
                  <span class="font-mono font-bold text-gray-900 dark:text-white">{{ rows.length }}</span>
                  {{ t('pricing.models') }}
                </p>
                <button class="btn btn-secondary btn-sm lg:hidden" :disabled="loading" @click="loadCatalog(true)">
                  <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
                </button>
              </div>

              <div class="flex flex-wrap items-center gap-2">
                <select v-model.number="selectedGroupId" class="input h-9 min-w-0 flex-1 text-xs xl:hidden sm:min-w-56">
                  <option v-for="group in catalog.groups" :key="group.id" :value="group.id">
                    {{ group.name }} · ×{{ group.effective_multiplier }}{{ group.multiplier_source === 'user' ? ` · ${t('pricing.personal')}` : '' }}
                  </option>
                </select>

                <div class="hidden rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800 lg:flex">
                  <button
                    type="button"
                    data-pricing-view="cards"
                    class="inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-all"
                    :class="viewMode === 'cards' ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-500 dark:text-dark-400'"
                    @click="viewMode = 'cards'"
                  >
                    <Icon name="grid" size="sm" />
                    {{ t('pricing.cardView') }}
                  </button>
                  <button
                    type="button"
                    data-pricing-view="table"
                    class="inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-all"
                    :class="viewMode === 'table' ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-500 dark:text-dark-400'"
                    @click="viewMode = 'table'"
                  >
                    <Icon name="document" size="sm" />
                    {{ t('pricing.tableView') }}
                  </button>
                </div>

                <button class="btn btn-secondary btn-sm hidden lg:inline-flex" :disabled="loading" @click="loadCatalog(true)">
                  <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
                  {{ t('common.refresh') }}
                </button>
              </div>
            </div>

            <div class="mt-3 flex gap-2 overflow-x-auto border-t border-gray-100 pt-3 xl:hidden dark:border-dark-700">
              <button
                v-for="provider in providers"
                :key="`mobile-${provider.value}`"
                type="button"
                class="flex-shrink-0 rounded-lg px-2.5 py-1.5 text-xs font-medium transition-colors"
                :class="selectedProvider === provider.value
                  ? 'bg-gray-900 text-white dark:bg-white dark:text-gray-900'
                  : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-800 dark:text-dark-300 dark:hover:bg-dark-700'"
                @click="selectedProvider = provider.value"
              >
                {{ provider.label }} · {{ providerCount(provider.value) }}
              </button>
            </div>
          </section>

          <div v-if="rows.length === 0" class="card p-10 text-center">
            <Icon name="inbox" size="xl" class="mx-auto text-gray-400" />
            <p class="mt-3 text-sm text-gray-500 dark:text-dark-400">{{ t('pricing.empty') }}</p>
            <button type="button" class="btn btn-secondary mt-4" @click="resetFilters">{{ t('pricing.reset') }}</button>
          </div>

          <div v-else-if="viewMode === 'cards'" class="grid grid-cols-1 gap-3 md:grid-cols-2 2xl:grid-cols-3">
            <PricingModelCard
              v-for="row in rows"
              :key="`${selectedGroupId}-${row.model.model_id}`"
              :row="row"
              :group="selectedGroup!"
              @open="openDetails(row)"
            />
          </div>

          <template v-else>
            <div class="hidden overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-card dark:border-dark-700 dark:bg-dark-900 lg:block">
              <div class="overflow-x-auto">
                <table class="w-full min-w-[1120px] border-collapse text-sm">
                  <thead class="bg-gray-50/90 text-xs font-medium uppercase tracking-wide text-gray-500 dark:bg-dark-800/80 dark:text-dark-300">
                    <tr class="border-b border-gray-200 dark:border-dark-700">
                      <th class="w-[220px] px-4 py-3 text-left">{{ t('pricing.columns.model') }}</th>
                      <th class="w-[180px] px-4 py-3 text-left">{{ t('pricing.columns.item') }}</th>
                      <th class="w-[170px] px-4 py-3 text-right">{{ t('pricing.columns.official') }}</th>
                      <th class="w-[170px] px-4 py-3 text-right">{{ t('pricing.columns.system') }}</th>
                      <th class="w-[120px] px-4 py-3 text-right">{{ t('pricing.columns.multiplier') }}</th>
                      <th class="w-[190px] px-4 py-3 text-right">{{ t('pricing.columns.actual') }}</th>
                      <th class="w-[150px] px-4 py-3 text-right">{{ t('pricing.columns.savings') }}</th>
                    </tr>
                  </thead>
                  <tbody
                    v-for="row in rows"
                    :key="`table-${selectedGroupId}-${row.model.model_id}`"
                    class="border-b-2 border-gray-200 last:border-b-0 dark:border-dark-600"
                  >
                    <tr
                      v-for="(item, itemIndex) in row.price.items"
                      :key="item.key"
                      class="border-t border-gray-100 first:border-t-0 hover:bg-gray-50/60 dark:border-dark-800 dark:hover:bg-dark-800/35"
                    >
                      <td v-if="itemIndex === 0" :rowspan="row.price.items.length" class="bg-gray-50/45 px-4 py-4 align-top dark:bg-dark-800/25">
                        <div class="flex items-start gap-3">
                          <span class="mt-0.5 flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-xl text-xs font-bold" :class="pricingProviderIconClass(row.model.provider)">
                            {{ pricingProviderInitial(row.model.provider) }}
                          </span>
                          <div class="min-w-0">
                            <button type="button" class="break-all text-left font-mono font-semibold text-gray-900 hover:text-primary-600 dark:text-white dark:hover:text-primary-400" @click="openDetails(row)">
                              {{ row.model.model_id }}
                            </button>
                            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ row.model.display_name }}</p>
                            <div class="mt-2 flex flex-wrap gap-1">
                              <span class="badge badge-gray px-2 py-0 text-[10px]">{{ billingModeLabel(row.price.billing_mode) }}</span>
                              <span v-if="row.price.price_source === 'channel'" class="badge badge-warning px-2 py-0 text-[10px]">{{ t('pricing.channelOverride') }}</span>
                            </div>
                            <a
                              v-if="safeOfficialSource(row.model.official_source)"
                              :href="safeOfficialSource(row.model.official_source)"
                              target="_blank"
                              rel="noopener noreferrer"
                              class="mt-2 inline-flex items-center gap-1 text-[11px] text-primary-600 hover:underline dark:text-primary-400"
                            >
                              {{ t('pricing.officialSource') }}
                              <Icon name="externalLink" size="xs" />
                            </a>
                          </div>
                        </div>
                      </td>
                      <td class="px-4 py-3 align-top">
                        <p class="font-medium text-gray-800 dark:text-dark-100">{{ itemLabel(item) }}</p>
                        <p class="mt-0.5 text-[11px] text-gray-400">{{ unitLabel(item.unit) }}</p>
                      </td>
                      <td class="px-4 py-3 text-right align-top">
                        <template v-if="item.official_price">
                          <p class="font-mono font-medium text-gray-800 dark:text-dark-100">{{ pricingCurrencySymbol(item.official_currency) }}{{ item.official_price }}</p>
                          <p v-if="item.official_cny_price && item.official_currency === 'USD'" class="mt-0.5 font-mono text-[11px] text-gray-400">≈ ¥{{ item.official_cny_price }}</p>
                        </template>
                        <span v-else class="text-xs text-gray-400">{{ t('pricing.noComparable') }}</span>
                      </td>
                      <td class="px-4 py-3 text-right align-top">
                        <p class="font-mono font-medium text-gray-700 dark:text-dark-200">{{ pricingCurrencySymbol(item.system_base_currency) }}{{ item.system_base_price }}</p>
                        <p class="mt-0.5 text-[11px] text-gray-400">{{ priceSourceLabel(row.price.price_source) }}</p>
                      </td>
                      <td class="px-4 py-3 text-right align-top">
                        <p class="font-mono font-semibold text-gray-800 dark:text-dark-100">×{{ item.effective_multiplier }}</p>
                        <p v-if="selectedGroup?.multiplier_source === 'user'" class="mt-0.5 text-[11px] text-primary-600 dark:text-primary-400">{{ t('pricing.personalRate') }}</p>
                        <p v-if="selectedGroup?.peak_rate_active && row.price.billing_mode === 'token'" class="mt-0.5 text-[11px] text-amber-600 dark:text-amber-400">{{ t('pricing.peakActive') }}</p>
                      </td>
                      <td class="px-4 py-3 text-right align-top">
                        <p class="font-mono text-base font-bold text-primary-600 dark:text-primary-400">¥{{ item.actual_cny_price }}</p>
                        <p class="mt-0.5 text-[11px] text-gray-400">{{ unitLabel(item.unit) }}</p>
                      </td>
                      <td class="px-4 py-3 text-right align-top">
                        <template v-if="item.savings_percent">
                          <p class="font-mono font-semibold" :class="Number(item.savings_percent) >= 0 ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'">
                            {{ Number(item.savings_percent) >= 0
                              ? t('pricing.savePercent', { value: formatPercent(Number(item.savings_percent)) })
                              : t('pricing.abovePercent', { value: formatPercent(Math.abs(Number(item.savings_percent))) }) }}
                          </p>
                          <p v-if="item.savings_cny" class="mt-0.5 font-mono text-[11px] text-gray-400">¥{{ item.savings_cny }}</p>
                        </template>
                        <span v-else-if="item.comparison_status === 'fx_unavailable'" class="text-xs text-amber-600 dark:text-amber-400">{{ t('pricing.fxRequired') }}</span>
                        <span v-else class="text-xs text-gray-400">—</span>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>

            <div class="grid grid-cols-1 gap-3 md:grid-cols-2 lg:hidden">
              <PricingModelCard
                v-for="row in rows"
                :key="`mobile-table-${selectedGroupId}-${row.model.model_id}`"
                :row="row"
                :group="selectedGroup!"
                @open="openDetails(row)"
              />
            </div>
          </template>
        </main>
      </div>
    </template>

    <PricingModelDetailsDrawer
      :open="Boolean(selectedRow)"
      :row="selectedRow"
      :group="selectedGroup"
      @close="selectedRow = null"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import pricingAPI, { type PricingCatalog, type PricingGroup, type PricingItem } from '@/api/pricing'
import Icon from '@/components/icons/Icon.vue'
import { useAuthStore } from '@/stores/auth'
import { extractApiErrorMessage } from '@/utils/apiError'
import { sanitizeUrl } from '@/utils/url'
import PricingModelCard from './PricingModelCard.vue'
import PricingModelDetailsDrawer from './PricingModelDetailsDrawer.vue'
import {
  pricingCurrencySymbol,
  pricingItemBaseKey,
  pricingItemQualifier,
  pricingProviderIconClass,
  pricingProviderInitial,
  type PricingRow,
} from './pricingPresentation'

const { t, locale } = useI18n()
const authStore = useAuthStore()
const catalog = ref<PricingCatalog | null>(null)
const loading = ref(false)
const errorMessage = ref('')
const localStale = ref(false)
const selectedGroupId = ref<number | null>(null)
const selectedProvider = ref('all')
const search = ref('')
const viewMode = ref<'cards' | 'table'>('cards')
const selectedRow = ref<PricingRow | null>(null)
let refreshTimer: ReturnType<typeof setInterval> | null = null
let controller: AbortController | null = null

const providers = computed(() => [
  { value: 'all', label: t('pricing.providers.all') },
  { value: 'openai', label: 'OpenAI' },
  { value: 'anthropic', label: 'Claude' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'grok', label: 'Grok' },
  { value: 'cn', label: t('pricing.providers.cn') },
])

const selectedGroup = computed<PricingGroup | null>(() =>
  catalog.value?.groups.find((group) => group.id === selectedGroupId.value) || null,
)

const rows = computed<PricingRow[]>(() => {
  if (!catalog.value || selectedGroupId.value === null) return []
  const query = search.value.trim().toLowerCase()
  return catalog.value.models
    .filter((model) => selectedProvider.value === 'all' || model.provider === selectedProvider.value)
    .filter((model) => !query || model.model_id.toLowerCase().includes(query) || model.display_name.toLowerCase().includes(query))
    .map((model) => ({ model, price: model.group_prices.find((price) => price.group_id === selectedGroupId.value) }))
    .filter((row): row is PricingRow => Boolean(row.price))
})

const isStale = computed(() => Boolean(catalog.value?.stale || localStale.value))

async function loadCatalog(manual = false) {
  if (loading.value && !manual) return
  controller?.abort()
  const requestController = new AbortController()
  controller = requestController
  loading.value = true
  try {
    const next = await pricingAPI.getCatalog(authStore.isAuthenticated, requestController.signal)
    catalog.value = next
    localStale.value = false
    errorMessage.value = ''
    if (!next.groups.some((group) => group.id === selectedGroupId.value)) {
      selectedGroupId.value = next.groups[0]?.id ?? null
    }
    if (selectedRow.value && selectedGroupId.value !== null) {
      const model = next.models.find((entry) => entry.model_id === selectedRow.value?.model.model_id)
      const price = model?.group_prices.find((entry) => entry.group_id === selectedGroupId.value)
      selectedRow.value = model && price ? { model, price } : null
    }
  } catch (error: unknown) {
    if ((error as { code?: string }).code === 'ERR_CANCELED') return
    errorMessage.value = extractApiErrorMessage(error, t('pricing.unavailableDescription'))
    if (catalog.value) localStale.value = true
  } finally {
    if (controller === requestController) {
      controller = null
      loading.value = false
    }
  }
}

function onVisibilityChange() {
  if (document.visibilityState === 'visible') loadCatalog()
}

function resetFilters() {
  search.value = ''
  selectedProvider.value = 'all'
  selectedGroupId.value = catalog.value?.groups[0]?.id ?? null
}

function groupModelCount(groupId: number) {
  return catalog.value?.models.filter((model) => model.group_prices.some((price) => price.group_id === groupId)).length ?? 0
}

function providerCount(provider: string) {
  if (!catalog.value || selectedGroupId.value === null) return 0
  return catalog.value.models.filter((model) =>
    (provider === 'all' || model.provider === provider) &&
    model.group_prices.some((price) => price.group_id === selectedGroupId.value),
  ).length
}

function openDetails(row: PricingRow) {
  selectedRow.value = row
}

function itemLabel(item: PricingItem) {
  const translated = t(`pricing.items.${pricingItemBaseKey(item)}`)
  const qualifier = pricingItemQualifier(item, locale.value)
  return qualifier ? `${translated} · ${qualifier}` : translated
}

function unitLabel(unit: string) {
  return unit === 'request' ? t('pricing.units.request') : t('pricing.units.millionTokens')
}

function billingModeLabel(mode: string) {
  return t(`pricing.billingModes.${mode}`)
}

function priceSourceLabel(source: string) {
  return t(`pricing.priceSources.${source}`)
}

function formatDateTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'medium' }).format(date)
}

function formatPercent(value: number) {
  return new Intl.NumberFormat(locale.value, { maximumFractionDigits: 2 }).format(value)
}

function safeOfficialSource(value?: string) {
  return value ? sanitizeUrl(value) : ''
}

watch(() => authStore.isAuthenticated, () => loadCatalog(true))
watch(selectedGroupId, () => {
  selectedRow.value = null
  if (providerCount(selectedProvider.value) === 0) selectedProvider.value = 'all'
})

onMounted(() => {
  loadCatalog()
  refreshTimer = setInterval(() => loadCatalog(), 60_000)
  document.addEventListener('visibilitychange', onVisibilityChange)
})

onBeforeUnmount(() => {
  controller?.abort()
  if (refreshTimer) clearInterval(refreshTimer)
  document.removeEventListener('visibilitychange', onVisibilityChange)
})
</script>
