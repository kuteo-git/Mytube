import { chromium } from '@playwright/test'

/**
 * A broadcast's captions, end to end, against the running gateway.
 *
 * Red before the fix in three separate ways, which is why it is one script
 * rather than a unit test: the stream answer's `liveCaptions` was dropped at
 * the wire type, the CC list was built from files on disk, and the gear that
 * holds the subtitle rows is not drawn for a single-tier video at all.
 */
const id = process.argv[2] ?? 'GotlA1KKWoo'
const b = await chromium.launch()
const ctx = await b.newContext({ viewport: { width: 1280, height: 900 } })
await ctx.addInitScript(() => window.localStorage.setItem('yt-profile-id-v1', 'u_luc'))
const p = await ctx.newPage()

const fail = (m) => { console.log('FAIL:', m); process.exitCode = 1 }

await p.goto(`http://localhost:8180/watch/${id}`, { waitUntil: 'domcontentloaded' })
await p.waitForSelector('video', { timeout: 45000 })
await p.waitForTimeout(12000)

const gear = p.locator('button[aria-label="Settings"]')
if (!(await gear.count())) fail('no Settings button — the subtitle rows are unreachable')
else {
  await gear.first().click()
  await p.waitForTimeout(800)
  const en = p.locator('button', { hasText: /^EN$/ })
  if (!(await en.count())) fail('the gear opened without an EN subtitle row')
  else {
    await en.first().click()
    await p.waitForTimeout(9000)
    const state = await p.evaluate(() => {
      const v = [...document.querySelectorAll('video')].find((x) => x.readyState > 0)
      const t = [...(v?.textTracks ?? [])].find((x) => x.language === 'en')
      return {
        mode: t?.mode ?? null,
        cues: t?.cues?.length ?? 0,
        sample: t?.cues?.length ? [...t.cues].slice(-1)[0].text.replace(/\s+/g, ' ').slice(0, 70) : '',
      }
    })
    console.log('en track:', JSON.stringify(state))
    if (state.mode !== 'showing') fail(`track mode is ${state.mode}, not showing`)
    if (state.cues === 0) fail('no cues arrived')
  }
}
await p.screenshot({ path: 'e2e/captions.png' })
await b.close()
console.log(process.exitCode ? '=== RED' : '=== GREEN')
