import { describe, expect, it } from 'vitest'
import { sha1Hex } from './sha1'

describe('sha1Hex', () => {
  it('matches the known digests', () => {
    expect(sha1Hex('')).toBe('da39a3ee5e6b4b0d3255bfef95601890afd80709')
    expect(sha1Hex('abc')).toBe('a9993e364706816aba3e25717850c26c9cd0d89d')
    expect(sha1Hex('The quick brown fox jumps over the lazy dog')).toBe(
      '2fd4e1c67a2d28fced849ee1bb76e7391b93eb12',
    )
  })

  it('handles a payload that crosses a block boundary', () => {
    // 56-63 bytes is where the length no longer fits in the first block.
    expect(sha1Hex('a'.repeat(56))).toBe(
      'c2db330f6083854c99d4b5bfb6e8f29f201be699',
    )
    expect(sha1Hex('a'.repeat(64))).toBe(
      '0098ba824b5c16427bd7a1122a5a442a25ec644d',
    )
  })

  it('hashes the bytes of non-ASCII text, not its code units', () => {
    // Cue text is Vietnamese. A digest over UTF-16 units would not match what
    // any other tool, or the previous crypto.subtle implementation, produces.
    // Values from Node's crypto, which is the same algorithm crypto.subtle ran.
    expect(sha1Hex('á')).toBe('2b9cc8d86a48fd3e4e76e117b1bd08884ec9691d')
    expect(sha1Hex('xin chào')).toBe('a77dcdd85a3f4a9ee90775dcab6d8ca0640e2c48')
  })
})
