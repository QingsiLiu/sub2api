import { describe, expect, it } from 'vitest'
import { accountFinancialCost, dashboardFinancialMetadata, escapeFinancialCSV, financialDate, financialExportNumber, financialMoney, financialTokenTotal, financialTimezone, safeSpreadsheetCell } from '../financialUsage'

describe('financial evidence presentation', () => {
  it('keeps exact zero distinct from unknown or nonfinite money', () => {
    expect(financialMoney(0, 'unknown', 8)).toBe('$0.00000000')
    expect(financialMoney(46.78302384, 'unknown', 8)).toBe('$46.78302384')
    for (const value of [null, undefined, NaN, Infinity]) {
      expect(financialMoney(value, 'unknown')).toBe('unknown')
      expect(financialExportNumber(value, 8)).toBe('')
    }
    expect(financialExportNumber(0, 8)).toBe('0.00000000')
  })
  it('does not invent missing token counts or account costs', () => {
    expect(financialTokenTotal({ input_tokens: null, output_tokens: 10 })).toBeNull()
    expect(financialTokenTotal({ input_tokens: 0, output_tokens: 0 })).toBe(0)
    expect(accountFinancialCost({ total_cost: null, record_completeness: 'partial' })).toBeNull()
    expect(accountFinancialCost({ total_cost: 2, record_completeness: 'partial' })).toBeNull()
    expect(accountFinancialCost({ total_cost: 2, account_rate_multiplier: 0, record_completeness: 'complete' })).toBe(0)
  })
  it.each(['=1+1', '+SUM(A1)', '-1+2', '@SUM(A1)', '\t=1+1', '\r=1+1', '\n=1+1', '  =1+1', '\u0000=1+1'])('neutralizes spreadsheet formulas: %j', value => {
    expect(safeSpreadsheetCell(value)).toBe(`'${value}`)
    expect(escapeFinancialCSV(value).replace(/^"/, '').startsWith("'")).toBe(true)
  })
  it('preserves numeric spreadsheet cells and CSV escaping', () => {
    expect(safeSpreadsheetCell(-2)).toBe(-2)
    expect(safeSpreadsheetCell(null)).toBe('')
    expect(escapeFinancialCSV('a,"b"\nc')).toBe('"a,""b""\nc"')
    expect(escapeFinancialCSV('normal')).toBe('normal')
  })
  it('anchors accounting day to Beijing across midnight independent of browser timezone', () => {
    expect(financialDate(new Date('2026-09-25T15:59:59Z'))).toBe('2026-09-25')
    expect(financialDate(new Date('2026-09-25T16:00:00Z'))).toBe('2026-09-26')
    expect(financialTimezone('accounting')).toBe('Asia/Shanghai')
  })
  it('maps dashboard periods without mixing current day with all-time missing records', () => {
    const stats = { today_subscription_actual_cost: 90, total_subscription_actual_cost: 100, today_unknown_amount_count: 0, total_unknown_amount_count: 3 }
    expect(dashboardFinancialMetadata(stats, 'today')).toMatchObject({ subscription_actual_cost: 90, unknown_amount_count: 0 })
    expect(dashboardFinancialMetadata(stats, 'total')).toMatchObject({ subscription_actual_cost: 100, unknown_amount_count: 3 })
  })
})
