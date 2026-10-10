// A run of the web UI's page (#167) in a DOM, against an API that answers from a table: what it
// asks the API for, what it shows, and what it won't do without being told twice.
//
// It is not a browser: nothing here checks the layout, or that the Content-Security-Policy lets the
// page run. internal/web's tests hold the files to that policy.
//
//   cd test/web && npm ci && node smoke.mjs
import { JSDOM, VirtualConsole } from 'jsdom'
import { readFileSync } from 'node:fs'
import assert from 'node:assert/strict'

const dir = new URL('../../internal/web/static/', import.meta.url).pathname
const html = readFileSync(dir + 'index.html', 'utf8').replace(/<script[^>]*><\/script>/g, '')
const appjs = readFileSync(dir + 'app.js', 'utf8')
const alpine = readFileSync(dir + 'vendor/alpine-csp.min.js', 'utf8')

const raw = (ms) => new Promise((r) => setTimeout(r, ms))
let settle = () => raw(300)
// sleep waits until the page has stopped changing.
const sleep = () => settle()
const sse = (events) => new Response(events.map(([e, d]) => `event: ${e}\ndata: ${JSON.stringify(d)}\n\n`).join(''), { headers: { 'Content-Type': 'text/event-stream' } })
const json = (d, status = 200) => new Response(JSON.stringify(d), { status, headers: { 'Content-Type': 'application/json' } })

async function boot({ url, caller, token }) {
  const problems = []
  const vc = new VirtualConsole()
  for (const k of ['error', 'warn', 'jsdomError']) vc.on(k, (...a) => problems.push(k + ': ' + a.map((x) => (x && x.stack) || String(x)).join(' ')))
  const dom = new JSDOM(html, { url, runScripts: 'outside-only', pretendToBeVisual: true, virtualConsole: vc })
  const w = dom.window
  const calls = []
  let firewall = { schema: 'berth.firewall/v1', org: 'acme', file: 'f', entries: ['mode on', '@python', 'pypi.org'], live: 'on 159' }
  w.TextDecoder = TextDecoder
  settle = async () => { let last = Date.now(); const mo = new w.MutationObserver(() => { last = Date.now() }); mo.observe(w.document, { subtree: true, childList: true, attributes: true, characterData: true }); await raw(700); while (Date.now() - last < 700) await raw(50); mo.disconnect() }
  w.fetch = async (path, init = {}) => {
    const auth = (init.headers && init.headers.Authorization) || ''
    calls.push({ method: init.method, path, auth, body: init.body ? JSON.parse(init.body) : undefined, accept: init.headers && init.headers.Accept })
    if (token && auth !== 'Bearer ' + token) return json({ schema: 'berth.error/v1', kind: 'refused', message: 'this API needs a token' }, 401)
    const key = init.method + ' ' + path
    switch (key) {
      case 'GET /v1': return json({ schema: 'berth.api/v1', version: 'test', read_only: false, ...caller })
      case 'GET /v1/orgs': return json({ schema: 'berth.orgs/v1', orgs: [
        { name: 'acme', state: 'up', remote: 'on', token: true, host: 'local', ssh_port: 2201, ttyd_port: 7701 },
        { name: 'initech', state: 'down', remote: '-', token: false, host: 'box1' }], unreachable: [{ host: 'box2', error: 'ssh: connection refused' }], destroyed: [] })
      case 'GET /v1/hosts': return json({ schema: 'berth.hosts/v1', hosts: [{ name: 'local', kind: 'local', address: '', reachable: true, engine: 'docker', docker: '29.0.0', orgs: 1, error: '' }] })
      case 'GET /v1/orgs/acme': return json({ schema: 'berth.info/v1', org: 'acme', container: 'claude-acme', state: 'running', remote_url: 'https://claude.ai/code?environment=env_x', address: '100.64.0.7', local_only: false, ssh_port: 2201, ttyd_port: 7701, user: 'node', ssh_host: 'acme', git_public_key: 'ssh-ed25519 AAAA claude-acme', tunnel: null })
      case 'GET /v1/orgs/acme/firewall': return json(firewall)
      case 'POST /v1/orgs/acme/firewall/allow':
        firewall = { ...firewall, entries: [...firewall.entries, ...JSON.parse(init.body).entries] }
        return sse([['result', { output: 'Allowed files.example.com', firewall }]])
      case 'POST /v1/orgs/acme/restart':
        if (JSON.parse(init.body).confirm !== 'acme') return sse([['failed', { schema: 'berth.error/v1', kind: 'refused', message: 'restart needs confirm', hint: '' }]])
        return sse([['start', { type: 'start' }], ['step', { type: 'step', step: 'container', message: 'recreating the container' }], ['output', { type: 'output', message: 'Container claude-acme Started' }], ['done', { type: 'done' }], ['result', { org: 'acme', output: 'x' }]])
      case 'POST /v1/orgs/initech%40box1/up':
        return sse([['failed', { type: 'failed', error: { schema: 'berth.error/v1', kind: 'command', message: 'box1 is unreachable', hint: 'check ssh' } }]])
      case 'GET /v1/orgs/initech%40box1': return json({ schema: 'berth.error/v1', kind: 'not-running', message: 'initech is not running', hint: 'run: berth up initech@box1' }, 409)
    }
    return json({ schema: 'berth.error/v1', kind: 'not-found', message: 'no ' + key }, 404)
  }
  w.eval(appjs)
  w.eval(alpine)
  await sleep(600)
  const $ = (s) => w.document.querySelector(s)
  const $$ = (s) => [...w.document.querySelectorAll(s)]
  const shown = (el) => { for (let e = el; e && e.style; e = e.parentElement) if (e.style.display === 'none') return false; return !!el }
  const go = async (hash) => { w.location.hash = hash; w.dispatchEvent(new w.HashChangeEvent('hashchange')); await sleep(400) }
  const type = (el, v) => { el.value = v; el.dispatchEvent(new w.Event('input', { bubbles: true })) }
  const submit = async (form) => { form.dispatchEvent(new w.Event('submit', { bubbles: true, cancelable: true })); await sleep(400) }
  const click = async (el) => { el.click(); await sleep(400) }
  const button = (text, scope = w.document) => [...scope.querySelectorAll('button')].find((b) => b.textContent.trim() === text && shown(b))
  return { w, calls, problems, $, $$, shown, go, type, submit, click, button }
}

// 1. `berth ui`: the token in the fragment, a caller that may do everything.
{
  const t = await boot({ url: 'http://127.0.0.1:1234/#token=berth_abc', token: 'berth_abc', caller: { writes: true, restarts: true } })
  assert.equal(t.w.location.hash, '#/', 'the token is taken out of the address')
  assert.equal(t.w.sessionStorage.getItem('berth.token'), 'berth_abc')
  assert.ok(t.calls.every((c) => c.auth === 'Bearer berth_abc'), 'every request carries the token')
  assert.ok(t.shown(t.$('header.top')), 'the main screen shows')
  assert.ok(!t.shown(t.$('main.signin')))
  const cards = t.$$('a.card')
  assert.equal(cards.length, 2)
  assert.equal(cards[1].getAttribute('href'), '#/orgs/initech%40box1')
  assert.match(t.w.document.body.textContent, /box2 is unreachable: ssh: connection refused/)

  await t.go('#/orgs/acme')
  assert.match(t.$('dl.sheet').textContent, /ssh -p 2201 node@100\.64\.0\.7/)
  assert.equal(t.$$('dl.sheet a').find((a) => t.shown(a) && /terminal/.test(a.textContent)).getAttribute('href'), 'http://100.64.0.7:7701/')
  assert.ok(t.button('Restart'), 'a caller that may restart sees Restart')

  await t.go('#/orgs/acme/firewall')
  const rows = () => t.$$('ul.list > li > code').filter(t.shown).map((c) => c.textContent)
  assert.deepEqual(rows(), ['mode on', '@python', 'pypi.org'])
  t.type(t.$('#fw-allow'), 'files.example.com  @go')
  await t.submit(t.$('#fw-allow').closest('form'))
  const allow = t.calls.find((c) => c.path.endsWith('/firewall/allow'))
  assert.deepEqual(allow.body, { entries: ['files.example.com', '@go'] })
  assert.equal(allow.accept, 'text/event-stream')
  assert.match(t.$('#job-title').textContent, /berth fw allow acme files\.example\.com @go/)
  await t.click(t.button('Close'))
  assert.deepEqual(rows(), ['mode on', '@python', 'pypi.org', 'files.example.com', '@go'])

  // A restart runs only with the org's name typed.
  await t.click(t.button('Restart'))
  assert.match(t.$('#dialog-title').textContent, /berth restart acme/)
  const run = t.button('Run it')
  assert.ok(run.disabled, 'Run it is off until the name is typed')
  await t.submit(run.closest('form'))
  assert.ok(!t.calls.some((c) => c.path.endsWith('/restart')), 'nothing ran without the name')
  t.type(t.$('#dialog-typed'), 'acm')
  await sleep(200)
  assert.ok(run.disabled)
  t.type(t.$('#dialog-typed'), 'acme')
  await sleep(200)
  assert.ok(!run.disabled)
  await t.submit(run.closest('form'))
  assert.deepEqual(t.calls.find((c) => c.path.endsWith('/restart')).body, { confirm: 'acme' })
  assert.match(t.$('.log.short').textContent, /▸ recreating the container\nContainer claude-acme Started/)
  await t.click(t.button('Close'))

  // A failure shows the error and its hint; an org on a host is asked for by its address.
  await t.go('#/orgs/initech%40box1')
  assert.match(t.w.document.body.textContent, /initech is not running/)
  assert.match(t.w.document.body.textContent, /run: berth up initech@box1/)
  await t.click(t.button('Start'))
  t.type(t.$('#dialog-typed'), 'initech@box1')
  await sleep(200)
  await t.submit(t.button('Run it').closest('form'))
  assert.deepEqual(t.calls.find((c) => c.path === '/v1/orgs/initech%40box1/up').body, { confirm: 'initech@box1' })
  assert.match(t.w.document.body.textContent, /box1 is unreachable/)
  await t.click(t.button('Close'))

  await t.go('#/hosts')
  assert.match(t.$$('li.card').filter(t.shown)[0].textContent, /local[\s\S]*reachable[\s\S]*docker 29\.0\.0/)
  assert.deepEqual(t.problems, [], 'nothing was logged')
  t.w.close()
}

// 2. A token that may only read: no control that changes anything.
{
  const t = await boot({ url: 'https://box:8443/', token: 'berth_ro', caller: { writes: false, restarts: false } })
  assert.ok(t.shown(t.$('main.signin')), 'without a token, the page asks for one')
  t.type(t.$('#token'), ' berth_ro ')
  await t.submit(t.$('main.signin form'))
  assert.ok(t.shown(t.$('header.top')))
  await t.go('#/orgs/acme/firewall')
  for (const text of ['Start', 'Restart', 'Stop', 'Deny', 'Allow']) assert.equal(t.button(text), undefined, text + ' is hidden')
  assert.ok(t.button('Test'), 'testing reads')
  await t.click(t.button('Sign out'))
  assert.ok(t.shown(t.$('main.signin')))
  assert.equal(t.w.sessionStorage.getItem('berth.token'), null)
  assert.deepEqual(t.problems, [])
  t.w.close()
}

// 3. A wrong token.
{
  const t = await boot({ url: 'https://box:8443/#token=berth_wrong', token: 'berth_right', caller: {} })
  assert.ok(t.shown(t.$('main.signin')))
  assert.match(t.$('main.signin .error').textContent, /doesn't know that token/)
  assert.equal(t.w.sessionStorage.getItem('berth.token'), null)
  assert.deepEqual(t.problems, [])
  t.w.close()
}
console.log('ok')
process.exit(0)
