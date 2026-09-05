import { chromium } from '@playwright/test'
const b = await chromium.launch()
const p = await b.newPage({ viewport: { width: 1280, height: 900 } })
await p.goto('http://localhost:8180/', { waitUntil: 'domcontentloaded' })
await p.waitForTimeout(2500)
console.log('clickables:', JSON.stringify(await p.evaluate(() =>
  [...document.querySelectorAll('button,a,[role=button]')].map((e) => ({
    tag: e.tagName, label: e.getAttribute('aria-label'), text: (e.innerText||'').trim().slice(0,30),
  })).slice(0, 12)), null, 1))
const btn = p.locator('button', { hasText: 'KuTeo' })
console.log('buttons matching KuTeo:', await btn.count())
if (await btn.count()) {
  await btn.first().click()
  await p.waitForTimeout(3000)
  console.log('after click, url:', p.url())
  console.log('storage:', JSON.stringify(await p.evaluate(() => ({ ...localStorage }))).slice(0,300))
}
await b.close()
