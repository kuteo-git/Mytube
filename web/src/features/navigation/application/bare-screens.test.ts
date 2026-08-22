import { describe, expect, it } from 'vitest'
import { bareTitle, isBareScreen, isWatchScreen } from './bare-screens'

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

  /**
   * A list and the pages under it have to agree, and the whole chain moves
   * together.
   *
   * They used to be kept out of this list on the reasoning that a bare screen
   * drops the tab bar while a playlist's own page did not, so the parent would
   * lose the navigation while the child kept it. The observation was right and
   * the conclusion was not: the answer is to move the child too, rather than
   * hold the parent back.
   *
   * What settled it is where they live. All four sit in the Account group,
   * beside the profile and the YouTube connection, and two of four behaving
   * one way while two behave another is a rule nobody can see and everybody
   * has to learn.
   */
  it('treats the account lists and a list itself the same way', () => {
    for (const p of ['/watch-later', '/playlists', '/playlist/pl_1']) {
      expect(isBareScreen(p)).toBe(true)
    }
  })

  /**
   * Both were missing rather than excluded, and the panels are the proof.
   *
   * `ProfileSettings` and `YouTubeAccountSettings` render `headless` — they
   * deliberately draw no heading, because the shell was expected to name them
   * as it names their four /settings/* siblings. With no entry here the shell
   * named nothing, so on a phone both screens were untitled, under a search
   * bar that had nothing to do with them.
   */
  it('names the profile and the YouTube account, whose panels draw no heading', () => {
    expect(isBareScreen('/profile')).toBe(true)
    expect(isBareScreen('/account')).toBe(true)
    expect(bareTitle('/profile')).toBe('nav.profile')
    expect(bareTitle('/account')).toBe('nav.youtubeAccount')
  })

  /**
   * A playlist names itself, like a channel.
   *
   * Null here is not "not a bare screen" — `isBareScreen` says otherwise on
   * the same path. It means the shell draws no bar and the page draws its own,
   * because the name is the playlist's and a module constant cannot know it.
   */
  it('leaves a playlist to name itself', () => {
    expect(isBareScreen('/playlist/pl_1')).toBe(true)
    expect(bareTitle('/playlist/pl_1')).toBeNull()
  })

  // '/watch-later'.startsWith('/watch') is true, and that one character of
  // overlap turned the Watch later page into a playing video: no tab bar, and
  // on a phone a layer over the tab underneath with a pull-to-dismiss gesture.
  // Every route added under /watch-* from now on meets the same trap.
  it('does not mistake Watch later for the watch page', () => {
    expect(isWatchScreen('/watch/abc123')).toBe(true)
    expect(isWatchScreen('/watch')).toBe(true)
    expect(isWatchScreen('/watch-later')).toBe(false)
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
    // Keys, not words: this list is a module constant and cannot call a hook,
    // so the shell translates what it returns. Asserting on the key is
    // asserting on what this function actually promises.
    expect(bareTitle('/storage')).toBe('nav.storage')
    expect(bareTitle('/settings/narration')).toBe('phoneSettings.narration')
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
