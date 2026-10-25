/** A fiscal receipt's verification link (the countries the server reads).
 *  Receipts often carry a second QR code — the shop's website or loyalty
 *  app — which this tells apart. */
export const RECEIPT_URL = /^\s*https?:\/\/(suf\.poreskaupravars\.org|suf\.purs\.gov\.rs|mapr\.tax\.gov\.me|efi\.porezi\.me|(www\.)?porezna\.gov\.hr)\//i

export const isReceiptLink = (text: string) => RECEIPT_URL.test(text)

/** "centrumshop.ba" for a hint about the other QR code. */
export function hostOf(text: string): string {
  try { return new URL(text.trim()).host.replace(/^www\./, '') } catch { return text.trim().slice(0, 40) }
}
