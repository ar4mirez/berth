// berth's web UI (#167). It is a client of the API and nothing more: every view is a document
// /v1 returns, and every action one of its endpoints. What the page hides (a write, for a token
// that may only read) the server refuses anyway.

const TOKEN_KEY = 'berth.token'

// The token lives in this tab's sessionStorage: not in a cookie, so no other site can spend it,
// and it is gone when the tab closes.
const session = {
  get() {
    try { return sessionStorage.getItem(TOKEN_KEY) || '' } catch { return '' }
  },
  set(t) {
    try { t ? sessionStorage.setItem(TOKEN_KEY, t) : sessionStorage.removeItem(TOKEN_KEY) } catch { /* private mode */ }
  },
}

// An ApiError is a berth.error/v1 document, with the response's status.
class ApiError extends Error {
  constructor(status, doc) {
    super((doc && doc.message) || 'the request failed (' + status + ')')
    this.status = status
    this.kind = (doc && doc.kind) || ''
    this.hint = (doc && doc.hint) || ''
  }
}

// events reads a server-sent event stream, calling on(event, data) for each event.
async function events(res, on) {
  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  for (;;) {
    const { done, value } = await reader.read()
    if (done) return
    buf += decoder.decode(value, { stream: true })
    for (let end = buf.indexOf('\n\n'); end >= 0; end = buf.indexOf('\n\n')) {
      const block = buf.slice(0, end)
      buf = buf.slice(end + 2)
      let event = 'message'
      let data = ''
      for (const line of block.split('\n')) {
        if (line.startsWith('event: ')) event = line.slice(7)
        else if (line.startsWith('data: ')) data += line.slice(6)
      }
      if (!data) continue
      try { on(event, JSON.parse(data)) } catch { /* not JSON: not one of berth's */ }
    }
  }
}

// request asks the API. With opts.progress, a change reports its events as it runs, and the
// promise is its result document; a failure is an ApiError either way.
async function request(method, path, body, opts = {}) {
  const headers = {}
  const token = session.get()
  if (token) headers.Authorization = 'Bearer ' + token
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (opts.progress) headers.Accept = 'text/event-stream'
  let res
  try {
    res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal: opts.signal, cache: 'no-store' })
  } catch (e) {
    if (e && e.name === 'AbortError') throw e
    throw new ApiError(0, { message: 'berth is not answering', kind: 'network' })
  }
  const type = res.headers.get('Content-Type') || ''
  if (type.startsWith('text/event-stream')) {
    let result
    let failure
    await events(res, (event, data) => {
      if (event === 'result') result = data
      else if (event === 'failed') failure = data.error || data
      else opts.progress(event, data)
    })
    if (failure) throw new ApiError(res.status, failure)
    return result
  }
  let doc = null
  try { doc = await res.json() } catch { /* an empty or foreign answer */ }
  if (!res.ok) throw new ApiError(res.status, doc)
  return doc
}

const org = (name) => '/v1/orgs/' + encodeURIComponent(name)

const TABS = [
  { id: 'info', label: 'Info' },
  { id: 'firewall', label: 'Firewall' },
  { id: 'repos', label: 'Repos' },
  { id: 'env', label: 'Env' },
  { id: 'packages', label: 'Packages' },
  { id: 'backups', label: 'Backups' },
  { id: 'logs', label: 'Logs' },
]

// What each lifecycle operation does, as the catalog (internal/ops) says it.
const LIFECYCLE = {
  up: 'It gets the image if needed, then recreates the container.',
  restart: 'It recreates the container, applying pending changes.',
  down: 'It stops and removes the container.',
}

const REFRESH_MS = 5000
const MAX_LOG_LINES = 1000

function parseRoute(hash) {
  const parts = hash.replace(/^#\/?/, '').split('/').filter(Boolean).map(decodeURIComponent)
  if (parts[0] === 'hosts') return { view: 'hosts', org: '', tab: 'info' }
  if (parts[0] === 'orgs' && parts[1]) {
    const tab = TABS.some((t) => t.id === parts[2]) ? parts[2] : 'info'
    return { view: 'org', org: parts[1], tab }
  }
  return { view: 'orgs', org: '', tab: 'info' }
}

function size(n) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  for (; n >= 1024 && i < units.length - 1; i++) n /= 1024
  return (i === 0 ? n : n.toFixed(1)) + ' ' + units[i]
}

function when(rfc3339, stamp) {
  const d = new Date(rfc3339)
  return rfc3339 && !isNaN(d) ? d.toLocaleString() : stamp
}

const emptyDetail = () => ({ info: null, firewall: null, repos: null, env: null, packages: null, backups: null, logs: null })
const noError = () => ({ message: '', hint: '' })

function app() {
  return {
    screen: 'loading', // loading, signin, main
    connecting: false,
    token: '',
    tokenInput: '',
    signInError: '',
    server: { version: '', read_only: false, writes: false, restarts: false },
    offline: '',

    route: parseRoute(''),
    tabs: TABS,
    orgs: null,
    hosts: null,
    hostsError: '',
    detail: emptyDetail(),
    tabError: noError(),
    tabLoading: false,
    probes: [],
    forms: { allow: '', probe: '', repo: '', branch: '' },

    dialog: null,
    typed: '',
    job: null,
    toast: '',
    following: false,
    followAbort: null,
    logLines: [],

    // --- starting ---------------------------------------------------------------------------

    init() {
      // `berth ui` hands its token over in the fragment, which no server sees. Take it, and take it
      // out of the address bar and the history.
      const m = /^#token=([A-Za-z0-9_-]+)$/.exec(location.hash)
      if (m) {
        session.set(m[1])
        history.replaceState(null, '', location.pathname + '#/')
      }
      this.token = session.get()
      this.route = parseRoute(location.hash)
      window.addEventListener('hashchange', () => this.navigate())
      document.addEventListener('visibilitychange', () => { if (!document.hidden) this.refresh() })
      setInterval(() => this.tick(), REFRESH_MS)
      this.connect()
    },

    async connect() {
      this.connecting = true
      try {
        this.server = await request('GET', '/v1')
        this.offline = ''
        this.screen = 'main'
        this.refresh()
      } catch (e) {
        if (e.status === 401) {
          this.signInError = this.token ? 'berth doesn\'t know that token.' : ''
          this.forget()
        } else {
          this.signInError = e.message
          this.screen = 'signin'
        }
      } finally {
        this.connecting = false
      }
    },

    signIn() {
      const t = this.tokenInput.trim()
      if (!t) return
      session.set(t)
      this.token = t
      this.tokenInput = ''
      this.connect()
    },

    signOut() {
      this.signInError = ''
      this.forget()
    },

    forget() {
      this.stopFollowing()
      session.set('')
      this.token = ''
      this.orgs = null
      this.hosts = null
      this.detail = emptyDetail()
      this.screen = 'signin'
    },

    // --- reading ----------------------------------------------------------------------------

    navigate() {
      const next = parseRoute(location.hash)
      const sameOrg = next.view === 'org' && this.route.view === 'org' && next.org === this.route.org
      this.stopFollowing()
      if (!sameOrg) {
        this.detail = emptyDetail()
        this.probes = []
        this.logLines = []
      }
      this.tabError = noError()
      this.route = next
      this.refresh()
    },

    tick() {
      if (this.screen !== 'main' || document.hidden || this.dialog || this.job) return
      this.refresh()
    },

    // read asks for one document. A token that stopped working sends the page back to signing in;
    // any other failure is the caller's to show.
    async read(path) {
      try {
        const doc = await request('GET', path)
        this.offline = ''
        return doc
      } catch (e) {
        if (e.status === 401) {
          this.signInError = 'That token no longer works.'
          this.forget()
        } else if (e.status === 0) {
          this.offline = e.message
        }
        throw e
      }
    },

    async refresh() {
      if (this.screen !== 'main') return
      const route = this.route
      try {
        if (route.view === 'hosts') {
          try {
            this.hosts = await this.read('/v1/hosts')
            this.hostsError = ''
          } catch (e) {
            this.hostsError = e.message
          }
          return
        }
        if (route.view === 'orgs' || this.orgs === null) {
          this.orgs = await this.read('/v1/orgs')
        }
        if (route.view === 'org') await this.refreshTab(route)
      } catch { /* shown where it happened */ }
    },

    async refreshTab(route) {
      const name = route.org
      const paths = {
        info: org(name),
        firewall: org(name) + '/firewall',
        repos: org(name) + '/repos',
        env: org(name) + '/env',
        packages: org(name) + '/packages',
        backups: '/v1/backups?org=' + encodeURIComponent(name),
        logs: org(name) + '/logs?lines=200',
      }
      if (route.tab === 'logs' && this.following) return
      if (route.tab === 'backups' && !this.orgIsLocal) return
      this.tabLoading = this.detail[route.tab] === null
      try {
        const doc = await this.read(paths[route.tab])
        if (this.route !== route) return // the page moved on while it was read
        this.detail[route.tab] = doc
        if (route.tab === 'logs') this.logLines = doc.lines
        this.tabError = noError()
      } catch (e) {
        if (this.route === route) this.tabError = { message: e.message, hint: e.hint || '' }
      } finally {
        if (this.route === route) this.tabLoading = false
      }
    },

    // --- what the page shows ----------------------------------------------------------------

    get multiHost() {
      return this.orgList.some((o) => o.host !== 'local')
    },

    get orgList() {
      if (!this.orgs) return []
      return this.orgs.orgs.map((o) => {
        const addr = o.host && o.host !== 'local' ? o.name + '@' + o.host : o.name
        return {
          addr, name: o.name, host: o.host || 'local', state: o.state, remote: o.remote, token: o.token,
          stateClass: o.state === 'up' ? 'ok' : 'off',
          href: '#/orgs/' + encodeURIComponent(addr),
        }
      })
    },

    get unreachable() {
      return (this.orgs && this.orgs.unreachable) || []
    },

    get hostList() {
      return (this.hosts && this.hosts.hosts) || []
    },

    get current() {
      return this.orgList.find((o) => o.addr === this.route.org) || null
    },

    get orgState() {
      return this.current ? this.current.state : ''
    },

    get orgStateClass() {
      return this.current ? this.current.stateClass : 'off'
    },

    get orgIsLocal() {
      return !this.route.org.includes('@')
    },

    tabHref(id) {
      return '#/orgs/' + encodeURIComponent(this.route.org) + '/' + id
    },

    get infoRemote() {
      const u = this.detail.info ? this.detail.info.remote_url : ''
      return u && u.startsWith('https://') ? u : ''
    },

    get infoAddress() {
      return this.detail.info ? this.detail.info.address : ''
    },

    get infoLocalOnly() {
      return !!(this.detail.info && this.detail.info.local_only)
    },

    get infoSSH() {
      const i = this.detail.info
      if (!i || !i.address || !i.ssh_port) return ''
      return 'ssh -p ' + i.ssh_port + ' ' + i.user + '@' + i.address
    },

    get infoTerminal() {
      const i = this.detail.info
      if (!i || !i.address || !i.ttyd_port || i.state !== 'running') return ''
      const host = i.address.includes(':') ? '[' + i.address + ']' : i.address
      return 'http://' + host + ':' + i.ttyd_port + '/'
    },

    get infoTunnel() {
      const i = this.detail.info
      return i && i.tunnel ? 'berth org connect ' + i.tunnel.connect : ''
    },

    get fwLive() {
      const f = this.detail.firewall
      if (!f) return ''
      return f.live === null ? 'the org is down' : f.live
    },

    get fwEntries() {
      return this.detail.firewall ? this.detail.firewall.entries : []
    },

    get repoList() {
      return this.detail.repos ? this.detail.repos.repos : []
    },

    get unregistered() {
      return (this.detail.repos && this.detail.repos.unregistered) || []
    },

    get envVars() {
      return this.detail.env ? this.detail.env.vars : []
    },

    get pkgEntries() {
      return this.detail.packages ? this.detail.packages.entries : []
    },

    get backupList() {
      if (!this.detail.backups) return []
      return this.detail.backups.backups.map((b) => ({
        file: b.file, encryption: b.encryption, sizeText: size(b.size), when: when(b.modified, b.stamp),
      }))
    },

    get logText() {
      return this.logLines.join('\n')
    },

    // What x-show reads is true or false, never null one moment and '' the next: a change between
    // two values that both hide would start hiding again, and can land after a show.
    shows(tab) {
      return this.route.tab === tab && this.detail[tab] !== null
    },

    get infoKey() {
      return (this.detail.info && this.detail.info.git_public_key) || ''
    },

    get noBackups() {
      return this.orgIsLocal && this.detail.backups !== null && this.backupList.length === 0
    },

    get dialogOpen() {
      return this.dialog !== null
    },

    get dialogNeeds() {
      return this.dialog ? this.dialog.needs : ''
    },

    get jobOpen() {
      return this.job !== null
    },

    get jobRunning() {
      return this.job !== null && this.job.running
    },

    get jobError() {
      return (this.job && this.job.error) || noError()
    },

    get jobDone() {
      return this.job !== null && !this.job.running && !this.job.error
    },

    get dialogLines() {
      return this.dialog ? this.dialog.lines : []
    },

    get dialogReady() {
      return !!this.dialog && (!this.dialog.needs || this.typed === this.dialog.needs)
    },

    get jobText() {
      return this.job ? this.job.lines.join('\n') : ''
    },

    // --- doing ------------------------------------------------------------------------------

    // ask shows what an operation does, and runs it only when confirmed: with the org's name typed
    // when it restarts a container, as the API wants it again in `confirm`.
    ask(title, lines, needs, run) {
      this.typed = ''
      this.dialog = { title, lines, needs, run }
      if (needs) this.$nextTick(() => this.$refs.typed.focus())
    },

    closeDialog() {
      this.dialog = null
      this.typed = ''
    },

    confirmDialog() {
      if (!this.dialogReady) return
      const run = this.dialog.run
      this.closeDialog()
      run()
    },

    // run does one operation, showing its progress as it goes and what it printed when it ends.
    async run(label, method, path, body) {
      const job = { label, lines: [], running: true, error: null }
      this.job = job
      try {
        const out = await request(method, path, body, {
          progress: (event, e) => {
            if (event === 'step') this.job.lines.push('▸ ' + (e.message || e.step))
            else if (event === 'output') this.job.lines.push(e.message)
          },
        })
        if (out && out.output && this.job.lines.length === 0) this.job.lines = out.output.split('\n')
        return out
      } catch (e) {
        this.job.error = { message: e.message, hint: e.hint }
        if (e.status === 401) this.forget()
        return null
      } finally {
        this.job.running = false
        this.refresh()
      }
    },

    closeJob() {
      this.job = null
    },

    lifecycle(kind) {
      const name = this.route.org
      this.ask('berth ' + kind + ' ' + name, [LIFECYCLE[kind], kind === 'up' ? 'If ' + name + ' is running, it stops the work running in it.' : 'It stops the work running in ' + name + '.'], name, async () => {
        await this.run('berth ' + kind + ' ' + name, 'POST', org(name) + '/' + kind, { confirm: name })
        this.orgs = await this.read('/v1/orgs').catch(() => this.orgs)
      })
    },

    async allow() {
      const entries = this.forms.allow.split(/[\s,]+/).filter(Boolean)
      if (entries.length === 0) return
      const name = this.route.org
      const out = await this.run('berth fw allow ' + name + ' ' + entries.join(' '), 'POST', org(name) + '/firewall/allow', { entries })
      if (out) {
        this.forms.allow = ''
        this.detail.firewall = out.firewall
      }
    },

    deny(entry) {
      const name = this.route.org
      this.ask('berth fw deny ' + name + ' ' + entry, ['It removes ' + entry + ' from the allowlist. ' + name + ' can no longer reach it, at once.'], '', async () => {
        const out = await this.run('berth fw deny ' + name + ' ' + entry, 'POST', org(name) + '/firewall/deny', { entries: [entry] })
        if (out) this.detail.firewall = out.firewall
      })
    },

    async probe() {
      const hosts = this.forms.probe.split(/[\s,]+/).filter(Boolean)
      const name = this.route.org
      try {
        const out = await request('POST', org(name) + '/firewall/test', hosts.length ? { hosts } : {})
        this.probes = out.results
        this.tabError = noError()
      } catch (e) {
        this.tabError = { message: e.message, hint: e.hint }
      }
    },

    async addRepo() {
      const repo = this.forms.repo.trim()
      if (!repo) return
      const name = this.route.org
      const body = { repo }
      const branch = this.forms.branch.trim()
      if (branch) body.branch = branch
      const out = await this.run('berth repo add ' + name + ' ' + repo, 'POST', org(name) + '/repos', body)
      if (out) {
        this.forms.repo = ''
        this.forms.branch = ''
      }
    },

    removeRepo(dir) {
      const name = this.route.org
      this.ask('berth repo rm ' + name + ' ' + dir, ['It unregisters ' + dir + '. Its folder goes to the org\'s quarantine, not deleted: berth repo adopt brings it back.'], '', () => {
        this.run('berth repo rm ' + name + ' ' + dir, 'DELETE', org(name) + '/repos/' + encodeURIComponent(dir))
      })
    },

    backup() {
      const name = this.route.org
      this.run('berth backup create ' + name, 'POST', '/v1/backups', { orgs: [name] })
    },

    // --- the log, followed ------------------------------------------------------------------

    toggleFollow() {
      if (this.following) this.stopFollowing()
      else this.follow()
    },

    async follow() {
      const name = this.route.org
      const abort = new AbortController()
      this.followAbort = abort
      this.following = true
      this.logLines = []
      try {
        await request('GET', org(name) + '/logs/follow', undefined, {
          signal: abort.signal,
          progress: (event, e) => {
            if (event !== 'output') return
            this.logLines.push(e.message)
            if (this.logLines.length > MAX_LOG_LINES) this.logLines.splice(0, this.logLines.length - MAX_LOG_LINES)
            this.$nextTick(() => { const el = this.$refs.log; if (el) el.scrollTop = el.scrollHeight })
          },
        })
      } catch (e) {
        if (e.name !== 'AbortError' && this.followAbort === abort) this.tabError = { message: e.message, hint: e.hint || '' }
      } finally {
        if (this.followAbort === abort) {
          this.following = false
          this.followAbort = null
        }
      }
    },

    stopFollowing() {
      if (this.followAbort) this.followAbort.abort()
      this.followAbort = null
      this.following = false
    },

    // --- small things -----------------------------------------------------------------------

    async copy(text) {
      try {
        await navigator.clipboard.writeText(text)
        this.say('Copied.')
      } catch {
        this.say('Copying isn\'t allowed here: select the text instead.')
      }
    },

    say(text) {
      this.toast = text
      setTimeout(() => { if (this.toast === text) this.toast = '' }, 2500)
    },
  }
}

document.addEventListener('alpine:init', () => window.Alpine.data('app', app))
