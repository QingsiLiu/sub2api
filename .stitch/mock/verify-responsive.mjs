import puppeteer from 'puppeteer'

const browser = await puppeteer.launch({ headless: true })
const results = []

for (const width of [390, 768, 1280, 1440]) {
  const page = await browser.newPage()
  await page.setViewport({ width, height: 900 })
  await page.goto('http://127.0.0.1:4173/pricing', { waitUntil: 'networkidle2' })
  await page.evaluate(() => {
    localStorage.removeItem('auth_token')
    localStorage.setItem('theme', 'light')
    document.documentElement.classList.remove('dark')
  })
  await page.reload({ waitUntil: 'networkidle2' })

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
