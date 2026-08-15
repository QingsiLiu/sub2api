import { beforeAll, describe, expect, it, vi } from 'vitest'
import type { RouteRecordRaw } from 'vue-router'

const routerHarness = vi.hoisted(() => ({
  routes: [] as RouteRecordRaw[],
}))

vi.mock('vue-router', () => ({
  createWebHistory: vi.fn(() => ({})),
  createRouter: vi.fn((options: { routes: RouteRecordRaw[] }) => {
    routerHarness.routes = options.routes
    return {
      beforeEach: vi.fn(),
      afterEach: vi.fn(),
      onError: vi.fn(),
    }
  }),
}))

vi.mock('@/composables/useNavigationLoading', () => ({
  useNavigationLoadingState: () => ({
    startNavigation: vi.fn(),
    endNavigation: vi.fn(),
    isLoading: { value: false },
  }),
}))

vi.mock('@/composables/useRoutePrefetch', () => ({
  useRoutePrefetch: () => ({
    triggerPrefetch: vi.fn(),
    cancelPendingPrefetch: vi.fn(),
    resetPrefetchState: vi.fn(),
  }),
}))

describe('administrator pricing preview route', () => {
  beforeAll(async () => {
    await import('@/router')
  })

  it('registers only the administrator route', () => {
    const preview = routerHarness.routes.find((route) => route.path === '/admin/model-pricing')

    expect(preview?.meta).toMatchObject({ requiresAuth: true, requiresAdmin: true })
    expect(routerHarness.routes.some((route) => route.path === '/pricing')).toBe(false)
  })
})
