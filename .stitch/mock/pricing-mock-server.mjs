import http from 'node:http'
import { apiResponse, buildPricingCatalog, mockUser, publicSettings } from './pricing-fixtures.mjs'

const port = Number(process.env.SUB2API_PRICING_MOCK_PORT || 4174)

const server = http.createServer((request, response) => {
  const url = new URL(request.url || '/', `http://${request.headers.host || '127.0.0.1'}`)
  const path = url.pathname

  let status = 200
  let data = []

  if (path === '/setup/status') data = { needs_setup: false, step: 'completed' }
  else if (path === '/api/v1/settings/public') data = publicSettings
  else if (path === '/api/v1/pricing/catalog') data = buildPricingCatalog(false)
  else if (path === '/api/v1/pricing/catalog/me') data = buildPricingCatalog(true)
  else if (path === '/api/v1/auth/me') data = mockUser
  else if (path === '/api/v1/groups/rates') data = { 27: 0.16, 46: 0.18 }
  else if (path === '/api/v1/subscriptions/active') data = []
  else if (path === '/api/v1/announcements') data = []
  else status = 404

  response.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Cache-Control': 'no-store',
  })
  response.end(status === 404
    ? JSON.stringify({ code: 404, message: `No mock route for ${path}`, data: null })
    : apiResponse(data))
})

server.listen(port, '127.0.0.1', () => {
  process.stdout.write(`Sub2API pricing mock API listening on http://127.0.0.1:${port}\n`)
})

function shutdown() {
  server.close(() => process.exit(0))
}

process.on('SIGINT', shutdown)
process.on('SIGTERM', shutdown)
