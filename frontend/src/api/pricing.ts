import { apiClient } from './client'

export type PricingCurrencyMode = 'usd_quota' | 'native_cny'

export interface PricingExchange {
  quota_usd_per_cny: string
  usd_cny_reference: string | null
  source: string
  effective_date: string
}

export interface PricingGroup {
  id: number
  name: string
  platform: string
  default_multiplier: string
  effective_multiplier: string
  multiplier_source: 'default' | 'user' | string
  currency_mode: PricingCurrencyMode
  peak_rate_enabled: boolean
  peak_rate_active: boolean
  peak_start?: string
  peak_end?: string
  peak_rate_multiplier?: string
}

export interface PricingItem {
  key: string
  unit: '1M_tokens' | 'request' | string
  tier_label?: string
  min_context_tokens?: number
  max_context_tokens?: number | null
  official_price: string | null
  official_currency?: 'USD' | 'CNY' | string
  official_cny_price: string | null
  system_base_price: string
  system_base_currency: 'USD' | 'CNY' | string
  effective_multiplier: string
  multiplier_source: string
  actual_cny_price: string
  savings_cny: string | null
  savings_percent: string | null
  comparison_status: 'exact' | 'fx_unavailable' | 'unavailable' | string
}

export interface PricingGroupPrice {
  group_id: number
  billing_mode: 'token' | 'per_request' | 'image' | string
  price_source: 'channel' | 'litellm' | 'fallback' | string
  items: PricingItem[]
}

export interface PricingModel {
  model_id: string
  display_name: string
  provider: string
  billing_mode: string
  comparison_status: string
  official_reference_model?: string
  official_source?: string
  official_effective_from?: string
  official_effective_until?: string
  official_rate_period?: 'peak' | 'off_peak' | string
  group_prices: PricingGroupPrice[]
}

export interface PricingCatalog {
  generated_at: string
  data_version: string
  stale: boolean
  exchange: PricingExchange
  groups: PricingGroup[]
  models: PricingModel[]
}

export async function getPricingCatalog(signal?: AbortSignal): Promise<PricingCatalog> {
  const { data } = await apiClient.get<PricingCatalog>('/admin/pricing/catalog', { signal })
  return data
}

export const pricingAPI = { getCatalog: getPricingCatalog }

export default pricingAPI
