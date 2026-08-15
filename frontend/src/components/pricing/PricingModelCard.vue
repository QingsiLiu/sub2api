<template>
  <article class="group flex h-full flex-col rounded-2xl border border-gray-200 bg-white p-4 transition-all duration-200 hover:-translate-y-0.5 hover:border-primary-200 hover:shadow-card dark:border-dark-700 dark:bg-dark-900 dark:hover:border-primary-800 sm:p-5">
    <header class="flex items-start justify-between gap-3">
      <div class="flex min-w-0 items-start gap-3">
        <span class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl text-xs font-bold" :class="pricingProviderIconClass(row.model.provider)">
          {{ pricingProviderInitial(row.model.provider) }}
        </span>
        <div class="min-w-0">
          <h2 class="truncate font-mono text-[15px] font-bold leading-tight text-gray-900 dark:text-white">
            {{ row.model.model_id }}
          </h2>
          <p class="mt-1 truncate text-xs text-gray-500 dark:text-dark-400">{{ row.model.display_name }}</p>
        </div>
      </div>
      <button
        type="button"
        data-pricing-details
        class="inline-flex flex-shrink-0 items-center gap-1 rounded-lg border border-gray-200 px-2.5 py-1.5 text-xs font-medium text-gray-600 transition-colors hover:border-primary-200 hover:bg-primary-50 hover:text-primary-700 dark:border-dark-700 dark:text-dark-300 dark:hover:border-primary-800 dark:hover:bg-primary-900/20 dark:hover:text-primary-300"
        @click="$emit('open')"
      >
        {{ t('pricing.details') }}
        <Icon name="chevronRight" size="xs" />
      </button>
    </header>

    <div class="mt-4 grid gap-2 sm:grid-cols-3">
      <div
        v-for="item in summaryItems"
        :key="item.key"
        class="rounded-xl bg-gray-50 px-3 py-2.5 dark:bg-dark-800/70"
      >
        <p class="truncate text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ itemLabel(item) }}</p>
        <p class="mt-1 font-mono text-base font-bold text-primary-600 dark:text-primary-400">¥{{ item.actual_cny_price }}</p>
        <p class="mt-0.5 text-[10px] text-gray-400">{{ unitLabel(item.unit) }}</p>
      </div>
    </div>

    <p class="mt-4 line-clamp-2 min-h-10 text-xs leading-5 text-gray-500 dark:text-dark-400">
      {{ t('pricing.cardDescription', { group: group.name }) }}
    </p>

    <footer class="mt-auto flex flex-wrap items-center gap-2 border-t border-gray-100 pt-4 dark:border-dark-700">
      <span class="badge badge-gray max-w-full truncate px-2 py-0.5 text-[10px]">{{ group.name }}</span>
      <span class="font-mono text-[11px] font-medium text-gray-600 dark:text-dark-300">×{{ firstMultiplier }}</span>
      <span v-if="group.multiplier_source === 'user'" class="badge badge-primary px-2 py-0.5 text-[10px]">{{ t('pricing.personal') }}</span>
      <span v-if="row.price.price_source === 'channel'" class="badge badge-warning px-2 py-0.5 text-[10px]">{{ t('pricing.channelOverride') }}</span>
      <span
        v-if="savings !== null"
        class="ml-auto rounded-full px-2 py-1 text-[10px] font-semibold"
        :class="savings >= 0
          ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300'
          : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300'"
      >
        {{ savings >= 0
          ? t('pricing.savePercent', { value: formatPercent(savings) })
          : t('pricing.abovePercent', { value: formatPercent(Math.abs(savings)) }) }}
      </span>
    </footer>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PricingGroup, PricingItem } from '@/api/pricing'
import Icon from '@/components/icons/Icon.vue'
import {
  bestSavingsPercent,
  pricingItemBaseKey,
  pricingItemQualifier,
  pricingProviderIconClass,
  pricingProviderInitial,
  representativePricingItems,
  type PricingRow,
} from './pricingPresentation'

const props = defineProps<{
  row: PricingRow
  group: PricingGroup
}>()

defineEmits<{ open: [] }>()

const { t, locale } = useI18n()
const summaryItems = computed(() => representativePricingItems(props.row.price.items))
const savings = computed(() => bestSavingsPercent(props.row.price.items))
const firstMultiplier = computed(() => props.row.price.items[0]?.effective_multiplier || props.group.effective_multiplier)

function itemLabel(item: PricingItem) {
  const translated = t(`pricing.items.${pricingItemBaseKey(item)}`)
  const qualifier = pricingItemQualifier(item, locale.value)
  return qualifier ? `${translated} · ${qualifier}` : translated
}

function unitLabel(unit: string) {
  return unit === 'request' ? t('pricing.units.request') : t('pricing.units.millionTokens')
}

function formatPercent(value: number) {
  return new Intl.NumberFormat(locale.value, { maximumFractionDigits: 2 }).format(value)
}
</script>
