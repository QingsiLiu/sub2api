import { apiClient } from '../client'
import type { PaginatedResponse } from '@/types'

export type ResponseAuditStatus = 'success' | 'partial_failure' | 'empty' | 'failed' | 'unknown'
export interface ResponseAudit {
  id: number
  audit_request_id: string
  turn: number
  request_id: string
  client_request_id: string
  usage_request_id?: string
  upstream_request_id?: string
  user_id: number
  api_key_id: number
  account_id: number
  group_id: number
  model: string
  endpoint: string
  protocol: string
  http_status: number
  status: ResponseAuditStatus
  reason: string
  text_written: boolean
  reasoning_written: boolean
  complete_tool_written: boolean
  upstream_content_seen: boolean
  upstream_terminal?: string
  terminal?: string
  terminal_written: boolean
  write_failed: boolean
  client_disconnected: boolean
  usage_present: boolean
  first_output_ms?: number
  started_at: string
  finished_at: string
  settlement_state: 'settled' | 'pending' | 'not_found' | 'unlinked'
  receipt_id?: number | null
  charged_amount?: string | null
}
export interface ResponseAuditQuery {
  from?: string
  to?: string
  status?: ResponseAuditStatus
  model?: string
  endpoint?: string
  request_id?: string
  user_id?: number
  api_key_id?: number
  account_id?: number
  page?: number
  page_size?: number
}
export interface ResponseAuditStats {
  total: number
  counts: Record<ResponseAuditStatus, number>
  missing_terminal: number
  write_failed: number
  empty_charged_receipts: number
  observation_started_at: string | null
  process_started_at: string
  write_failures_since_start: number
  dropped_since_start: number
}
export const responseAuditAPI = {
  async list(params: ResponseAuditQuery) {
    return (await apiClient.get<PaginatedResponse<ResponseAudit>>('/admin/usage/response-audits', { params })).data
  },
  async stats(params: ResponseAuditQuery) {
    return (await apiClient.get<ResponseAuditStats>('/admin/usage/response-audits/stats', { params })).data
  },
  async get(id: number) {
    return (await apiClient.get<ResponseAudit>(`/admin/usage/response-audits/${id}`)).data
  },
}
