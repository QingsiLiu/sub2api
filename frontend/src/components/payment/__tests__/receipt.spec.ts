import { describe, expect, it } from 'vitest'
import type { PaymentOrder } from '@/types/payment'
import {
  amountInChineseYuan,
  amountInWords,
  buildReceiptModel,
  buildReceiptNo,
  canViewReceipt,
} from '../receipt'

const copy = {
  title: '付款收据',
  paid: '已付款',
  refunded: '已退款',
  receiptNo: '收据编号',
  issuedAt: '开具日期',
  received: '实收金额',
  amountWords: '金额大写',
  payer: '付款客户',
  payerName: '客户名称',
  sectionTrade: '交易信息',
  paidAt: '付款时间',
  method: '支付方式',
  tradeNo: '交易流水号',
  settled: '入账状态',
  settledYes: '已到账',
  settledRefunded: '已退款',
  merchant: '收款主体',
  currency: '币种',
  sectionItems: '服务项目',
  itemName: '项目名称',
  itemDesc: '服务说明',
  qty: '数量',
  unitPrice: '单价',
  lineAmount: '金额',
  total: '合计',
  notesTitle: '收据说明',
  noteProof: 'a',
  noteNotInvoice: 'b',
  noteRefund: 'c',
  issuerTitle: '开具方',
  itemBalance: 'API 账户充值',
  itemBalanceDesc: '用于 API 调用额度消费',
  itemSubscription: '订阅套餐',
  itemSubscriptionDesc: '用于订阅额度消费',
}

const order = (overrides: Partial<PaymentOrder> = {}): PaymentOrder => ({
  id: 81,
  user_id: 9,
  amount: 25000,
  pay_amount: 25000,
  currency: 'CNY',
  fee_rate: 0,
  payment_type: 'alipay',
  out_trade_no: 'sub2_20260806abc',
  status: 'COMPLETED',
  order_type: 'balance',
  created_at: '2026-08-06T15:55:07+08:00',
  expires_at: '2026-08-06T16:25:07+08:00',
  paid_at: '2026-08-06T15:55:07+08:00',
  completed_at: '2026-08-06T15:55:08+08:00',
  refund_amount: 0,
  ...overrides,
})

describe('receipt helpers', () => {
  it('only shows a receipt after the payment has been credited', () => {
    expect(canViewReceipt(order({ status: 'PENDING' }))).toBe(false)
    expect(canViewReceipt(order({ status: 'COMPLETED' }))).toBe(true)
    expect(canViewReceipt(order({ status: 'REFUNDED' }))).toBe(true)
  })

  it.each([
    [0, '人民币零元整'],
    [0.05, '人民币零元伍分'],
    [0.5, '人民币零元伍角'],
    [1, '人民币壹元整'],
    [1.05, '人民币壹元零伍分'],
    [12.34, '人民币壹拾贰元叁角肆分'],
    [1001, '人民币壹仟零壹元整'],
    [25000, '人民币贰万伍仟元整'],
  ])('writes %s as %s', (amount, words) => {
    expect(amountInChineseYuan(amount as number)).toBe(words)
  })

  it('keeps non-CNY amounts as currency plus digits', () => {
    expect(amountInWords(88, 'USD')).toBe('USD 88.00')
  })

  it('builds a receipt from a completed top-up', () => {
    const model = buildReceiptModel({
      order: order(),
      payer: { email: 'hcdmumu@gmail.com', username: 'mumu' },
      siteName: '给力 API',
      siteUrl: 'https://sub.geiliapi.com/',
      contactInfo: 'support@example.com',
      locale: 'zh-CN',
      copy,
      paymentMethodLabel: '支付宝',
    })
    expect(model.receiptNo).toBe(buildReceiptNo(order()))
    expect(model.filename).toBe(`${model.receiptNo}.pdf`)
    expect(model.refunded).toBe(false)
    expect(model.statusLabel).toBe('已付款')
    expect(model.settledLabel).toBe('已到账')
    expect(model.amountWords).toBe('人民币贰万伍仟元整')
    expect(model.payerEmail).toBe('hcdmumu@gmail.com')
    expect(model.itemName).toBe('API 账户充值')
    expect(model.merchant).toBe('给力 API')
    expect(model.siteUrl).toBe('https://sub.geiliapi.com')
    expect(model.tradeNo).toBe('sub2_20260806abc')
    expect(model.paymentMethod).toBe('支付宝')
  })

  it('marks refunded subscription receipts', () => {
    const model = buildReceiptModel({
      order: order({ status: 'REFUNDED', order_type: 'subscription', pay_amount: 99 }),
      payer: { email: 'user@example.com', username: 'user' },
      siteName: '给力 API',
      siteUrl: 'https://sub.geiliapi.com',
      copy,
      paymentMethodLabel: '微信支付',
    })
    expect(model.refunded).toBe(true)
    expect(model.statusLabel).toBe('已退款')
    expect(model.itemName).toBe('订阅套餐')
  })
})
