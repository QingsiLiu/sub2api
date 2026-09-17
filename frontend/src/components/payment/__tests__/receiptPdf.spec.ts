import { describe, expect, it } from 'vitest'
import { embedJpegInA4Pdf } from '../receiptPdf'

describe('embedJpegInA4Pdf', () => {
  it('wraps JPEG bytes in a one-page PDF', () => {
    const jpeg = new Uint8Array([0xff, 0xd8, 0xff, 0xd9, 1, 2, 3])
    const pdf = embedJpegInA4Pdf(jpeg, 100, 140)
    const text = new TextDecoder().decode(pdf)
    expect(text.startsWith('%PDF-1.4')).toBe(true)
    expect(text).toContain('/Subtype /Image')
    expect(text).toContain('/Filter /DCTDecode')
    expect(text).toContain('%%EOF')
    expect(Array.from(pdf)).toEqual(expect.arrayContaining([0xff, 0xd8, 0xff, 0xd9, 1, 2, 3]))
  })
})
