// The web UI (#167) in a real browser, against a real `berth ui`: what a DOM can't check.
//
//   - the Content-Security-Policy lets the page run: no violation, nothing loaded from elsewhere;
//   - it is usable on a phone: at 360×640 no screen scrolls sideways, and every control is on it
//     and big enough for a finger;
//   - it works end to end: an operation's progress streams, a restart asks for the org's name, a
//     sign-in's terminal shows what the command printed.
//
// It needs a berth binary and Chromium. Each screen is saved under shots/, to look at.
//
//   cd test/web && npm ci && npx playwright install chromium && node browser.mjs /path/to/berth
import { chromium } from 'playwright'
import { spawn, execFileSync } from 'node:child_process'
import { mkdtempSync, mkdirSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import assert from 'node:assert/strict'

const berth = process.argv[2]
if (!berth) {
  console.error('usage: node browser.mjs /path/to/berth')
  process.exit(2)
}

const shots = new URL('./shots/', import.meta.url).pathname
rmSync(shots, { recursive: true, force: true })
mkdirSync(shots, { recursive: true })

// A state root and a home of its own: nothing of the machine's is read or touched.
const root = mkdtempSync(join(tmpdir(), 'berth-web-'))
const env = { ...process.env, HOME: join(root, 'home'), BERTH_HOME: join(root, 'state'), XDG_CONFIG_HOME: '', XDG_RUNTIME_DIR: '' }
mkdirSync(env.HOME, { recursive: true })
execFileSync(berth, ['org', 'create', 't-web', '--name', 'Ada Lovelace', '--email', 'ada@example.com'], { env, stdio: 'pipe' })

const servers = []
// serve starts `berth ui` and gives its link: the address, and this run's token.
function serve(...flags) {
  const child = spawn(berth, [...flags, 'ui', '--no-open'], { env, stdio: ['ignore', 'pipe', 'inherit'] })
  servers.push(child)
  return new Promise((resolve, reject) => {
    let out = ''
    child.stdout.on('data', (b) => {
      out += b
      const m = /^(http:\/\/127\.0\.0\.1:\d+)\/#token=\S+$/m.exec(out)
      if (m) resolve({ link: m[0], origin: m[1] })
    })
    child.on('exit', (code) => reject(new Error('berth ui ended with ' + code + ': ' + out)))
    setTimeout(() => reject(new Error('berth ui printed no link: ' + out)), 20000)
  })
}

const problems = []
const browser = await chromium.launch()

// open is a page on a server, with everything that would mean the page is broken recorded: an
// error, a refused policy, a request to anywhere else.
async function open(server, options) {
  const context = await browser.newContext(options)
  const page = await context.newPage()
  page.on('console', (m) => { if (m.type() === 'error') problems.push('console: ' + m.text()) })
  page.on('pageerror', (e) => problems.push('page error: ' + e.message))
  page.on('request', (r) => { if (!r.url().startsWith(server.origin + '/')) problems.push('a request to elsewhere: ' + r.url()) })
  await page.addInitScript(() => {
    window.__violations = []
    document.addEventListener('securitypolicyviolation', (e) => window.__violations.push(e.violatedDirective + ' ' + e.blockedURI))
  })
  await page.goto(server.link)
  await page.locator('header.top').waitFor({ state: 'visible', timeout: 15000 })
  return page
}

const settle = (page) => page.waitForTimeout(700)

// fits holds a screen to a phone: it doesn't scroll sideways, and each control that shows is on
// the screen and big enough to touch. The org's tabs scroll sideways on purpose, inside their bar.
async function fits(page, name) {
  await settle(page)
  await page.screenshot({ path: join(shots, name + '.png'), fullPage: true })
  const report = await page.evaluate(() => {
    const out = []
    const de = document.documentElement
    if (de.scrollWidth > de.clientWidth + 1) out.push('the page scrolls sideways: ' + de.scrollWidth + ' wide in ' + de.clientWidth)
    for (const el of document.querySelectorAll('button, input, select, .tabs a, .top nav a, a.card')) {
      const r = el.getBoundingClientRect()
      const shown = r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== 'hidden'
      if (!shown) continue
      const what = el.tagName.toLowerCase() + ' "' + (el.textContent || el.getAttribute('aria-label') || el.id || '').trim().slice(0, 30) + '"'
      if (!el.closest('.tabs') && (r.left < -1 || r.right > innerWidth + 1)) out.push(what + ' is off the screen: ' + Math.round(r.left) + ' to ' + Math.round(r.right))
      if (r.height < 40) out.push(what + ' is ' + Math.round(r.height) + 'px high: too small to touch')
    }
    return out.concat(window.__violations.map((v) => 'the policy refused: ' + v))
  })
  for (const r of report) problems.push(name + ': ' + r)
}

const button = (page, text) => page.getByRole('button', { name: text, exact: true })

try {
  const server = await serve()

  // --- a phone ---------------------------------------------------------------------------------
  const phone = await open(server, { viewport: { width: 360, height: 640 }, isMobile: true, hasTouch: true })
  assert.equal(new URL(phone.url()).hash, '#/', 'the token is taken out of the address')
  await phone.locator('a.card', { hasText: 't-web' }).waitFor()
  await fits(phone, 'phone-orgs')

  await phone.locator('a.card', { hasText: 't-web' }).click()
  await phone.locator('dl.sheet').first().waitFor()
  for (const tab of ['info', 'firewall', 'repos', 'env', 'packages', 'backups', 'logs']) {
    await phone.goto(server.origin + '/#/orgs/t-web/' + tab)
    await fits(phone, 'phone-org-' + tab)
  }

  // A restart asks for the org's name, and runs nothing without it.
  await phone.goto(server.origin + '/#/orgs/t-web/info')
  await button(phone, 'Restart').click()
  await phone.locator('#dialog-title').waitFor({ state: 'visible' })
  await fits(phone, 'phone-confirm')
  assert.ok(await button(phone, 'Run it').isDisabled(), 'Run it is off until the name is typed')
  await phone.locator('#dialog-typed').fill('t-we')
  assert.ok(await button(phone, 'Run it').isDisabled())
  await button(phone, 'Cancel').click()

  // An operation runs, and what it printed shows: the firewall of an org that is down is saved.
  await phone.goto(server.origin + '/#/orgs/t-web/firewall')
  await phone.locator('#fw-allow').fill('files.example.com')
  await phone.locator('#fw-allow').press('Enter')
  await phone.locator('#job-title').waitFor({ state: 'visible' })
  await button(phone, 'Close').waitFor({ state: 'visible' })
  await phone.waitForFunction(() => ![...document.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Close' && b.offsetParent).disabled)
  await fits(phone, 'phone-job')
  assert.match(await phone.locator('.dialog pre.log').first().innerText(), /files\.example\.com/)
  await button(phone, 'Close').click()
  await phone.locator('ul.list code', { hasText: 'files.example.com' }).waitFor()

  // A secret is typed into a password field, and is nowhere in the page once sent.
  await phone.goto(server.origin + '/#/orgs/t-web/env')
  await phone.locator('#env-name').fill('T_WEB_KEY')
  await phone.locator('#env-value').fill('sk-web-BROWSER-SECRET')
  assert.equal(await phone.locator('#env-value').getAttribute('type'), 'password')
  await phone.locator('#env-value').press('Enter')
  await phone.locator('#dialog-typed').fill('t-web')
  await button(phone, 'Run it').click()
  await phone.locator('#job-title').waitFor({ state: 'visible' })
  await phone.waitForFunction(() => ![...document.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Close' && b.offsetParent).disabled)
  await fits(phone, 'phone-env-set')
  assert.ok(!(await phone.content()).includes('sk-web-BROWSER-SECRET'), 'the secret is nowhere in the page')
  await button(phone, 'Close').click()
  await phone.locator('ul.list code', { hasText: 'T_WEB_KEY' }).waitFor()

  // Signing in: the terminal's sheet, with what the command printed. This org is down, so the
  // command says so and ends: the terminal, the stream and the sheet are what is checked.
  await phone.goto(server.origin + '/#/orgs/t-web/info')
  await button(phone, 'Sign in to GitHub').click()
  await phone.locator('#term-title').waitFor({ state: 'visible' })
  await phone.waitForFunction(() => document.querySelector('pre[x-ref=term]').textContent.includes('t-web'), null, { timeout: 20000 })
  await fits(phone, 'phone-signin')
  assert.match(await phone.locator('pre[x-ref=term]').innerText(), /not running/)
  await button(phone, 'Close').click()

  // The rest: hosts, every operation by name, one of them run.
  await phone.goto(server.origin + '/#/hosts')
  await phone.locator('li.card').first().waitFor()
  await fits(phone, 'phone-hosts')
  await phone.goto(server.origin + '/#/operations')
  await phone.locator('ul.ops > li').first().waitFor()
  await fits(phone, 'phone-operations')
  assert.ok((await phone.locator('ul.ops > li').count()) >= 60, 'every operation is listed')
  await phone.goto(server.origin + '/#/operations/env_list')
  await phone.locator('#op-org').fill('t-web')
  await button(phone, 'Run').click()
  await phone.locator('main pre.log', { hasText: 'T_WEB_KEY' }).waitFor()
  await fits(phone, 'phone-operation')

  // Creating an org, from the list.
  await phone.goto(server.origin + '/#/')
  await phone.locator('#org-new').fill('t-two')
  await phone.locator('#org-new').press('Enter')
  await phone.locator('#job-title').waitFor({ state: 'visible' })
  await phone.waitForFunction(() => ![...document.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Close' && b.offsetParent).disabled)
  await button(phone, 'Close').click()
  await phone.locator('a.card', { hasText: 't-two' }).waitFor()
  await fits(phone, 'phone-orgs-two')
  const light = await phone.evaluate(() => getComputedStyle(document.body).backgroundColor)
  await phone.context().close()

  // --- a desk, and the dark ----------------------------------------------------------------------
  const desk = await open(server, { viewport: { width: 1280, height: 800 }, colorScheme: 'dark' })
  await desk.locator('a.card', { hasText: 't-web' }).waitFor()
  await fits(desk, 'desk-orgs-dark')
  const dark = await desk.evaluate(() => getComputedStyle(document.body).backgroundColor)
  assert.notEqual(dark, light, 'the page follows the system into the dark')
  await desk.goto(server.origin + '/#/orgs/t-web/info')
  await fits(desk, 'desk-org-dark')
  await desk.context().close()

  // --- read-only -------------------------------------------------------------------------------
  const ro = await serve('--read-only')
  const reader = await open(ro, { viewport: { width: 360, height: 640 }, isMobile: true, hasTouch: true })
  await reader.locator('a.card', { hasText: 't-web' }).waitFor()
  await fits(reader, 'phone-read-only')
  assert.ok(await reader.locator('.top .pill', { hasText: 'read-only' }).isVisible())
  assert.ok(!(await reader.locator('#org-new').isVisible()), 'creating an org is hidden')
  await reader.goto(ro.origin + '/#/orgs/t-web/info')
  await settle(reader)
  for (const text of ['Start', 'Restart', 'Stop', 'Sign in to Claude', 'Destroy…']) {
    const b = button(reader, text)
    assert.ok((await b.count()) === 0 || !(await b.first().isVisible()), text + ' is hidden')
  }
  await reader.context().close()

  assert.deepEqual(problems, [], 'nothing went wrong on any screen')
  console.log('ok')
} finally {
  await browser.close()
  for (const s of servers) s.kill('SIGINT')
  rmSync(root, { recursive: true, force: true })
}
process.exit(problems.length ? 1 : 0)
