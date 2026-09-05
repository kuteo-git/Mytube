import { chromium } from '@playwright/test'

const id = process.argv[2] ?? 'GotlA1KKWoo'
const b = await chromium.launch()
const ctx = await b.newContext({ viewport: { width: 1280, height: 900 } })
// The household gate writes this when somebody is chosen. Seeded rather than
// clicked so the run starts on the page under test.
await ctx.addInitScript(() => window.localStorage.setItem('yt-profile-id-v1', 'u_luc'))
const p = await ctx.newPage()
await p.goto(`http://localhost:8180/watch/${id}`, { waitUntil: 'domcontentloaded' })
await p.waitForSelector('video', { timeout: 45000 })
await p.waitForTimeout(15000)

console.log(JSON.stringify(await p.evaluate(() => {
  const vids = [...document.querySelectorAll('video')]
  const v = vids.find((x) => x.readyState > 0) ?? vids[0]
  return {
    videos: vids.length,
    readyState: v?.readyState,
    currentSrc: (v?.currentSrc || '').slice(0, 45),
    textTracks: v ? [...v.textTracks].map((t) => ({ lang: t.language, label: t.label, mode: t.mode, cues: t.cues?.length ?? null })) : [],
    buttons: [...document.querySelectorAll('button[aria-label]')].map((x) => x.getAttribute('aria-label')),
  }
}), null, 1))
await p.screenshot({ path: 'e2e/page.png' })
await b.close()
