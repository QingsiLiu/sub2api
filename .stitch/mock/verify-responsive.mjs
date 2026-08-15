import puppeteer from 'puppeteer'
import { mockUser } from './pricing-fixtures.mjs'

const browser = await puppeteer.launch({ headless: true })
const results = []

for (const width of [390, 768, 1280, 1440]) {
  const page = await browser.newPage()
  await page.setViewport({ width, height: 900 })
  await page.goto('http://127.0.0.1:4173/login', { waitUntil: 'networkidle2' })
  await page.evaluate((user) => {
    localStorage.setItem('auth_token', 'stitch-mock-token')
    localStorage.setItem('auth_user', JSON.stringify(user))
    localStorage.setItem('token_expires_at', String(Date.now() + 86_400_000))
    localStorage.setItem('theme', 'light')
    document.documentElement.classList.remove('dark')
  }, mockUser)
  await page.goto('http://127.0.0.1:4173/admin/model-pricing', { waitUntil: 'networkidle2' })

  if (width >= 1280) {
    const tableButton = await page.$('[data-pricing-view="table"]')
    await tableButton?.click()
  }

  const metrics = await page.evaluate(() => {
    const internalScrollers = Array.from(document.querySelectorAll('.overflow-x-auto'))
      .map((element) => ({ client: element.clientWidth, scroll: element.scrollWidth }))
      .filter((entry) => entry.scroll > entry.client)
    return {
      clientWidth: document.documentElement.clientWidth,
      scrollWidth: document.documentElement.scrollWidth,
      bodyScrollWidth: document.body.scrollWidth,
      maxInternalOverflow: Math.max(...internalScrollers.map((entry) => entry.scroll - entry.client), 0),
    }
  })

  results.push({
    width,
    ...metrics,
    pageOverflow: metrics.scrollWidth > metrics.clientWidth,
  })

  if (width === 390) {
    await page.click('[data-pricing-details]')
    await new Promise((resolve) => setTimeout(resolve, 350))
    results.push({
      drawer: await page.evaluate(() => {
        const dialog = document.querySelector('[role="dialog"]')
        return {
          width: dialog?.getBoundingClientRect().width ?? 0,
          viewport: window.innerWidth,
          bodyOverflow: document.body.style.overflow,
          pageOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth,
        }
      }),
    })
  }

  await page.close()
}

await browser.close()
process.stdout.write(`${JSON.stringify(results, null, 2)}\n`)
