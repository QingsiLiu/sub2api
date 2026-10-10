import { apiClient } from './client'

export type SubmissionPlatform = 'anthropic' | 'openai'
export interface SubmissionConfig {
  name: string
  platform: SubmissionPlatform
  group_ids: number[]
  proxy_id: number | null
  concurrency: number
  priority: number
  rate_multiplier: number
}
export interface SubmissionInvite {
  id: number
  config: SubmissionConfig
  created_at: string
  expires_at: string
  status: 'pending' | 'submitted' | 'revoked' | 'expired'
  account_id: number | null
}
export interface PublicSubmissionInvite {
  platform: SubmissionPlatform
  expires_at: string
  status: 'pending' | 'submitted'
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
async function publicSubmissionRequest<T>(action: 'inspect' | 'submit', payload: { token: string; api_key?: string }): Promise<T> {
  const controller = new AbortController()
  const timer = window.setTimeout(() => controller.abort(), 15000)
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
export function submitAccountKey(token: string, api_key: string) {
  return publicSubmissionRequest<{ status: 'submitted' }>('submit', { token, api_key })
}
