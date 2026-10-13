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
const refusals = [] // every answer of 400 or more, from any page
const browser = await chromium.launch()

// open is a page on a server, with everything that would mean the page is broken recorded: an
// error, a refused policy, a request to anywhere else.
async function open(server, options) {
  const context = await browser.newContext(options)
  const page = await context.newPage()
  // A refusal the API answers with is not the page breaking: the browser logs each as a failed
  // load, and refused() below holds them to the ones this state root has reason for.
  page.on('console', (m) => { if (m.type() === 'error' && !m.text().startsWith('Failed to load resource: the server responded with a status of 4')) problems.push('console: ' + m.text()) })
  page.on('response', (r) => { if (r.status() >= 400) refusals.push(r.status() + ' ' + r.request().method() + ' ' + new URL(r.url()).pathname) })
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
    // A tab the API refuses says why, with what to do about it: it isn't left empty.
    if (tab === 'repos') assert.match(await phone.locator('main:visible [role=alert]:visible').first().innerText(), /never started[\s\S]*berth up t-web/)
    if (tab === 'logs') assert.match(await phone.locator('main:visible [role=alert]:visible').first().innerText(), /not running/)
  }

  // An org that is down says so, and is offered what fits: Start, and nothing that needs it
  // running. Starting it stops no work, so it asks without its name typed.
  await phone.goto(server.origin + '/#/orgs/t-web/info')
  await phone.locator('p.banner', { hasText: "This org isn't running" }).waitFor({ state: 'visible' })
  for (const text of ['Restart', 'Stop', 'Sign in to Claude', 'Sign in to GitHub', 'Restart Remote Control']) {
    assert.ok(!(await button(phone, text).first().isVisible()), text + ' is not offered for an org that is down')
  }
  await button(phone, 'Start').click()
  await phone.locator('#dialog-title').waitFor({ state: 'visible' })
  await fits(phone, 'phone-confirm')
  assert.ok(!(await phone.locator('#dialog-typed').isVisible()), 'starting an org that is down needs no name typed')
  assert.ok(await button(phone, 'Run it').isEnabled())
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

  // Signing in: the terminal's sheet, with what the command printed. The page doesn't offer it for
  // an org that is down, so it is asked for directly: the command says the org isn't running and
  // ends, which is shown as the failure it is. The terminal, the stream and the sheet are checked.
  await phone.goto(server.origin + '/#/orgs/t-web/info')
  await phone.evaluate(() => { void window.Alpine.$data(document.querySelector('[x-data]')).signInOrg('gh') })
  await phone.locator('#term-title').waitFor({ state: 'visible' })
  await phone.locator('.dialog.wide .error', { hasText: "didn't finish" }).waitFor({ state: 'visible', timeout: 20000 })
  await fits(phone, 'phone-signin')
  assert.match(await phone.locator('pre[x-ref=term]').innerText(), /not running/)
  assert.ok(!(await phone.locator('.dialog.wide .ok-text').isVisible()), 'a sign-in that failed is not shown as done')
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

  // Creating an org, from the list: its page opens, saying it isn't running yet, with Start.
  await phone.goto(server.origin + '/#/')
  await phone.locator('#org-new').fill('t-two')
  await phone.locator('#org-new').press('Enter')
  await phone.locator('#job-title').waitFor({ state: 'visible' })
  await phone.waitForFunction(() => ![...document.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Close' && b.offsetParent).disabled)
  await button(phone, 'Close').click()
  assert.equal(new URL(phone.url()).hash, '#/orgs/t-two')
  await phone.locator('p.banner', { hasText: "This org isn't running" }).waitFor({ state: 'visible' })
  assert.ok(await button(phone, 'Start').isVisible())
  await fits(phone, 'phone-org-new')
  await phone.goto(server.origin + '/#/')
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

  // What the API refused, each for a reason this state root has, and each said in words by the
  // page: the log of an org that is down, and the repos of one that has never started (it has no
  // workspace to check them against yet).
  // Anything else answered with an error would be the page asking for something it shouldn't.
  const expected = ['409 GET /v1/orgs/t-web/logs', '409 GET /v1/orgs/t-web/repos']
  const unexpected = refusals.filter((r) => !expected.includes(r))
  assert.deepEqual(unexpected, [], 'the API refused nothing but what an org that never started has reason for')
  for (const e of expected) assert.ok(refusals.includes(e), e + ' was asked for, and refused')
  assert.deepEqual(problems, [], 'nothing went wrong on any screen')
  console.log('ok')
} finally {
  await browser.close()
  for (const s of servers) s.kill('SIGINT')
  rmSync(root, { recursive: true, force: true })
}
process.exit(problems.length ? 1 : 0)
