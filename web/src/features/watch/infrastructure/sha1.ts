/**
 * SHA-1 in plain JavaScript.
 *
 * Not for security — this is a cache key. It exists because `crypto.subtle` is
 * only defined in a secure context, and this library is served over plain HTTP
 * to a LAN address (CLAUDE.md §2). `localhost` is exempt from that rule, which
 * is exactly why every test and every curl passed while the browser on the
 * house network threw before it could send a single request.
 *
 * Produces the same digest as crypto.subtle, so caches written before this
 * still match.
 */
export function sha1Hex(input: string): string {
  const bytes = new TextEncoder().encode(input)

  // Pad to a multiple of 64 bytes: 0x80, zeroes, then the length in bits.
  const bitLen = bytes.length * 8
  const withPad = new Uint8Array(((bytes.length + 8) >> 6) * 64 + 64)
  withPad.set(bytes)
  withPad[bytes.length] = 0x80
  const view = new DataView(withPad.buffer)
  // Lengths are written as a 64-bit big-endian count; the high word is only
  // non-zero past 512 MiB of input, which a subtitle cue will never be.
  view.setUint32(withPad.length - 4, bitLen >>> 0, false)
  view.setUint32(withPad.length - 8, Math.floor(bitLen / 0x100000000), false)

  let h0 = 0x67452301
  let h1 = 0xefcdab89
  let h2 = 0x98badcfe
  let h3 = 0x10325476
  let h4 = 0xc3d2e1f0

  const w = new Uint32Array(80)
  const rol = (n: number, s: number) => (n << s) | (n >>> (32 - s))

  for (let i = 0; i < withPad.length; i += 64) {
    for (let j = 0; j < 16; j++) w[j] = view.getUint32(i + j * 4, false)
    for (let j = 16; j < 80; j++) {
      w[j] = rol(w[j - 3] ^ w[j - 8] ^ w[j - 14] ^ w[j - 16], 1)
    }

    let a = h0
    let b = h1
    let c = h2
    let d = h3
    let e = h4

    for (let j = 0; j < 80; j++) {
      let f: number
      let k: number
      if (j < 20) {
        f = (b & c) | (~b & d)
        k = 0x5a827999
      } else if (j < 40) {
        f = b ^ c ^ d
        k = 0x6ed9eba1
      } else if (j < 60) {
        f = (b & c) | (b & d) | (c & d)
        k = 0x8f1bbcdc
      } else {
        f = b ^ c ^ d
        k = 0xca62c1d6
      }
      const t = (rol(a, 5) + f + e + k + w[j]) >>> 0
      e = d
      d = c
      c = rol(b, 30) >>> 0
      b = a
      a = t
    }

    h0 = (h0 + a) >>> 0
    h1 = (h1 + b) >>> 0
    h2 = (h2 + c) >>> 0
    h3 = (h3 + d) >>> 0
    h4 = (h4 + e) >>> 0
  }

  return [h0, h1, h2, h3, h4]
    .map((n) => n.toString(16).padStart(8, '0'))
    .join('')
}
