import { DEFAULT_PAYMENT_CURRENCY, normalizePaymentCurrency } from './currency'

const ZERO_DECIMAL_CURRENCIES = new Set(['BIF', 'CLP', 'DJF', 'GNF', 'JPY', 'KMF', 'KRW', 'MGA', 'PYG', 'RWF', 'VND', 'VUV', 'XAF', 'XOF', 'XPF', 'ISK', 'UGX'])
const THREE_DECIMAL_CURRENCIES = new Set(['BHD', 'IQD', 'JOD', 'KWD', 'LYD', 'OMR', 'TND'])

function rational(value: number): { numerator: bigint; denominator: bigint } {
  const [mantissa, exponent = '0'] = value.toString().toLowerCase().split('e')
  const [whole, fraction = ''] = mantissa.split('.')
  const scale = fraction.length - Number(exponent)
  const digits = BigInt(whole + fraction)
  return scale >= 0
    ? { numerator: digits, denominator: 10n ** BigInt(scale) }
    : { numerator: digits * 10n ** BigInt(-scale), denominator: 1n }
}

function roundHalfUp(numerator: bigint, denominator: bigint): bigint {
  return numerator / denominator + (numerator % denominator * 2n >= denominator ? 1n : 0n)
}

// Match backend payment/fee.go and payment_order.go: decimal FX rounding,
// then decimal fee ceiling once, at the gateway currency's payment precision.
export function subscriptionPaymentAmounts(amount: number, currency: string, usdToCnyRate: number, feeRate: number) {
  if (!Number.isFinite(amount) || amount < 0 || !Number.isFinite(feeRate)) return { base: 0, fee: 0, total: 0, valid: false }
  const normalized = normalizePaymentCurrency(currency)
  const digits = ZERO_DECIMAL_CURRENCIES.has(normalized) ? 0 : THREE_DECIMAL_CURRENCIES.has(normalized) ? 3 : 2
  const factor = 10n ** BigInt(digits)
  const source = rational(amount)
  let numerator = source.numerator * factor
  let denominator = source.denominator
  const convert = normalized === DEFAULT_PAYMENT_CURRENCY && Number.isFinite(usdToCnyRate) && usdToCnyRate > 0
  if (convert) {
    const rate = rational(usdToCnyRate)
    numerator *= rate.numerator
    denominator *= rate.denominator
  }
  const valid = convert || numerator % denominator === 0n
  const baseMinor = roundHalfUp(numerator, denominator)
  let feeMinor = 0n
  if (feeRate > 0) {
    const rate = rational(feeRate)
    const feeNumerator = baseMinor * rate.numerator
    const feeDenominator = rate.denominator * 100n
    feeMinor = feeNumerator / feeDenominator + (feeNumerator % feeDenominator > 0n ? 1n : 0n)
  }
  return { base: Number(baseMinor) / Number(factor), fee: Number(feeMinor) / Number(factor), total: Number(baseMinor + feeMinor) / Number(factor), valid }
}
