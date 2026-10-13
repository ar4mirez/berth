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
const openapi = JSON.parse(readFileSync(new URL('../../docs/api/openapi.json', import.meta.url), 'utf8'))

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
  let pkgs = ['jq']
  let terminal = null
  let firewall = { schema: 'berth.firewall/v1', org: 'acme', file: 'f', entries: ['mode on', '@python', 'pypi.org'], live: 'on 159' }
  w.TextDecoder = TextDecoder
  w.AbortController = AbortController
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
      case 'POST /v1/terminal': {
        // A terminal: its output as it comes, and more once something is typed.
        const enc = new TextEncoder()
        const ev = (e, d) => enc.encode(`event: ${e}\ndata: ${JSON.stringify(d)}\n\n`)
        return new Response(new ReadableStream({
          start(controller) {
            terminal = { controller, ev }
            controller.enqueue(ev('session', { id: 's1' }))
            controller.enqueue(ev('output', { data: '\x1b[32m!\x1b[0m First copy your one-time code: ABCD-1234\r\nOpen this URL to continue: https://github.com/login/device\r\n' }))
            controller.enqueue(ev('output', { data: 'Waiting.\rWaiting..\rWaiting...\r\n\x1b[1A\x1b[2KWaiting...\r\n' }))
          },
        }), { headers: { 'Content-Type': 'text/event-stream' } })
      }
      case 'POST /v1/terminal/s1/input':
        terminal.controller.enqueue(terminal.ev('output', { data: '\x1b[32m✓\x1b[0m Authentication complete.\r\n' }))
        terminal.controller.enqueue(terminal.ev('exit', 0))
        terminal.controller.close()
        return json({ ok: true })
      case 'GET /v1/openapi.json': return json(openapi)
      case 'POST /v1/backups/restore': return sse([['result', { output: 'Restored' }]])
      case 'GET /v1/orgs/acme/env': return json({ schema: 'berth.env/v1', org: 'acme', vars: [{ name: 'SENTRY_DSN', present: true }] })
      case 'PUT /v1/orgs/acme/env/OPENROUTER_API_KEY': return sse([['result', { org: 'acme', output: 'Set OPENROUTER_API_KEY for acme.' }]])
      case 'DELETE /v1/orgs/acme/env/SENTRY_DSN': return sse([['result', { org: 'acme', output: 'Removed' }]])
      case 'PUT /v1/orgs/acme/token': return sse([['result', { org: 'acme', output: 'Token saved for acme.' }]])
      case 'POST /v1/schedule': return sse([['result', { output: 'Scheduled', schedule: { kind: 'cron' } }]])
      case 'GET /v1/orgs/acme/logs?lines=5': return json({ org: 'acme', lines: ['ready'] })
      case 'POST /v1/orgs/acme/repos/policy': return sse([['result', { org: 'acme', output: 'REPO_POLICY=warn' }]])
      case 'GET /v1/hosts': return json({ schema: 'berth.hosts/v1', hosts: [{ name: 'local', kind: 'local', address: '', reachable: true, engine: 'docker', docker: '29.0.0', orgs: 1, error: '' }] })
      case 'GET /v1/orgs/acme': return json({ schema: 'berth.info/v1', org: 'acme', container: 'claude-acme', state: 'running', remote_url: 'https://claude.ai/code?environment=env_x', address: '100.64.0.7', local_only: false, ssh_port: 2201, ttyd_port: 7701, user: 'node', ssh_host: 'acme', git_public_key: 'ssh-ed25519 AAAA claude-acme', tunnel: null })
      case 'GET /v1/orgs/acme/firewall': return json(firewall)
      case 'POST /v1/orgs/acme/firewall/allow':
        firewall = { ...firewall, entries: [...firewall.entries, ...JSON.parse(init.body).entries] }
        return sse([['result', { output: 'Allowed files.example.com', firewall }]])
      case 'POST /v1/orgs/acme/firewall/off':
        firewall = { ...firewall, entries: firewall.entries.map((e) => (e === 'mode on' ? 'mode off' : e)) }
        return sse([['result', { output: '', firewall }]])
      case 'GET /v1/orgs/acme/repos': return json({ schema: 'berth.repos/v1', org: 'acme', repos: [{ dir: 'app', repo: 'github.com/acme/app', url: '', branch: 'main', state: 'cloned', changed: null }], unregistered: [] })
      case 'POST /v1/orgs/acme/repos/sync': return sse([['result', { org: 'acme', output: 'Cloned widget' }]])
      case 'GET /v1/orgs/acme/packages': return json({ schema: 'berth.packages/v1', org: 'acme', entries: pkgs, packages: pkgs, image: 'berth/claude-env-acme:x', built: true })
      case 'POST /v1/orgs/acme/packages':
        pkgs = [...pkgs, ...JSON.parse(init.body).entries]
        return sse([['result', { output: 'added', packages: { entries: pkgs, packages: pkgs, image: 'i', built: true } }]])
      case 'POST /v1/orgs': return sse([['result', { org: JSON.parse(init.body).org, output: 'Created' }]])
      case 'DELETE /v1/orgs/acme':
        if (JSON.parse(init.body).confirm !== 'acme') return sse([['failed', { schema: 'berth.error/v1', kind: 'refused', message: 'destroy needs confirm', hint: '' }]])
        return sse([['result', { org: 'acme', output: 'Destroyed acme.' }]])
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
  const t = await boot({ url: 'http://127.0.0.1:1234/#token=berth_abc', token: 'berth_abc', caller: { writes: true, restarts: true, admin: true } })
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
  // What is offered fits the org's state: one that is up isn't offered Start, and can be signed in.
  assert.equal(t.button('Start'), undefined, 'an org that is up is not offered Start')
  assert.ok(t.button('Stop') && t.button('Sign in to Claude') && t.button('Restart Remote Control'))
  assert.ok(!/isn.t running/.test(t.$$('p.banner').filter(t.shown).map((p) => p.textContent).join(' ')), 'and is not said to be down')

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
  // One that is down is offered Start and nothing that needs it running; starting it stops no
  // work, so it asks without the name typed, and sends the name for it.
  assert.match(t.$$('p.banner').filter(t.shown).map((p) => p.textContent).join(' '), /This org isn't running/)
  for (const text of ['Restart', 'Stop', 'Sign in to Claude', 'Sign in to GitHub', 'Restart Remote Control']) assert.equal(t.button(text), undefined, text + ' is not offered for an org that is down')
  await t.click(t.button('Start'))
  assert.match(t.$('#dialog-title').textContent, /berth up initech@box1/)
  assert.ok(!t.shown(t.$('#dialog-typed')), 'starting an org that is down needs no name typed')
  assert.ok(!t.button('Run it').disabled)
  await t.submit(t.button('Run it').closest('form'))
  assert.deepEqual(t.calls.find((c) => c.path === '/v1/orgs/initech%40box1/up').body, { confirm: 'initech@box1' })
  assert.match(t.w.document.body.textContent, /box1 is unreachable/)
  await t.click(t.button('Close'))

  // Turning the firewall off asks first; syncing repos and adding packages just run.
  await t.go('#/orgs/acme/firewall')
  await t.click(t.button('Turn off'))
  assert.ok(!t.calls.some((c) => c.path.endsWith('/firewall/off')), 'off asked before running')
  assert.match(t.$('#dialog-title').textContent, /berth fw off acme/)
  await t.submit(t.button('Run it').closest('form'))
  assert.ok(t.calls.some((c) => c.path === '/v1/orgs/acme/firewall/off' && c.method === 'POST'))
  await t.click(t.button('Close'))
  assert.ok(rows().includes('mode off'))
  await t.go('#/orgs/acme/repos')
  await t.click(t.button('Sync'))
  assert.match(t.$('.log.short').textContent, /Cloned widget/)
  await t.click(t.button('Close'))
  await t.go('#/orgs/acme/packages')
  t.type(t.$('#pkg-add'), 'postgresql-client @playwright-chromium')
  await t.submit(t.$('#pkg-add').closest('form'))
  assert.deepEqual(t.calls.find((c) => c.path === '/v1/orgs/acme/packages' && c.method === 'POST').body, { entries: ['postgresql-client', '@playwright-chromium'] })
  await t.click(t.button('Close'))
  assert.deepEqual(rows(), ['jq', 'postgresql-client', '@playwright-chromium'])

  // Creating an org sends what was typed, and no more.
  await t.go('#/')
  t.type(t.$('#org-new'), ' t-new ')
  await t.submit(t.$('#org-new').closest('form'))
  assert.deepEqual(t.calls.find((c) => c.path === '/v1/orgs' && c.method === 'POST').body, { org: 't-new' })
  await t.click(t.button('Close'))
  assert.equal(t.w.location.hash, '#/orgs/t-new', 'the new org\'s page opens, to start it from')

  // Destroying one needs its name typed, like a restart, and goes back to the list.
  await t.go('#/orgs/acme')
  await t.click(t.button('Destroy…'))
  assert.match(t.$('#dialog-title').textContent, /berth org destroy acme/)
  assert.ok(t.button('Run it').disabled)
  await t.submit(t.button('Run it').closest('form'))
  assert.ok(!t.calls.some((c) => c.method === 'DELETE'), 'nothing was destroyed without the name')
  t.type(t.$('#dialog-typed'), 'acme')
  await sleep()
  await t.submit(t.button('Run it').closest('form'))
  assert.deepEqual(t.calls.find((c) => c.method === 'DELETE' && c.path === '/v1/orgs/acme').body, { confirm: 'acme' })
  await t.click(t.button('Close'))
  assert.equal(t.w.location.hash, '#/')

  // Every operation, by name: made from the API's own document, so nothing is left out.
  await t.go('#/operations')
  const served = Object.values(openapi.paths).flatMap((m) => Object.values(m)).filter((o) => o['x-berth-about'])
  assert.ok(served.length >= 40)
  assert.equal(t.$$('ul.ops > li').length, served.length, 'every endpoint is listed')
  t.type(t.$('#op-filter'), 'schedule')
  await sleep()
  assert.deepEqual(t.$$('ul.ops > li strong').map((e) => e.textContent).sort(), ['schedule', 'schedule off', 'schedule run', 'schedule status'])
  // Arguments go where the API takes them from: the body, as their types.
  await t.go('#/operations/schedule_on')
  t.type(t.$('#op-at'), '02:30')
  t.type(t.$('#op-keep'), '7')
  await t.submit(t.$('form.fields'))
  assert.deepEqual(t.calls.find((c) => c.path === '/v1/schedule' && c.method === 'POST').body, { at: '02:30', keep: 7 })
  await t.click(t.button('Close'))
  // The path and the query.
  await t.go('#/operations/org_logs')
  await t.submit(t.$('form.fields'))
  assert.match(t.$('main:not(.signin) .error[role=alert]:not([style*="none"])').textContent, /org is required/)
  t.type(t.$('#op-org'), 'acme')
  t.type(t.$('#op-lines'), '5')
  await t.submit(t.$('form.fields'))
  assert.ok(t.calls.some((c) => c.method === 'GET' && c.path === '/v1/orgs/acme/logs?lines=5'))
  // One that restarts asks for the org's name, and sends it as confirm.
  await t.go('#/operations/repo_policy_set')
  assert.equal(t.$('#op-confirm'), null, 'confirm is not a field to fill')
  t.type(t.$('#op-org'), 'acme')
  t.type(t.$('#op-mode'), 'warn')
  await t.submit(t.$('form.fields'))
  assert.ok(!t.calls.some((c) => c.path.endsWith('/repos/policy')), 'it asked first')
  t.type(t.$('#dialog-typed'), 'acme')
  await sleep()
  await t.submit(t.button('Run it').closest('form'))
  assert.deepEqual(t.calls.find((c) => c.path === '/v1/orgs/acme/repos/policy').body, { mode: 'warn', confirm: 'acme' })
  await t.click(t.button('Close'))

  // One with no org says its first required argument again: the backup to restore.
  await t.go('#/operations/backup_restore')
  t.type(t.$('#op-file'), 'acme-20261006-023000.tar.zst.age')
  await t.submit(t.$('form.fields'))
  assert.ok(!t.calls.some((c) => c.path === '/v1/backups/restore'), 'it asked first')
  assert.match(t.$('#dialog-title').textContent, /berth restore acme-20261006-023000\.tar\.zst\.age/)
  t.type(t.$('#dialog-typed'), 'acme-20261006-023000.tar.zst.age')
  await sleep()
  await t.submit(t.button('Run it').closest('form'))
  assert.deepEqual(t.calls.find((c) => c.path === '/v1/backups/restore').body, { file: 'acme-20261006-023000.tar.zst.age', confirm: 'acme-20261006-023000.tar.zst.age' })
  await t.click(t.button('Close'))

  // A secret is typed unseen, sent once with the org's name, and not kept in the page.
  await t.go('#/orgs/acme/env')
  assert.equal(t.$('#env-value').type, 'password')
  t.type(t.$('#env-name'), 'OPENROUTER_API_KEY')
  t.type(t.$('#env-value'), 'sk-or-PAGE-SECRET')
  await t.submit(t.$('#env-name').closest('form'))
  assert.equal(t.$('#env-value').value, '', 'the value leaves the form as soon as it is submitted')
  assert.ok(!t.calls.some((c) => c.method === 'PUT'), 'it asked first')
  assert.ok(!t.w.document.body.textContent.includes('sk-or-PAGE-SECRET'), 'the confirmation doesn\'t show the value')
  t.type(t.$('#dialog-typed'), 'acme')
  await sleep()
  await t.submit(t.button('Run it').closest('form'))
  assert.deepEqual(t.calls.find((c) => c.method === 'PUT' && c.path === '/v1/orgs/acme/env/OPENROUTER_API_KEY').body, { value: 'sk-or-PAGE-SECRET', confirm: 'acme' })
  assert.ok(!t.w.document.documentElement.outerHTML.includes('sk-or-PAGE-SECRET'), 'the value is nowhere in the page afterwards')
  await t.click(t.button('Close'))
  await t.click(t.button('Remove'))
  t.type(t.$('#dialog-typed'), 'acme')
  await sleep()
  await t.submit(t.button('Run it').closest('form'))
  assert.deepEqual(t.calls.find((c) => c.method === 'DELETE' && c.path === '/v1/orgs/acme/env/SENTRY_DSN').body, { confirm: 'acme' })
  await t.click(t.button('Close'))
  // By name too: a secret argument is a password field, and is dropped once sent.
  await t.go('#/operations/token_set')
  assert.equal(t.$('#op-token').type, 'password')
  assert.equal(t.$('#op-org').type, 'text')
  t.type(t.$('#op-org'), 'acme')
  t.type(t.$('#op-token'), 'sk-ant-PAGE-TOKEN')
  await t.submit(t.$('form.fields'))
  t.type(t.$('#dialog-typed'), 'acme')
  await sleep()
  await t.submit(t.button('Run it').closest('form'))
  assert.deepEqual(t.calls.find((c) => c.path === '/v1/orgs/acme/token').body, { token: 'sk-ant-PAGE-TOKEN', confirm: 'acme' })
  await t.click(t.button('Close'))
  assert.ok(!t.w.document.documentElement.outerHTML.includes('sk-ant-PAGE-TOKEN'), 'the token is nowhere in the page afterwards')
  assert.equal(t.$('#op-token').value, '')

  // Signing an org in: what the command prints is read as text, its link is offered to open, and
  // a code typed here reaches it.
  await t.go('#/orgs/acme')
  await t.click(t.button('Sign in to GitHub'))
  assert.match(t.$('#term-title').textContent, /berth account gh acme/)
  assert.deepEqual(t.calls.find((c) => c.path === '/v1/terminal').body, { command: 'gh', org: 'acme', cols: 240, rows: 50 })
  const screen = () => t.$('pre[x-ref=term]').textContent
  assert.match(screen(), /First copy your one-time code: ABCD-1234/)
  assert.ok(!screen().includes('\x1b') && !screen().includes('[32m'), 'escape sequences are taken out')
  assert.equal((screen().match(/Waiting/g) || []).length, 1, 'a line drawn again is shown once')
  const link = t.$$('.dialog.wide ul.list a').map((a) => a.getAttribute('href'))
  assert.deepEqual(link, ['https://github.com/login/device'])
  t.type(t.$('#term-input'), 'ABCD-1234')
  await t.submit(t.$('#term-input').closest('form'))
  assert.deepEqual(t.calls.find((c) => c.path === '/v1/terminal/s1/input').body, { data: 'ABCD-1234\r' })
  assert.match(screen(), /Authentication complete/)
  assert.match(t.$('.dialog.wide .ok-text').textContent, /Done\./)
  assert.equal(t.button('Send'), undefined, 'once it has ended there is nothing to type into')
  await t.click(t.button('Close'))
  assert.ok(!t.shown(t.$('#term-title')))

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
  for (const text of ['Start', 'Restart', 'Stop', 'Deny', 'Allow', 'Turn on', 'Turn off', 'Reload']) assert.equal(t.button(text), undefined, text + ' is hidden')
  await t.go('#/orgs/acme')
  for (const text of ['Destroy…', 'Restart Remote Control', 'Sign in to Claude', 'Sign in to GitHub']) assert.equal(t.button(text), undefined, text + ' is hidden')
  await t.go('#/')
  assert.ok(!t.shown(t.$('#org-new')), 'creating an org is hidden')
  await t.go('#/orgs/acme/env')
  assert.ok(!t.shown(t.$('#env-value')), 'setting a variable is hidden')
  assert.equal(t.button('Remove'), undefined, 'removing one is hidden')
  await t.go('#/orgs/acme/firewall')
  assert.ok(t.button('Test'), 'testing reads')
  await t.go('#/operations/firewall_allow')
  assert.ok(t.button('Run').disabled, 'a caller that may only read can run no write by name either')
  await t.go('#/operations/org_logs')
  assert.ok(!t.button('Run').disabled, 'and can run a read')
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
