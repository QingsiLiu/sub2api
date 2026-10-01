// Idempotency keys for administrator grant writes. Same contract as
// bulkSubscriptionOperation.ts: a key stays bound to its exact payload in
// sessionStorage until the outcome is known, so a retry after a timeout or a
// page reload can never issue a second grant.

export interface AdminGrantOperation<T> {
  payload: T
  key: string
  storageKey: string | null
  outcomeUncertain: boolean
}

const pendingKeys = new Map<string, string>()

function currentAdminId(): number | null {
  try {
    const user = JSON.parse(globalThis.localStorage?.getItem('auth_user') ?? 'null') as { id?: unknown } | null
    const id = user?.id
    return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null
  } catch {
    return null
  }
}

function readStoredKey(storageKey: string): string | null {
  try {
    return globalThis.sessionStorage?.getItem(storageKey) ?? null
  } catch {
    return null
  }
}

function storeKey(storageKey: string, key: string | null) {
  try {
    if (key) globalThis.sessionStorage?.setItem(storageKey, key)
    else globalThis.sessionStorage?.removeItem(storageKey)
  } catch {
    // Keep same-session retries safe in memory when browser storage is unavailable.
  }
}

export function prepareAdminGrantOperation<T>(scope: 'grant' | 'terminate', payload: T): AdminGrantOperation<T> {
  const adminId = currentAdminId()
  const storageKey = adminId ? `sub2api:admin:subscription-${scope}:${adminId}:${JSON.stringify(payload)}` : null
  let key = storageKey ? pendingKeys.get(storageKey) ?? readStoredKey(storageKey) : null
  const outcomeUncertain = !!key
  if (!key) {
    const requestId = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
    key = `subscription-${scope}-${adminId ?? 'unknown'}-${requestId}`
  }
  if (storageKey) {
    pendingKeys.set(storageKey, key)
    storeKey(storageKey, key)
  }
  return { payload, key, storageKey, outcomeUncertain }
}

export function completeAdminGrantOperation(operation: AdminGrantOperation<unknown>) {
  if (!operation.storageKey) return
  pendingKeys.delete(operation.storageKey)
  storeKey(operation.storageKey, null)
}

export interface ApiFailure {
  status?: number
  reason?: string
  code?: unknown
  message?: string
}

// Business rejections roll the whole transaction back, so the key can be
// dropped. Timeouts, 5xx and "request in progress" conflicts may land after
// the write and must be retried with the same key.
export function isDefiniteFailure(error: ApiFailure): boolean {
  const reason = typeof error?.reason === 'string' ? error.reason : ''
  if (reason.startsWith('ADMIN_GRANT_') || reason.startsWith('SUBSCRIPTION_')) return true
  const status = error?.status
  return !!status && status >= 400 && status < 500 && status !== 408 && status !== 409 && status !== 429
}
