import { describe, expect, it } from 'vitest'
import { bareTitle, isBareScreen } from './bare-screens'

describe('which screens carry their own chrome', () => {
  it('counts everywhere you arrive on purpose', () => {
    for (const p of [
      '/saved',
      '/storage',
      '/activity',
      '/settings/feed',
      '/settings/narration',
      '/settings/translation',
      '/channel/UC123',
    ]) {
      expect(isBareScreen(p)).toBe(true)
    }
  })

  it('does not count the places you move between', () => {
    // What earns a place in the bottom bar is passing through. Those keep the
    // search bar and the tab bar; a screen that took them away would have
    // nowhere to go next.
    for (const p of ['/', '/subscriptions', '/history', '/settings', '/topic/Music']) {
      expect(isBareScreen(p)).toBe(false)
    }
  })

  // A list and the pages under it have to agree, and they did not: Watch later
  // and Playlists dropped the tab bar while a playlist's own page kept it, so
  // the parent lost the navigation and the child had it. Whichever way that is
  // argued, it cannot be argued both ways at once.
  it('keeps the navigation on the lists and on a list', () => {
    for (const p of ['/watch-later', '/playlists', '/playlist/pl_1']) {
      expect(isBareScreen(p)).toBe(false)
    }
  })

  it('does not mistake Settings itself for one of its screens', () => {
    // `/settings` is a tab; `/settings/feed` is a screen it leads to. A prefix
    // match would have taken the tab bar away from the tab.
    expect(isBareScreen('/settings')).toBe(false)
    expect(isBareScreen('/settings/feed')).toBe(true)
  })
})

describe('what the back bar calls a screen', () => {
  it('names the ones the shell draws for', () => {
    expect(bareTitle('/storage')).toBe('Storage')
    expect(bareTitle('/settings/narration')).toBe('Narration')
  })

  it('leaves a channel to name itself', () => {
    // ChannelHeader gives the name in large type, and the bar fades its own
    // copy in only once that has scrolled away — behaviour the page owns, not
    // something a table can say.
    expect(bareTitle('/channel/UC123')).toBeNull()
    expect(isBareScreen('/channel/UC123')).toBe(true)
  })

  it('says nothing about a screen that is not one of these', () => {
    // Null twice over for two different reasons, which is why callers ask
    // isBareScreen first.
    expect(bareTitle('/')).toBeNull()
  })
})
