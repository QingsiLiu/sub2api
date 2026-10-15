/**
 * Supplier console API (geili)
 * Every endpoint is scoped server-side to accounts owned by the signed-in supplier.
 */

import { apiClient } from './client'
import { buildApiUrl } from './url'
import type { PaginatedResponse } from '@/types'

export type SupplierReviewState = 'pending' | 'running' | 'paused' | 'disabled' | 'error'

export interface SupplierAccount {
  id: number
  name: string
  notes: string | null
  platform: string
  type: string
  base_url: string
  api_key_hint: string
  concurrency: number
  rate_multiplier: number
  status: string
  schedulable: boolean
  review_state: SupplierReviewState
  error_message: string
  groups: string[]
  last_used_at: string | null
  created_at: string
  updated_at: string
}

export interface SupplierAccountPayload {
  platform?: string
  name?: string
  notes?: string | null
  api_key?: string
  base_url?: string
  concurrency?: number
}

export interface SupplierUsageLog {
  id: number
  account_id: number
  account_name: string
  model: string
  input_tokens: number
  output_tokens: number
  cache_creation_tokens: number
  cache_read_tokens: number
  image_count: number
  account_cost: number
  stream: boolean
  duration_ms: number | null
  first_token_ms: number | null
  created_at: string
}

export interface SupplierUsageStats {
  total_requests: number
  total_input_tokens: number
  total_output_tokens: number
  total_cache_creation_tokens: number
  total_cache_read_tokens: number
  total_tokens: number
  total_account_cost: number
  average_duration_ms: number
}

export interface SupplierUsageQuery {
  page?: number
  page_size?: number
  account_id?: number
  model?: string
  start_date?: string
  end_date?: string
  timezone?: string
}

export async function getMeta(): Promise<{ platforms: string[] }> {
  const { data } = await apiClient.get<{ platforms: string[] }>('/supplier/meta')
  return data
}

export async function listAccounts(
  page = 1,
  pageSize = 20,
  filters: { platform?: string; status?: string; search?: string } = {}
): Promise<PaginatedResponse<SupplierAccount>> {
  const { data } = await apiClient.get<PaginatedResponse<SupplierAccount>>('/supplier/accounts', {
    params: { page, page_size: pageSize, ...filters }
  })
  return data
}

export async function createAccount(payload: SupplierAccountPayload): Promise<SupplierAccount> {
  const { data } = await apiClient.post<SupplierAccount>('/supplier/accounts', payload)
  return data
}

export async function updateAccount(id: number, payload: SupplierAccountPayload): Promise<SupplierAccount> {
  const { data } = await apiClient.put<SupplierAccount>(`/supplier/accounts/${id}`, payload)
  return data
}

export async function pauseAccount(id: number): Promise<SupplierAccount> {
  const { data } = await apiClient.post<SupplierAccount>(`/supplier/accounts/${id}/pause`)
  return data
}

export async function deleteAccount(id: number): Promise<void> {
  await apiClient.delete(`/supplier/accounts/${id}`)
}

/** SSE test stream URL; callers POST with fetch because EventSource cannot. */
export function testAccountUrl(id: number): string {
  return buildApiUrl(`/supplier/accounts/${id}/test`)
}

export async function listUsage(query: SupplierUsageQuery): Promise<PaginatedResponse<SupplierUsageLog>> {
  const { data } = await apiClient.get<PaginatedResponse<SupplierUsageLog>>('/supplier/usage', { params: query })
  return data
}

export async function getUsageStats(query: SupplierUsageQuery): Promise<SupplierUsageStats> {
  const { data } = await apiClient.get<SupplierUsageStats>('/supplier/usage/stats', { params: query })
  return data
}

/** Administrator: assign (ownerUserId > 0) or clear (0) the supplier owner of an account. */
export async function adminSetSupplierOwner(
  accountId: number,
  ownerUserId: number
): Promise<{ supplier_owner: { id: number; email: string } | null }> {
  const { data } = await apiClient.put<{ supplier_owner: { id: number; email: string } | null }>(
    `/admin/accounts/${accountId}/supplier-owner`,
    { owner_user_id: ownerUserId }
  )
  return data
}

export const supplierAPI = {
  getMeta,
  listAccounts,
  createAccount,
  updateAccount,
  pauseAccount,
  deleteAccount,
  testAccountUrl,
  listUsage,
  getUsageStats,
  adminSetSupplierOwner
}

export default supplierAPI
