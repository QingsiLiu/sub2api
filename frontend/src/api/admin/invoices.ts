/**
 * Admin invoice API (geili)
 * Commercial invoices issued by administrators, plus the issuer profile.
 */

import { apiClient } from '../client'
import type { PaginatedResponse } from '@/types'

export const INVOICE_CURRENCIES = ['USD', 'HKD', 'CNY'] as const
export type InvoiceCurrency = (typeof INVOICE_CURRENCIES)[number]

export interface InvoiceIssuer {
  company_name: string
  company_address: string
  company_contact: string
  number_prefix: string
  payment: Record<InvoiceCurrency, string>
  footer_note: string
  seal_image: string
  updated_at: string
}

export interface InvoiceIssuerPayload {
  company_name: string
  company_address: string
  company_contact: string
  number_prefix: string
  payment: Record<InvoiceCurrency, string>
  footer_note: string
  /** Omit to keep the stored seal; empty string removes it. */
  seal_image?: string
}

export interface Invoice {
  id: number
  invoice_no: string
  issue_date: string
  currency: InvoiceCurrency
  amount: string
  description: string
  bill_to_name: string
  bill_to_address: string
  notes: string
  issuer_name: string
  issuer_address: string
  issuer_contact: string
  payment_details: string
  footer_note: string
  created_by: number
  created_at: string
  voided_at: string | null
}

export interface InvoicePayload {
  issue_date?: string
  currency: InvoiceCurrency
  amount: string
  description: string
  bill_to_name?: string
  bill_to_address?: string
  notes?: string
}

export interface InvoiceDocument {
  invoice: Invoice
  seal_image: string
}

export const invoicesAPI = {
  async getIssuer() {
    return (await apiClient.get<InvoiceIssuer>('/admin/invoices/issuer')).data
  },
  async updateIssuer(payload: InvoiceIssuerPayload) {
    return (await apiClient.put<InvoiceIssuer>('/admin/invoices/issuer', payload)).data
  },
  async list(page: number, pageSize: number) {
    return (await apiClient.get<PaginatedResponse<Invoice>>('/admin/invoices', { params: { page, page_size: pageSize } })).data
  },
  async create(payload: InvoicePayload) {
    return (await apiClient.post<Invoice>('/admin/invoices', payload)).data
  },
  async get(id: number) {
    return (await apiClient.get<InvoiceDocument>(`/admin/invoices/${id}`)).data
  },
  async void(id: number) {
    return (await apiClient.post<Invoice>(`/admin/invoices/${id}/void`)).data
  }
}

/** Groups thousands without going through floating point: "1234567.5" → "1,234,567.50". */
export function formatInvoiceAmount(amount: string): string {
  const [whole, frac = ''] = amount.split('.')
  return whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',') + '.' + (frac + '00').slice(0, 2)
}
