import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))

vi.mock('@/api/client', () => ({
  apiClient: { get },
}))

import { getPricingCatalog, type PricingCatalog } from '@/api/pricing'

const catalog: PricingCatalog = {
  generated_at: '2026-08-14T00:00:00Z',
  data_version: 'abc123',
  stale: false,
  exchange: {
    quota_usd_per_cny: '1',
    usd_cny_reference: '6.8',
    source: 'Operator fixed reference',
    effective_date: '2026-08-14',
  },
  groups: [],
  models: [],
}

describe('pricing API', () => {
  beforeEach(() => {
    get.mockReset()
    get.mockResolvedValue({ data: catalog })
  })

  it('loads the administrator-only pricing catalog', async () => {
    const controller = new AbortController()

    await expect(getPricingCatalog(controller.signal)).resolves.toEqual(catalog)
    expect(get).toHaveBeenCalledWith('/admin/pricing/catalog', { signal: controller.signal })
  })
})
