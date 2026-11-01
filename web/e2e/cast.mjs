import { chromium } from '@playwright/test'

/**
 * Casting, end to end in a real browser against a running gateway, with the
 * cast routes answered here so no speaker in the house makes a sound.
 *
 *   node e2e/cast.mjs [videoId] [base]
 *
 * The video must be downloaded, so the player opens on the file and has a
 * position worth handing over. What it proves: the cast button is drawn when a
 * speaker answers; casting pauses the player and sends where it was; the remote
 * takes the screen and seeks and skips through the gateway's routes; "Stop
 * casting" closes it and the player carries on from the speaker's position.
 */
const id = process.argv[2] ?? 'eq6lKYNgs8Y'
const base = process.argv[3] ?? 'http://localhost:8180'
const handBackAt = 42

const b = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] })
const ctx = await b.newContext({ viewport: { width: 1280, height: 900 } })
await ctx.addInitScript(() => window.localStorage.setItem('yt-profile-id-v1', 'u_luc'))
const p = await ctx.newPage()

const fail = (m) => {
  console.log('FAIL:', m)
  process.exitCode = 1
}
const sent = []
let state = 'none'
const view = () => ({
  id: 's1',
  device: { id: 'r1-living', name: 'Loa R1', kind: 'r1', online: true },
  state,
  ended_reason: state === 'ended' ? 'handoff' : null,
  queue: [
    { video_id: id, title: 'First', channel: 'Channel', thumbnail_url: '', duration_s: 185 },
    { video_id: 'next1', title: 'Second', channel: 'Channel', thumbnail_url: '', duration_s: 200 },
  ],
  current_index: 0,
  position_s: 30,
  duration_s: 185,
  volume: 0,
  version: 1,
  started_by: 'u_luc',
  updated_at: new Date().toISOString(),
})

await p.route('**/api/cast/**', async (route) => {
  const req = route.request()
  const path = new URL(req.url()).pathname
  const body = req.postData() ? JSON.parse(req.postData()) : null
  sent.push({ method: req.method(), path, body })
  const json = (data) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(data) })
  if (path === '/api/cast/devices') return json([{ id: 'r1-living', name: 'Loa R1', kind: 'r1', model: 'R1', online: true }])
  if (path === '/api/cast/sessions' && req.method() === 'GET') return json(state === 'playing' ? [view()] : [])
  if (path === '/api/cast/sessions' && req.method() === 'POST') state = 'playing'
  if (path.endsWith('/handoff')) {
    state = 'ended'
    return json({ video_id: id, position_s: handBackAt })
  }
  return json(view())
})

await p.goto(`${base}/watch/${id}`, { waitUntil: 'domcontentloaded' })
await p.waitForSelector('video', { timeout: 45000 })
await p.waitForFunction(() => [...document.querySelectorAll('video')].some((v) => v.currentTime > 3 && !v.paused), null, {
  timeout: 30000,
})

const castButton = p.locator('button[aria-label="Cast to a speaker"]')
await p.locator('video').first().hover()
if (!(await castButton.count())) fail('no cast button although a speaker answered')
else {
  const before = await p.evaluate(() => [...document.querySelectorAll('video')].find((v) => !v.paused)?.currentTime ?? -1)
  await castButton.first().click()
  await p.locator('[role="dialog"][aria-label="Play on a speaker"] button', { hasText: 'Loa R1' }).click()
  await p.waitForSelector('[role="dialog"][aria-label="Playing on Loa R1"]', { timeout: 5000 })

  const start = sent.find((s) => s.method === 'POST' && s.path === '/api/cast/sessions')
  if (!start) fail('no session was started')
  else {
    console.log('start:', JSON.stringify(start.body))
    if (start.body.items?.[0]?.video_id !== id) fail('the video being watched was not the first item')
    if (Math.abs(start.body.start_position_s - before) > 2) fail(`start ${start.body.start_position_s}s, player was at ${before}s`)
  }
  const playing = await p.evaluate(() => [...document.querySelectorAll('video')].some((v) => !v.paused))
  if (playing) fail('the player kept playing under the remote')

  const bar = p.locator('input[aria-label="Position"]')
  await bar.focus()
  await bar.press('ArrowRight')
  await p.waitForTimeout(300)
  if (!sent.some((s) => s.path === '/api/cast/sessions/s1/seek')) fail('a seek on the remote reached nothing')

  await p.locator('button[aria-label="Next"]').click()
  await p.waitForTimeout(300)
  if (!sent.some((s) => s.path === '/api/cast/sessions/s1/next')) fail('next reached nothing')

  await p.screenshot({ path: 'e2e/cast-remote.png' })
  await p.locator('button', { hasText: 'Stop casting' }).click()
  await p.waitForSelector('[role="dialog"][aria-label="Playing on Loa R1"]', { state: 'detached', timeout: 5000 })
  await p.waitForTimeout(1500)
  const after = await p.evaluate(() => {
    const v = [...document.querySelectorAll('video')].find((x) => x.readyState > 0)
    return { at: v?.currentTime ?? -1, paused: v?.paused ?? true }
  })
  console.log('after handoff:', JSON.stringify(after))
  if (after.paused) fail('the player did not carry on after the handoff')
  if (Math.abs(after.at - handBackAt) > 3) fail(`player at ${after.at}s, the speaker handed back ${handBackAt}s`)
}
await p.screenshot({ path: 'e2e/cast-after.png' })
await b.close()
console.log(process.exitCode ? '=== RED' : '=== GREEN')
