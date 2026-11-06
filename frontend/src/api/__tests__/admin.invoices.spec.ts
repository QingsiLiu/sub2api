import { describe, expect, it } from 'vitest'
import { formatInvoiceAmount } from '@/api/admin/invoices'

describe('formatInvoiceAmount', () => {
  it('groups thousands and pads to two decimals without float rounding', () => {
    expect(formatInvoiceAmount('0.01')).toBe('0.01')
    expect(formatInvoiceAmount('5')).toBe('5.00')
    expect(formatInvoiceAmount('1234.5')).toBe('1,234.50')
    expect(formatInvoiceAmount('999999999999.99')).toBe('999,999,999,999.99')
    expect(formatInvoiceAmount('1000000.00')).toBe('1,000,000.00')
  })
})
