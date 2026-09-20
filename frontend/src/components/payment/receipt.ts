import type { PaymentOrder } from '@/types/payment'
import { formatPaymentAmount, normalizePaymentCurrency } from '@/components/payment/currency'

const RECEIPT_STATUSES = new Set([
  'COMPLETED',
  'REFUND_REQUESTED',
  'REFUNDING',
  'REFUND_PENDING',
  'PARTIALLY_REFUNDED',
  'REFUNDED',
])

const REFUND_STATUSES = new Set([
  'REFUND_REQUESTED',
  'REFUNDING',
  'REFUND_PENDING',
  'PARTIALLY_REFUNDED',
  'REFUNDED',
])

const CN_DIGITS = ['零', '壹', '贰', '叁', '肆', '伍', '陆', '柒', '捌', '玖']
const CN_UNITS = ['', '拾', '佰', '仟']
const CN_SECTIONS = ['', '万', '亿']

export interface ReceiptPayer {
  email?: string | null
  username?: string | null
}

export interface ReceiptModel {
  receiptNo: string
  issuedAt: string
  paidAt: string
  refunded: boolean
  statusLabel: string
  settledLabel: string
  amountText: string
  amountWords: string
  currency: string
  payerEmail: string
  payerName: string
  paymentMethod: string
  tradeNo: string
  merchant: string
  siteUrl: string
  contactInfo: string
  itemName: string
  itemDesc: string
  quantity: string
  unitPrice: string
  lineAmount: string
  filename: string
}

export interface ReceiptCopy {
  title: string
  paid: string
  refunded: string
  receiptNo: string
  issuedAt: string
  received: string
  amountWords: string
  payer: string
  payerName: string
  sectionTrade: string
  paidAt: string
  method: string
  tradeNo: string
  settled: string
  settledYes: string
  settledRefunded: string
  merchant: string
  currency: string
  sectionItems: string
  itemName: string
  itemDesc: string
  qty: string
  unitPrice: string
  lineAmount: string
  total: string
  notesTitle: string
  noteProof: string
  noteRefund: string
  issuerTitle: string
  itemBalance: string
  itemBalanceDesc: string
  itemSubscription: string
  itemSubscriptionDesc: string
}

export function canViewReceipt(order: Pick<PaymentOrder, 'status'> | null | undefined): boolean {
  return !!order && RECEIPT_STATUSES.has(order.status)
}

export function isReceiptRefunded(order: Pick<PaymentOrder, 'status'>): boolean {
  return REFUND_STATUSES.has(order.status)
}

export function formatReceiptDate(value?: string | null, locale?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  const formatter = new Intl.DateTimeFormat(locale || undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
  return formatter.format(date)
}

export function formatReceiptDay(value?: string | null, locale?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat(locale || undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(date)
}

function sectionToChinese(section: number): string {
  if (section === 0) return ''
  const digits = String(section).padStart(4, '0')
  let out = ''
  let zeroPending = false
  for (let i = 0; i < 4; i++) {
    const n = Number(digits[i])
    const pos = 3 - i
    if (n === 0) {
      zeroPending = out.length > 0
      continue
    }
    if (zeroPending) {
      out += '零'
      zeroPending = false
    }
    out += CN_DIGITS[n] + CN_UNITS[pos]
  }
  return out
}

export function amountInChineseYuan(amount: number): string {
  if (!Number.isFinite(amount)) return '人民币零元整'
  const sign = amount < 0 ? '负' : ''
  const cents = Math.round(Math.abs(amount) * 100)
  if (cents === 0) return `${sign}人民币零元整`
  const yuan = Math.floor(cents / 100)
  const jiao = Math.floor((cents % 100) / 10)
  const fen = cents % 10

  let integer = ''
  if (yuan === 0) {
    integer = '零'
  } else {
    const parts: string[] = []
    let remaining = yuan
    let sectionIndex = 0
    while (remaining > 0 && sectionIndex < CN_SECTIONS.length) {
      const section = remaining % 10000
      const body = sectionToChinese(section)
      if (body) {
        parts.unshift(body + CN_SECTIONS[sectionIndex])
      } else if (parts.length > 0 && !parts[0].startsWith('零')) {
        parts.unshift('零')
      }
      remaining = Math.floor(remaining / 10000)
      sectionIndex++
    }
    integer = parts.join('').replace(/零+/g, '零').replace(/零+$/g, '')
  }

  let out = `${sign}人民币${integer}元`
  if (jiao === 0 && fen === 0) return `${out}整`
  if (jiao > 0) out += `${CN_DIGITS[jiao]}角`
  else if (fen > 0 && yuan > 0) out += '零'
  if (fen > 0) out += `${CN_DIGITS[fen]}分`
  return out
}

export function amountInWords(amount: number, currency?: string | null): string {
  const code = normalizePaymentCurrency(currency)
  if (code === 'CNY' || code === 'RMB') return amountInChineseYuan(amount)
  return `${code} ${Math.abs(Number.isFinite(amount) ? amount : 0).toFixed(2)}`
}

const SOFTWARE_SITE_NAME = /^sub2api$/i

export function resolveReceiptSiteUrl(siteUrl?: string | null): string {
  return (siteUrl ?? '').trim().replace(/\/+$/, '')
}

export function resolveReceiptMerchant(siteName?: string | null, siteUrl?: string | null): string {
  const name = (siteName ?? '').trim()
  if (name && !SOFTWARE_SITE_NAME.test(name)) return name
  const url = resolveReceiptSiteUrl(siteUrl)
  if (!url) return name
  try {
    const parsed = new URL(/^[a-z][a-z0-9+.-]*:\/\//i.test(url) ? url : `https://${url}`)
    return parsed.hostname.replace(/^www\./i, '')
  } catch {
    return url
  }
}

export function buildReceiptNo(order: Pick<PaymentOrder, 'id' | 'created_at' | 'paid_at' | 'completed_at'>): string {
  const stamp = order.paid_at || order.completed_at || order.created_at
  const date = stamp && !Number.isNaN(new Date(stamp).getTime()) ? new Date(stamp) : new Date()
  const y = date.getFullYear()
  const m = String(date.getMonth() + 1).padStart(2, '0')
  const d = String(date.getDate()).padStart(2, '0')
  return `RCP-${y}${m}${d}-${String(order.id).padStart(6, '0')}`
}

export function buildReceiptModel(input: {
  order: PaymentOrder
  payer?: ReceiptPayer | null
  siteName: string
  siteUrl: string
  contactInfo?: string | null
  locale?: string
  copy: ReceiptCopy
  paymentMethodLabel: string
}): ReceiptModel {
  const { order, copy } = input
  const currency = normalizePaymentCurrency(order.currency)
  const amount = Number.isFinite(order.pay_amount) ? order.pay_amount : 0
  const amountText = formatPaymentAmount(amount, currency, input.locale)
  const refunded = isReceiptRefunded(order)
  const paidAt = formatReceiptDate(order.paid_at || order.completed_at || order.created_at, input.locale)
  const issuedAt = formatReceiptDay(order.paid_at || order.completed_at || order.created_at, input.locale)
  const itemName = order.order_type === 'subscription' ? copy.itemSubscription : copy.itemBalance
  const itemDesc = order.order_type === 'subscription' ? copy.itemSubscriptionDesc : copy.itemBalanceDesc
  const receiptNo = buildReceiptNo(order)
  return {
    receiptNo,
    issuedAt,
    paidAt,
    refunded,
    statusLabel: refunded ? copy.refunded : copy.paid,
    settledLabel: refunded ? copy.settledRefunded : copy.settledYes,
    amountText,
    amountWords: amountInWords(amount, currency),
    currency,
    payerEmail: input.payer?.email?.trim() || '—',
    payerName: input.payer?.username?.trim() || input.payer?.email?.trim() || '—',
    paymentMethod: input.paymentMethodLabel,
    tradeNo: order.payment_trade_no || order.out_trade_no || `ORD-${order.id}`,
    merchant: resolveReceiptMerchant(input.siteName, input.siteUrl),
    siteUrl: resolveReceiptSiteUrl(input.siteUrl),
    contactInfo: input.contactInfo?.trim() || '',
    itemName,
    itemDesc,
    quantity: '1',
    unitPrice: amountText,
    lineAmount: amountText,
    filename: `${receiptNo}.pdf`,
  }
}
