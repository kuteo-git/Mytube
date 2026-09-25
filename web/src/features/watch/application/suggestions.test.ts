import { describe, expect, it } from 'vitest'
import { columnBesideVideo } from './suggestions'

// The switch that hides the suggestions column is drawn on the player, and it
// must only exist where that column does.
//
// Reported twice, from the two directions:
//   - narrow window: the rail stacks under the video, so the switch changed a
//     layout that was not on screen;
//   - full screen: the window is still wide, so a width-only rule said yes
//     while the whole screen was the picture. Measured: the rail stayed 402px
//     and the picture stayed 1440px whether it was pressed or not.
describe('whether there is a column beside the video', () => {
  it('there is one on a wide page that is not filled by the video', () => {
    expect(columnBesideVideo({ wide: true, fullscreen: false })).toBe(true)
  })

  it('there is none in full screen, however wide the window', () => {
    // The case the first fix missed. Full screen does not narrow the window,
    // so width alone cannot answer this.
    expect(columnBesideVideo({ wide: true, fullscreen: true })).toBe(false)
  })

  it('there is none on a narrow page', () => {
    expect(columnBesideVideo({ wide: false, fullscreen: false })).toBe(false)
  })

  it('there is none when both are true', () => {
    expect(columnBesideVideo({ wide: false, fullscreen: true })).toBe(false)
  })
})
