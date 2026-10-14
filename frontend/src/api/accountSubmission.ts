import { apiClient } from './client'
import type { adminAPI } from './admin'

export type SubmissionPlatform = 'anthropic' | 'openai'
export interface SubmissionConfig {
  name: string
  platform: SubmissionPlatform
  group_ids: number[]
  proxy_id: number | null
  concurrency: number
  priority: number
  rate_multiplier: number
  expires_in_hours?: number
}
export interface SubmissionInvite {
  id: number
  config: SubmissionConfig
  created_at: string
  expires_at: string
  status: 'pending' | 'submitted' | 'revoked' | 'expired'
  view_revoked_at?: string | null
  account_id: number | null
  account_count?: number
}
export interface PublicSubmissionInvite {
  priority?: number
  rate_multiplier?: number
	name?: string
	concurrency?: number
	has_preset_proxy?: boolean
  platform: SubmissionPlatform
  expires_at: string
  status: 'pending' | 'submitted'
  view_available?: boolean
}

export async function createSubmissionInvite(config: SubmissionConfig) {
  const { data } = await apiClient.post<{ invite: SubmissionInvite; token: string }>('/admin/accounts/submission-invites', config)
  return data
}
export async function listSubmissionInvites(page = 1) {
  const { data } = await apiClient.get<{ items: SubmissionInvite[]; total: number }>('/admin/accounts/submission-invites', {
    params: { page, page_size: 20 }
  })
  return data
}
export async function revokeSubmissionInvite(id: number) {
  await apiClient.post(`/admin/accounts/submission-invites/${id}/revoke`)
}

// Public credential requests bypass auth/refresh interceptors and never retain request configs
// in thrown errors. Only a numeric status escapes this function.
export class SubmissionRequestError extends Error {
  constructor(public readonly status: number) { super('Submission request failed') }
}
async function publicSubmissionRequest<T>(action: 'inspect' | 'submit' | 'submit/batch' | 'usage' | 'connection' | 'connection/update' | 'form', payload: { token: string } & Record<string, unknown>): Promise<T> {
  const controller = new AbortController()
  const timer = window.setTimeout(() => controller.abort(), action === 'form' ? 120000 : action === 'submit/batch' ? 45000 : 15000)
  try {
    const response = await fetch(`/api/v1/account-submissions/${action}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
      credentials: 'omit',
      cache: 'no-store',
      referrerPolicy: 'no-referrer',
      redirect: 'error',
      signal: controller.signal
    })
    if (!response.ok) throw new SubmissionRequestError(response.status)
    const result = await response.json()
    if (result.code !== 0 || !result.data) throw new SubmissionRequestError(0)
    return result.data as T
  } catch (error) {
    if (error instanceof SubmissionRequestError) throw error
    throw new SubmissionRequestError(0)
  } finally {
    window.clearTimeout(timer)
  }
}
export function inspectSubmissionInvite(token: string) {
  return publicSubmissionRequest<PublicSubmissionInvite>('inspect', { token })
}
export function submitAccountKey(token: string, api_key: string, options?: SubmissionOptions) {
  return publicSubmissionRequest<{ status: 'submitted' }>('submit', { token, api_key, ...options })
}

export function submitAccountKeyBatch(token: string, api_keys: string[], options: SubmissionOptions) {
  return publicSubmissionRequest<{ status: 'submitted' }>('submit/batch', { token, api_keys, ...options })
}

export interface SubmissionBatchItem { api_key: string; connection: SubmissionConnectionInput }
export function submitAccountKeyBatchItems(token: string, items: SubmissionBatchItem[], options: Pick<SubmissionOptions, 'name' | 'concurrency'>) {
  return publicSubmissionRequest<{ status: 'submitted' }>('submit/batch', { token, items, ...options })
}

export interface SubmissionProxyDraft { name: string; protocol: string; host: string; port: number; username: string; password?: string; clear_password?: boolean }
export interface SubmissionConnectionInput { mode: 'preset' | 'direct' | 'custom'; proxy?: SubmissionProxyDraft }
export interface SubmissionOptions { name: string; concurrency: number; connection: SubmissionConnectionInput }
export interface SubmissionConnection extends SubmissionConnectionInput { version: number; has_preset: boolean; has_password: boolean }
export function getSubmissionConnection(token: string, account_ref?: string) { return publicSubmissionRequest<SubmissionConnection>('connection', { token, ...(account_ref ? { account_ref } : {}) }) }
export function updateSubmissionConnection(token: string, version: number, request_id: string, connection: SubmissionConnectionInput, account_ref?: string) {
  return publicSubmissionRequest<SubmissionConnection>('connection/update', { token, version, request_id, connection, ...(account_ref ? { account_ref } : {}) })
}

export interface SubmissionUsageBucket {
  date?: string
  model?: string
  requests: number
  input_tokens: number
  output_tokens: number
  cache_creation_tokens: number
  cache_read_tokens: number
  reference_cost: number
}
export interface SubmissionAccountUsage {
  account_ref: string
  name: string
  platform: SubmissionPlatform
  enabled: boolean
  summary: SubmissionUsageBucket
}
export interface SubmissionUsage {
  accounts: SubmissionAccountUsage[]
  name: string
  platform: SubmissionPlatform
  enabled: boolean
  start_date: string
  end_date: string
  timezone: string
  currency: string
  updated_at: string
  summary: SubmissionUsageBucket
  daily: SubmissionUsageBucket[]
  models: SubmissionUsageBucket[]
}
export function getSubmissionUsage(token: string, start_date: string, end_date: string) {
  return publicSubmissionRequest<SubmissionUsage>('usage', { token, start_date, end_date })
}
export async function revokeSubmissionView(id: number) {
  await apiClient.post(`/admin/accounts/submission-invites/${id}/revoke-view`)
}
export async function resetSubmissionView(id: number) {
  const { data } = await apiClient.post<{ token: string }>(`/admin/accounts/submission-invites/${id}/reset-view`)
  return data
}

// Reuse the entire official modal/OAuth API surface without an administrator
// session or interceptors. Every request carries only this invitation capability.
export function createSubmissionFormAPI(token: string, connection: () => SubmissionConnectionInput, batchItems?: () => SubmissionBatchItem[]): typeof adminAPI {
  const request = async <T>(action: string, payload: Record<string, unknown> = {}): Promise<T> => {
    try { return await publicSubmissionRequest<T>('form', { token, action, payload, ...(action === 'create-batch' ? { items: batchItems!() } : { connection: connection() }) }) }
    catch (cause) {
      // An uncertain create only inspects the committed state; never retries a
      // credential-bearing operation or another invitation's request.
      if (['create','create-batch','codex-import','codex-pat'].includes(action)) {
        try {
          const state = await inspectSubmissionInvite(token)
          if (state.status === 'submitted' && state.view_available !== false) {
            return (action === 'codex-import' ? {created:1,updated:0,skipped:0,failed:0} : {id:0,status:'submitted'}) as T
          }
        } catch { /* Keep only the original sanitized status. */ }
      }
      throw cause
    }
  }
  const accounts = {
    create: (payload: Record<string, unknown>) => request(batchItems && payload.type === 'apikey' ? 'create-batch' : 'create', payload),
    checkMixedChannelRisk: (payload: Record<string, unknown>) => request('mixed-risk', payload),
    generateAuthUrl: (endpoint: string, payload: Record<string, unknown>) => request(endpoint, payload),
    exchangeCode: (endpoint: string, payload: Record<string, unknown>) => request(endpoint, payload),
    refreshOpenAIToken: (refresh_token: string, _proxyId?: number, endpoint = '/admin/openai/refresh-token', client_id?: string) => request(endpoint, {refresh_token, client_id}),
    importCodexSession: (payload: Record<string, unknown>) => request('codex-import', payload),
    createOpenAICodexPAT: (payload: Record<string, unknown>) => request('codex-pat', payload),
    syncUpstreamModelsPreview: (payload: Record<string, unknown>) => request('models-preview', payload),
  }
  return {
    accounts,
    settings: {
      getSettings: () => request('settings'),
      getWebSearchEmulationConfig: () => request('web-search'),
      getOpenAISyncModelIDs: () => request('openai-models'),
    },
    tlsFingerprintProfiles: { list: () => request('profiles') },
  } as unknown as typeof adminAPI
}
