import type { AdminUsageLog, FinancialDashboardMetadata, FinancialStatsMetadata, UsageDateBasis, UsageLog } from '@/types'

// geili hook: missing evidence is not a zero-priced request.
export const finiteFinancialNumber = (value: unknown): value is number =>
  typeof value === 'number' && Number.isFinite(value)

export function financialMoney(value: unknown, unknownLabel: string, digits = 4): string {
  return finiteFinancialNumber(value) ? `$${value.toFixed(digits)}` : unknownLabel
}

export function financialExportNumber(value: unknown, digits?: number): string | number {
  if (!finiteFinancialNumber(value)) return ''
  return digits === undefined ? value : value.toFixed(digits)
}

export function financialTokenTotal(log: Pick<UsageLog, 'input_tokens' | 'output_tokens'>): number | null {
  return finiteFinancialNumber(log.input_tokens) && finiteFinancialNumber(log.output_tokens)
    ? log.input_tokens + log.output_tokens : null
}

export function accountFinancialCost(row: Pick<AdminUsageLog, 'total_cost' | 'account_stats_cost' | 'account_rate_multiplier' | 'record_completeness'>): number | null {
  const base = row.account_stats_cost ?? row.total_cost
  if (!finiteFinancialNumber(base)) return null
  if (row.account_rate_multiplier == null && row.record_completeness && row.record_completeness !== 'complete') return null
  const value = base * (row.account_rate_multiplier ?? 1)
  return finiteFinancialNumber(value) ? value : null
}

// Neutralize formula prefixes even behind leading whitespace/control characters.
// Numeric values remain numeric in XLSX; unknown values stay blank, never zero.
export function safeSpreadsheetCell(value: unknown): string | number | boolean {
  if (value == null) return ''
  if (typeof value === 'number') return Number.isFinite(value) ? value : ''
  if (typeof value === 'boolean') return value
  const text = String(value)
  if (text === '-') return text
  let prefix = 0
  while (prefix < text.length && (/\s/u.test(text[prefix]) || text.charCodeAt(prefix) < 32)) prefix++
  return /^[=+\-@]/u.test(text.slice(prefix)) || /^[\t\r\n]/u.test(text) ? `'${text}` : text
}

export function escapeFinancialCSV(value: unknown): string {
  const safe = safeSpreadsheetCell(value)
  const text = String(safe)
  const neutralized = typeof value === 'string' && safe !== value
  return neutralized || /[,"\n\r]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text
}

export function financialDate(date: Date = new Date(), _basis: UsageDateBasis = 'accounting'): string {
  // Completed-time reporting is retired; keep the parameter for API/source
  // compatibility while always using the Beijing accounting calendar.
  return new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).format(date)
}

export const financialTimezone = (_basis: UsageDateBasis): string => 'Asia/Shanghai'

export function dashboardFinancialMetadata(stats: FinancialDashboardMetadata, period: 'today' | 'total'): FinancialStatsMetadata {
  return {
    date_basis: stats.date_basis,
    balance_actual_cost: stats[`${period}_balance_actual_cost`],
    subscription_actual_cost: stats[`${period}_subscription_actual_cost`],
    detail_pending_count: stats[`${period}_detail_pending_count`],
    unknown_amount_count: stats[`${period}_unknown_amount_count`],
    incomplete_record_count: stats[`${period}_incomplete_record_count`],
    standard_cost_complete: stats[`${period}_standard_cost_complete`],
    token_counts_complete: stats[`${period}_token_counts_complete`],
  }
}

export type FinancialTimeExtra = { key: 'accountingDate' | 'completedAt' | 'settledAt'; value: string | null; time: boolean }

// geili hook: secondary financial times appear only when they add information.
// Most rows complete, settle and belong to the same Beijing day within seconds.
const FINANCIAL_TIME_GAP_MS = 60_000

export function financialTimeExtras(row: Pick<UsageLog, 'created_at'> & { accounting_date?: string | null; completed_at?: string | null; settled_at?: string | null }): FinancialTimeExtra[] {
  const shown = row.created_at ? new Date(row.created_at).getTime() : NaN
  const differs = (value: string | null | undefined): value is string => {
    if (!value) return false
    const at = new Date(value).getTime()
    return !Number.isFinite(shown) || !Number.isFinite(at) || Math.abs(at - shown) > FINANCIAL_TIME_GAP_MS
  }
  const extras: FinancialTimeExtra[] = []
  if ('accounting_date' in row && (!row.accounting_date || !Number.isFinite(shown) || row.accounting_date !== financialDate(new Date(shown)))) {
    extras.push({ key: 'accountingDate', value: row.accounting_date || null, time: false })
  }
  if (differs(row.completed_at)) extras.push({ key: 'completedAt', value: row.completed_at, time: true })
  if (differs(row.settled_at)) extras.push({ key: 'settledAt', value: row.settled_at, time: true })
  return extras
}
