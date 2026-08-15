import type { Page } from 'puppeteer'

export default async function preparePublicPricingCapture(page: Page) {
  await page.evaluate(() => {
    localStorage.removeItem('auth_token')
    localStorage.removeItem('refresh_token')
    localStorage.removeItem('auth_user')
    localStorage.setItem('sub2api_locale', 'zh')
    localStorage.setItem('theme', 'light')
    document.documentElement.classList.remove('dark')
    document.documentElement.classList.add('light')
  })
}
