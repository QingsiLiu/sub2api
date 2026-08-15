<template>
  <Teleport to="body">
    <Transition
      enter-active-class="transition-opacity duration-200"
      enter-from-class="opacity-0"
      leave-active-class="transition-opacity duration-200"
      leave-to-class="opacity-0"
    >
      <div v-if="open && row && group" class="fixed inset-0 z-[70] bg-black/45 backdrop-blur-sm" @click="$emit('close')"></div>
    </Transition>

    <Transition
      enter-active-class="transition-transform duration-300 ease-out"
      enter-from-class="translate-x-full"
      leave-active-class="transition-transform duration-200 ease-in"
      leave-to-class="translate-x-full"
    >
      <aside
        v-if="open && row && group"
        ref="panel"
        role="dialog"
        aria-modal="true"
        :aria-label="t('pricing.detailsTitle', { model: row.model.display_name })"
        class="fixed inset-y-0 right-0 z-[80] flex w-full max-w-3xl flex-col border-l border-gray-200 bg-white shadow-2xl dark:border-dark-700 dark:bg-dark-950"
      >
        <header class="flex items-start justify-between gap-4 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:px-6">
          <div class="flex min-w-0 items-start gap-3">
            <span class="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl text-xs font-bold" :class="pricingProviderIconClass(row.model.provider)">
              {{ pricingProviderInitial(row.model.provider) }}
            </span>
            <div class="min-w-0">
              <h2 class="break-all font-mono text-lg font-bold text-gray-900 dark:text-white">{{ row.model.model_id }}</h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ row.model.display_name }}</p>
            </div>
          </div>
          <button
            ref="closeButton"
            type="button"
            class="rounded-lg p-2 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-800 dark:hover:text-white"
            :aria-label="t('pricing.close')"
            @click="$emit('close')"
          >
            <Icon name="x" size="md" />
          </button>
        </header>

        <div class="flex-1 overflow-y-auto px-4 py-5 sm:px-6">
          <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
              <p class="text-[10px] font-medium uppercase tracking-wide text-gray-400">{{ t('pricing.groupFilter') }}</p>
              <p class="mt-1.5 truncate text-sm font-semibold text-gray-900 dark:text-white">{{ group.name }}</p>
            </div>
            <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
              <p class="text-[10px] font-medium uppercase tracking-wide text-gray-400">{{ t('pricing.columns.multiplier') }}</p>
              <p class="mt-1.5 font-mono text-sm font-semibold text-gray-900 dark:text-white">×{{ row.price.items[0]?.effective_multiplier || group.effective_multiplier }}</p>
              <p class="mt-1 text-[10px] text-gray-400">
                {{ multiplierSourceLabel(row.price.items[0]?.multiplier_source || group.multiplier_source) }}
                <template v-if="group.multiplier_source === 'user'"> · {{ t('pricing.defaultMultiplier', { value: group.default_multiplier }) }}</template>
              </p>
              <p v-if="group.peak_rate_enabled && row.price.billing_mode === 'token'" class="mt-1 text-[10px] text-amber-600 dark:text-amber-400">
                {{ group.peak_rate_active ? t('pricing.peakActive') : t('pricing.peakScheduled') }}
                · {{ group.peak_start }}–{{ group.peak_end }} · ×{{ group.peak_rate_multiplier }}
              </p>
            </div>
            <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
              <p class="text-[10px] font-medium uppercase tracking-wide text-gray-400">{{ t('pricing.billingMode') }}</p>
              <p class="mt-1.5 text-sm font-semibold text-gray-900 dark:text-white">{{ billingModeLabel(row.price.billing_mode) }}</p>
            </div>
            <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
              <p class="text-[10px] font-medium uppercase tracking-wide text-gray-400">{{ t('pricing.systemSource') }}</p>
              <p class="mt-1.5 text-sm font-semibold text-gray-900 dark:text-white">{{ priceSourceLabel(row.price.price_source) }}</p>
            </div>
          </div>

          <section class="mt-6 rounded-2xl border border-gray-200 bg-gray-50/60 p-4 dark:border-dark-700 dark:bg-dark-900/50">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div>
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('pricing.officialReference') }}</h3>
                <p class="mt-1 font-mono text-xs text-gray-500 dark:text-dark-400">
                  {{ row.model.official_reference_model || t('pricing.noComparable') }}
                </p>
              </div>
              <a
                v-if="safeOfficialSource"
                :href="safeOfficialSource"
                target="_blank"
                rel="noopener noreferrer"
                class="inline-flex items-center gap-1 text-xs font-medium text-primary-600 hover:underline dark:text-primary-400"
              >
                {{ t('pricing.officialSource') }}
                <Icon name="externalLink" size="xs" />
              </a>
            </div>
            <p v-if="effectivePeriod" class="mt-3 text-xs text-gray-500 dark:text-dark-400">
              {{ t('pricing.effectivePeriod') }}：{{ effectivePeriod }}
            </p>
            <p v-if="row.model.official_rate_period" class="mt-2 text-xs font-medium text-amber-600 dark:text-amber-400">
              {{ t(`pricing.officialRatePeriods.${row.model.official_rate_period}`) }}
            </p>
          </section>

          <section class="mt-6">
            <div class="mb-3 flex items-center justify-between gap-3">
              <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('pricing.pricingBreakdown') }}</h3>
              <span class="text-xs text-gray-400">{{ row.price.items.length }} {{ t('pricing.billingItems') }}</span>
            </div>

            <div class="space-y-3">
              <article
                v-for="item in row.price.items"
                :key="item.key"
                class="overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900"
              >
                <div class="flex items-center justify-between gap-3 border-b border-gray-100 px-4 py-3 dark:border-dark-700">
                  <div>
                    <p class="text-sm font-semibold text-gray-900 dark:text-white">{{ itemLabel(item) }}</p>
                    <p class="mt-0.5 text-[11px] text-gray-400">{{ unitLabel(item.unit) }}</p>
                  </div>
                  <div class="text-right">
                    <p class="font-mono text-lg font-bold text-primary-600 dark:text-primary-400">¥{{ item.actual_cny_price }}</p>
                    <p class="text-[10px] text-gray-400">{{ t('pricing.columns.actual') }}</p>
                  </div>
                </div>
                <dl class="grid grid-cols-2 gap-px bg-gray-100 text-xs dark:bg-dark-700 sm:grid-cols-4">
                  <div class="bg-white p-3 dark:bg-dark-900">
                    <dt class="text-gray-400">{{ t('pricing.columns.official') }}</dt>
                    <dd v-if="item.official_price" class="mt-1 font-mono font-medium text-gray-800 dark:text-dark-100">
                      {{ pricingCurrencySymbol(item.official_currency) }}{{ item.official_price }}
                      <span v-if="item.official_cny_price && item.official_currency === 'USD'" class="mt-0.5 block text-[10px] text-gray-400">≈ ¥{{ item.official_cny_price }}</span>
                    </dd>
                    <dd v-else class="mt-1 text-gray-400">{{ t('pricing.noComparable') }}</dd>
                  </div>
                  <div class="bg-white p-3 dark:bg-dark-900">
                    <dt class="text-gray-400">{{ t('pricing.columns.system') }}</dt>
                    <dd class="mt-1 font-mono font-medium text-gray-800 dark:text-dark-100">
                      {{ pricingCurrencySymbol(item.system_base_currency) }}{{ item.system_base_price }}
                    </dd>
                  </div>
                  <div class="bg-white p-3 dark:bg-dark-900">
                    <dt class="text-gray-400">{{ t('pricing.columns.multiplier') }}</dt>
                    <dd class="mt-1 font-mono font-medium text-gray-800 dark:text-dark-100">×{{ item.effective_multiplier }}</dd>
                  </div>
                  <div class="bg-white p-3 dark:bg-dark-900">
                    <dt class="text-gray-400">{{ t('pricing.columns.savings') }}</dt>
                    <dd v-if="item.savings_percent" class="mt-1 font-mono font-semibold" :class="Number(item.savings_percent) >= 0 ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'">
                      <span class="block">
                        {{ Number(item.savings_percent) >= 0
                          ? t('pricing.savePercent', { value: formatPercent(Number(item.savings_percent)) })
                          : t('pricing.abovePercent', { value: formatPercent(Math.abs(Number(item.savings_percent))) }) }}
                      </span>
                      <span v-if="item.savings_cny" class="mt-0.5 block text-[10px] font-normal text-gray-400">¥{{ item.savings_cny }}</span>
                    </dd>
                    <dd v-else-if="item.comparison_status === 'fx_unavailable'" class="mt-1 text-amber-600 dark:text-amber-400">{{ t('pricing.fxRequired') }}</dd>
                    <dd v-else class="mt-1 text-gray-400">—</dd>
                  </div>
                </dl>
              </article>
            </div>
          </section>
        </div>
      </aside>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PricingGroup, PricingItem } from '@/api/pricing'
import Icon from '@/components/icons/Icon.vue'
import { sanitizeUrl } from '@/utils/url'
import {
  pricingCurrencySymbol,
  pricingItemBaseKey,
  pricingItemQualifier,
  pricingProviderIconClass,
  pricingProviderInitial,
  type PricingRow,
} from './pricingPresentation'

const props = defineProps<{
  open: boolean
  row: PricingRow | null
  group: PricingGroup | null
}>()

const emit = defineEmits<{ close: [] }>()
const { t, locale } = useI18n()
const closeButton = ref<HTMLButtonElement | null>(null)
const previousBodyOverflow = ref('')

const safeOfficialSource = computed(() => {
  const value = props.row?.model.official_source
  return value ? sanitizeUrl(value) : ''
})

const effectivePeriod = computed(() => {
  const from = props.row?.model.official_effective_from
  const until = props.row?.model.official_effective_until
  if (!from) return ''
  return until ? `${from} – < ${until}` : `${from} – ${t('pricing.current')}`
})

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') emit('close')
}

watch(() => props.open, async (open) => {
  if (open) {
    previousBodyOverflow.value = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    window.addEventListener('keydown', onKeydown)
    await nextTick()
    closeButton.value?.focus()
  } else {
    document.body.style.overflow = previousBodyOverflow.value
    window.removeEventListener('keydown', onKeydown)
  }
}, { immediate: true })

onBeforeUnmount(() => {
  document.body.style.overflow = previousBodyOverflow.value
  window.removeEventListener('keydown', onKeydown)
})

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

function multiplierSourceLabel(source: string) {
  return source === 'user' ? t('pricing.personalRate') : t('pricing.defaultRate')
}

function formatPercent(value: number) {
  return new Intl.NumberFormat(locale.value, { maximumFractionDigits: 2 }).format(value)
}
</script>
