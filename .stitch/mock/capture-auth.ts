import type { Page } from 'puppeteer'
import { apiResponse, buildPricingCatalog, mockUser, publicSettings } from './pricing-fixtures.mjs'

export default async function preparePricingCapture(page: Page) {
  await page.setRequestInterception(true)

  page.on('request', async (request) => {
    const url = new URL(request.url())
    const path = url.pathname

    if (path === '/setup/status') {
      await request.respond({
        status: 200,
        contentType: 'application/json',
        body: apiResponse({ needs_setup: false, step: 'completed' }),
      })
      return
    }

    if (!path.startsWith('/api/v1/')) {
      await request.continue()
      return
    }

    let data: unknown = []
    if (path === '/api/v1/settings/public') data = publicSettings
    if (path === '/api/v1/auth/me') data = mockUser
    if (path === '/api/v1/admin/pricing/catalog') data = buildPricingCatalog(false)
    if (path === '/api/v1/groups/rates') data = { 27: 0.16, 46: 0.18 }
    if (path === '/api/v1/subscriptions/active') data = []
    if (path === '/api/v1/announcements') data = []

    await request.respond({
      status: 200,
      contentType: 'application/json',
      body: apiResponse(data),
    })
  })

  await page.evaluate((user) => {
    localStorage.setItem('auth_token', 'stitch-mock-token')
    localStorage.setItem('auth_user', JSON.stringify(user))
    localStorage.setItem('token_expires_at', String(Date.now() + 86_400_000))
    localStorage.setItem('sub2api_locale', 'zh')
    localStorage.setItem('theme', 'light')
  }, mockUser)
}
