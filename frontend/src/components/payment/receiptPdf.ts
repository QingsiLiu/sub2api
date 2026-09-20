import { saveAs } from 'file-saver'
import type { ReceiptCopy, ReceiptModel } from './receipt'

const PAGE_W = 595.28
const PAGE_H = 841.89
const CANVAS_W = 1240

function dataUrlToBytes(dataUrl: string): Uint8Array {
  const comma = dataUrl.indexOf(',')
  const binary = atob(comma >= 0 ? dataUrl.slice(comma + 1) : dataUrl)
  const out = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i)
  return out
}

function concatBytes(parts: Array<Uint8Array | string>): Uint8Array {
  const encoded = parts.map(part => (typeof part === 'string' ? new TextEncoder().encode(part) : part))
  const total = encoded.reduce((sum, part) => sum + part.length, 0)
  const out = new Uint8Array(total)
  let offset = 0
  for (const part of encoded) {
    out.set(part, offset)
    offset += part.length
  }
  return out
}

export function embedJpegInA4Pdf(jpeg: Uint8Array, widthPx: number, heightPx: number): Uint8Array {
  const margin = 28
  const imgW = PAGE_W - margin * 2
  const imgH = imgW * (heightPx / Math.max(widthPx, 1))
  const y = PAGE_H - margin - imgH
  const content = `q\n${imgW.toFixed(2)} 0 0 ${imgH.toFixed(2)} ${margin.toFixed(2)} ${y.toFixed(2)} cm\n/Im0 Do\nQ\n`
  const objects: Uint8Array[] = []
  const push = (body: Array<Uint8Array | string>) => {
    objects.push(concatBytes(body))
  }
  push(['<< /Type /Catalog /Pages 2 0 R >>\n'])
  push(['<< /Type /Pages /Kids [3 0 R] /Count 1 >>\n'])
  push([`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 ${PAGE_W} ${PAGE_H}] /Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>\n`])
  push([
    `<< /Type /XObject /Subtype /Image /Width ${Math.round(widthPx)} /Height ${Math.round(heightPx)} /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length ${jpeg.length} >>\nstream\n`,
    jpeg,
    '\nendstream\n',
  ])
  push([`<< /Length ${content.length} >>\nstream\n`, content, 'endstream\n'])

  const chunks: Array<Uint8Array | string> = ['%PDF-1.4\n']
  const offsets = [0]
  let cursor = '%PDF-1.4\n'.length
  objects.forEach((object, index) => {
    const header = `${index + 1} 0 obj\n`
    offsets.push(cursor)
    chunks.push(header, object, 'endobj\n')
    cursor += header.length + object.length + 'endobj\n'.length
  })
  const xrefStart = cursor
  let xref = `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`
  for (let i = 1; i <= objects.length; i++) {
    xref += `${String(offsets[i]).padStart(10, '0')} 00000 n \n`
  }
  chunks.push(xref, `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xrefStart}\n%%EOF\n`)
  return concatBytes(chunks)
}

function drawWrapped(
  ctx: CanvasRenderingContext2D,
  text: string,
  x: number,
  y: number,
  maxWidth: number,
  lineHeight: number,
): number {
  const chars = text.split('')
  let line = ''
  let cursor = y
  for (const ch of chars) {
    const next = line + ch
    if (ctx.measureText(next).width > maxWidth && line) {
      ctx.fillText(line, x, cursor)
      line = ch
      cursor += lineHeight
    } else {
      line = next
    }
  }
  if (line) {
    ctx.fillText(line, x, cursor)
    cursor += lineHeight
  }
  return cursor
}

function drawKvRow(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  width: number,
  label: string,
  value: string,
  labelWidth: number,
): number {
  ctx.fillStyle = '#f8fafc'
  ctx.fillRect(x, y, width, 46)
  ctx.strokeStyle = '#e2e8f0'
  ctx.strokeRect(x, y, width, 46)
  ctx.fillStyle = '#64748b'
  ctx.font = '22px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.textBaseline = 'middle'
  ctx.fillText(label, x + 16, y + 23)
  ctx.fillStyle = '#0f172a'
  ctx.font = '22px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  const valueX = x + labelWidth
  ctx.fillText(value, valueX, y + 23)
  return y + 46
}

export function renderReceiptCanvas(model: ReceiptModel, copy: ReceiptCopy): HTMLCanvasElement {
  const width = CANVAS_W
  const height = 1680
  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('canvas')

  ctx.fillStyle = '#ffffff'
  ctx.fillRect(0, 0, width, height)

  const pad = 64
  ctx.fillStyle = '#0f766e'
  ctx.font = '600 26px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.textBaseline = 'top'
  ctx.fillText(model.merchant, pad, 48)
  ctx.fillStyle = model.refunded ? '#b45309' : '#0f766e'
  ctx.textAlign = 'right'
  ctx.fillText(model.statusLabel, width - pad, 48)
  ctx.textAlign = 'left'

  ctx.fillStyle = '#0f172a'
  ctx.font = '700 56px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.fillText(copy.title, pad, 100)
  ctx.fillStyle = '#64748b'
  ctx.font = '22px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.fillText(`${copy.receiptNo}  ${model.receiptNo}`, pad, 176)
  ctx.textAlign = 'right'
  ctx.fillText(`${copy.issuedAt}  ${model.issuedAt}`, width - pad, 176)
  ctx.textAlign = 'left'

  ctx.fillStyle = '#0f766e'
  ctx.fillRect(pad, 220, width - pad * 2, 8)

  ctx.fillStyle = '#64748b'
  ctx.font = '22px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.fillText(copy.received, pad, 260)
  ctx.fillStyle = '#0f172a'
  ctx.font = '700 64px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.fillText(model.amountText, pad, 296)
  ctx.fillStyle = '#64748b'
  ctx.font = '22px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.fillText(`${copy.amountWords}：${model.amountWords}`, pad, 380)

  ctx.textAlign = 'right'
  ctx.fillStyle = '#64748b'
  ctx.fillText(copy.payer, width - pad, 260)
  ctx.fillStyle = '#0f172a'
  ctx.font = '26px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.fillText(model.payerEmail, width - pad, 300)
  ctx.fillStyle = '#64748b'
  ctx.font = '22px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.fillText(`${copy.payerName}：${model.payerName}`, width - pad, 344)
  ctx.textAlign = 'left'

  let y = 430
  ctx.fillStyle = '#0f172a'
  ctx.font = '600 26px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.fillText(copy.sectionTrade, pad, y)
  y += 40
  const colW = (width - pad * 2) / 2
  const left = pad
  const right = pad + colW
  y = drawKvRow(ctx, left, y, colW, copy.paidAt, model.paidAt, 150)
  drawKvRow(ctx, right, y - 46, colW, copy.method, model.paymentMethod, 150)
  y = drawKvRow(ctx, left, y, colW, copy.tradeNo, model.tradeNo, 150)
  drawKvRow(ctx, right, y - 46, colW, copy.settled, model.settledLabel, 150)
  y = drawKvRow(ctx, left, y, colW, copy.merchant, model.merchant, 150)
  drawKvRow(ctx, right, y - 46, colW, copy.currency, model.currency, 150)
  y += 36

  ctx.fillStyle = '#0f172a'
  ctx.font = '600 26px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.textBaseline = 'top'
  ctx.fillText(copy.sectionItems, pad, y)
  y += 40
  const cols = [280, 360, 100, 180, 180]
  const headers = [copy.itemName, copy.itemDesc, copy.qty, copy.unitPrice, copy.lineAmount]
  const values = [model.itemName, model.itemDesc, model.quantity, model.unitPrice, model.lineAmount]
  ctx.fillStyle = '#f1f5f9'
  ctx.fillRect(pad, y, width - pad * 2, 44)
  ctx.strokeStyle = '#e2e8f0'
  ctx.strokeRect(pad, y, width - pad * 2, 44)
  ctx.fillStyle = '#64748b'
  ctx.font = '20px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.textBaseline = 'middle'
  let x = pad + 16
  headers.forEach((header, i) => {
    ctx.fillText(header, x, y + 22)
    x += cols[i]
  })
  y += 44
  ctx.fillStyle = '#ffffff'
  ctx.fillRect(pad, y, width - pad * 2, 56)
  ctx.strokeStyle = '#e2e8f0'
  ctx.strokeRect(pad, y, width - pad * 2, 56)
  ctx.fillStyle = '#0f172a'
  ctx.font = '22px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  x = pad + 16
  values.forEach((value, i) => {
    ctx.fillText(value, x, y + 28)
    x += cols[i]
  })
  y += 56
  ctx.fillStyle = '#f8fafc'
  ctx.fillRect(pad, y, width - pad * 2, 52)
  ctx.strokeStyle = '#e2e8f0'
  ctx.strokeRect(pad, y, width - pad * 2, 52)
  ctx.fillStyle = '#0f172a'
  ctx.font = '600 24px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.fillText(copy.total, pad + 16, y + 26)
  ctx.textAlign = 'right'
  ctx.fillText(model.lineAmount, width - pad - 16, y + 26)
  ctx.textAlign = 'left'
  y += 88

  const notesWidth = 620
  ctx.fillStyle = '#0f172a'
  ctx.font = '600 24px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.textBaseline = 'top'
  ctx.fillText(copy.notesTitle, pad, y)
  ctx.fillText(copy.issuerTitle, pad + notesWidth + 40, y)
  ctx.fillStyle = '#475569'
  ctx.font = '20px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  let noteY = y + 36
  ;[copy.noteProof, copy.noteRefund].forEach((line, i) => {
    noteY = drawWrapped(ctx, `${i + 1}. ${line}`, pad, noteY, notesWidth, 28)
  })
  ctx.fillStyle = '#0f172a'
  ctx.font = '22px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.fillText(model.merchant, pad + notesWidth + 40, y + 36)
  ctx.fillStyle = '#64748b'
  ctx.font = '20px "PingFang SC","Hiragino Sans GB","Noto Sans SC","Microsoft YaHei",sans-serif'
  ctx.fillText(model.siteUrl, pad + notesWidth + 40, y + 70)
  if (model.contactInfo) ctx.fillText(model.contactInfo, pad + notesWidth + 40, y + 100)
  return canvas
}

export function downloadReceiptPdf(model: ReceiptModel, copy: ReceiptCopy): void {
  const canvas = renderReceiptCanvas(model, copy)
  const jpeg = dataUrlToBytes(canvas.toDataURL('image/jpeg', 0.92))
  const pdf = embedJpegInA4Pdf(jpeg, canvas.width, canvas.height)
  saveAs(new Blob([pdf], { type: 'application/pdf' }), model.filename)
}
