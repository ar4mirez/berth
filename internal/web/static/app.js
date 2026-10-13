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
  if (parts[0] === 'hosts') return { view: 'hosts', org: '', tab: 'info', op: '' }
  if (parts[0] === 'operations') return { view: 'operations', org: '', tab: 'info', op: parts[1] || '' }
  if (parts[0] === 'orgs' && parts[1]) {
    const tab = TABS.some((t) => t.id === parts[2]) ? parts[2] : 'info'
    return { view: 'org', org: parts[1], tab, op: '' }
  }
  return { view: 'orgs', org: '', tab: 'info', op: '' }
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

// operations are the API's endpoints as its OpenAPI document has them: for each, where it is, what
// a caller needs for it, and its arguments.
function operations(doc) {
  const out = []
  for (const [path, methods] of Object.entries(doc.paths || {})) {
    for (const [method, o] of Object.entries(methods)) {
      if (!o['x-berth-about']) continue // the API's own: its description of itself, and the streams
      const fields = []
      for (const p of o.parameters || []) {
        fields.push({ name: p.name, where: p.in, type: (p.schema && p.schema.type) || 'string', required: !!p.required, help: p.description || '' })
      }
      const content = o.requestBody && o.requestBody.content && o.requestBody.content['application/json']
      const schema = (content && content.schema) || {}
      for (const [name, s] of Object.entries(schema.properties || {})) {
        fields.push({ name, where: 'body', type: s.type || 'string', required: (schema.required || []).includes(name), help: s.description || '', secret: !!s.writeOnly })
      }
      out.push({
        id: o.operationId, method: method.toUpperCase(), path, access: o['x-berth-access'] || 'read',
        command: o['x-berth-operation'] || '', about: o['x-berth-about'], fields,
      })
    }
  }
  return out.sort((a, b) => a.id.localeCompare(b.id))
}

// fieldValue is what a form field holds, as the API wants it: undefined when it holds nothing.
function fieldValue(f, raw) {
  if (f.type === 'boolean') return raw ? true : undefined
  const text = String(raw === undefined || raw === null ? '' : raw).trim()
  if (text === '') return undefined
  if (f.type === 'array') return text.split(/[\s,]+/).filter(Boolean)
  if (f.type === 'integer') return Number.isFinite(Number(text)) ? Number(text) : undefined
  return text
}

// What a terminal's keys send.
const TERMINAL_KEYS = { enter: '\r', esc: '\x1b', up: '\x1b[A', down: '\x1b[B' }
const MAX_TERMINAL = 200000

// screenText is what a terminal printed, as text to read: its escape sequences taken out, a line
// rewritten in place kept as its last writing, and what was drawn again and again shown once. It is
// not what a terminal would show, cell for cell; it is enough to read a link and a prompt.
function screenText(raw) {
  const plain = raw
    .replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g, '') // operating-system commands: titles, links
    // What moves the cursor, read as the text it leaves: forward is spaces, and a new position or
    // a line down is a new line. A screen drawn by placing each piece has its pieces kept apart.
    .replace(/\x1b\[(\d*)C/g, (_, n) => ' '.repeat(Math.min(Number(n) || 1, 200)))
    .replace(/\x1b\[[\d;]*[Hf]|\x1b\[\d*[BE]/g, '\n')
    .replace(/\x1b\[[0-9;?<=>]*[ -/]*[@-~]/g, '') // the other control sequences: colours, erasing
    .replace(/\x1b[@-Z\\-_]/g, '')
    .replace(/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/g, '')
  const out = []
  // A line may end in more than one carriage return: what runs inside a container passes through
  // its terminal and then the engine's, and each adds one.
  for (const line of plain.replace(/\r+\n/g, '\n').split('\n')) {
    // A line rewritten in place is its last writing. A carriage return with nothing after it
    // wrote nothing over the line: what was there still is.
    const writings = line.split('\r').map(tidy).filter((w) => w !== '')
    const text = writings.length ? writings[writings.length - 1] : ''
    const last = out[out.length - 1]
    if (text === last || (text === '' && last === '')) continue
    out.push(text)
  }
  return out.slice(-400).join('\n').trim()
}

// tidy is a line of a drawn screen as text: its frame taken off (a box's borders, a rule across
// a terminal two thousand columns wide), and the room it was padded with closed up.
function tidy(line) {
  return line
    .replace(/[─-╿▀-▟]+/g, ' ')
    .replace(/\s+$/, '')
    .replace(/^( {0,8}) +/, '$1')
    .replace(/(\S) {3,}/g, '$1  ')
}

// links are the https links a terminal was given, the newest first: the one to open is usually the
// last. A link may be written out, or be behind a word as a hyperlink the terminal would open.
function links(raw) {
  const behind = [...raw.matchAll(/\x1b\]8;[^;\x07\x1b]*;(https:\/\/[^\x07\x1b\s]+)/g)].map((m) => m[1])
  const written = screenText(raw).match(/https:\/\/[^\s"'<>)\]]+/g) || []
  return [...new Set([...written, ...behind].reverse())].slice(0, 3)
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
    server: { version: '', read_only: false, writes: false, restarts: false, admin: false },
    offline: '',

    route: parseRoute(''),
    tabs: TABS,
    orgs: null,
    hosts: null,
    operations: [],
    opFilter: '',
    opValues: {},
    opResult: '',
    opError: noError(),
    hostsError: '',
    detail: emptyDetail(),
    tabError: noError(),
    tabLoading: false,
    probes: [],
    forms: { allow: '', probe: '', repo: '', branch: '', packages: '', org: '', gitName: '', gitEmail: '', envName: '', envValue: '' },

    dialog: null,
    typed: '',
    job: null,
    toast: '',
    term: null,
    termInput: '',
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
      this.closeTerminal()
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
      if (next.op !== this.route.op) {
        this.opValues = {}
        this.opResult = ''
        this.opError = noError()
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
        if (route.view === 'operations') return this.loadOperations()
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

    // Whether the open org is running, once that is known: until the list is read, neither.
    get orgUp() {
      return this.orgState === 'up'
    },

    get orgDown() {
      return this.orgState === 'down'
    },

    // What needs the org running: Remote Control's service, and a sign-in, which run in it.
    get canRemote() {
      return this.server.writes && this.orgUp
    },

    get canSignIn() {
      return this.server.admin && this.orgUp
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

    // lifecycle starts, restarts or stops the org. What stops the work running in it runs only
    // after its name is typed, which is the `confirm` the API wants. Starting an org that is down
    // stops nothing: it asks, and the name is sent for it.
    lifecycle(kind) {
      const name = this.route.org
      const go = async () => {
        await this.run('berth ' + kind + ' ' + name, 'POST', org(name) + '/' + kind, { confirm: name })
        this.orgs = await this.read('/v1/orgs').catch(() => this.orgs)
      }
      if (kind === 'up' && this.orgDown) {
        this.ask('berth up ' + name, [LIFECYCLE.up, name + ' isn\'t running, so nothing is stopped. The first start can take a few minutes.'], '', go)
        return
      }
      this.ask('berth ' + kind + ' ' + name, [LIFECYCLE[kind], 'It stops the work running in ' + name + '.'], name, go)
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

    syncRepos() {
      const name = this.route.org
      this.run('berth repo sync ' + name, 'POST', org(name) + '/repos/sync', {})
    },

    // firewall turns the firewall on or off, or applies the allowlist again. Off asks first: until
    // it is on again, the org reaches anything.
    firewall(verb) {
      const name = this.route.org
      const go = async () => {
        const out = await this.run('berth fw ' + verb + ' ' + name, 'POST', org(name) + '/firewall/' + verb, {})
        if (out) this.detail.firewall = out.firewall
      }
      if (verb !== 'off') return go()
      this.ask('berth fw off ' + name, ['It turns ' + name + '\'s egress firewall off, at once: the org can reach anything until it is turned on again.'], '', go)
    },

    async addPackages() {
      const entries = this.forms.packages.split(/[\s,]+/).filter(Boolean)
      if (entries.length === 0) return
      const name = this.route.org
      const out = await this.run('berth pkg add ' + name + ' ' + entries.join(' '), 'POST', org(name) + '/packages', { entries })
      if (out) {
        this.forms.packages = ''
        this.detail.packages = out.packages
      }
    },

    removePackage(entry) {
      const name = this.route.org
      this.ask('berth pkg rm ' + name + ' ' + entry, ['It takes ' + entry + ' out of the packages ' + name + '\'s image adds. The org keeps it until its next restart.'], '', async () => {
        const out = await this.run('berth pkg rm ' + name + ' ' + entry, 'POST', org(name) + '/packages/remove', { entries: [entry] })
        if (out) this.detail.packages = out.packages
      })
    },

    // setVariable hands berth a variable's value, which may be a secret. It is sent once, in the
    // request's body, and dropped from the page as soon as it is: berth stores it for the org and
    // never gives it back. A running org is restarted to apply it, so the org's name is typed.
    setVariable() {
      const name = this.route.org
      const key = this.forms.envName.trim()
      const value = this.forms.envValue
      if (!key || !value) return
      this.forms.envValue = ''
      this.ask('berth env set ' + name + ' ' + key, ['It sets ' + key + ' for ' + name + '. The value is stored for the org and never shown again.', 'A running org is restarted to apply it, which stops the work running in it.'], name, async () => {
        const out = await this.run('berth env set ' + name + ' ' + key, 'PUT', org(name) + '/env/' + encodeURIComponent(key), { value, confirm: name })
        if (out) this.forms.envName = ''
      })
    },

    unsetVariable(key) {
      const name = this.route.org
      this.ask('berth env unset ' + name + ' ' + key, ['It removes ' + key + ' from ' + name + '\'s variables.', 'A running org is restarted to apply it, which stops the work running in it.'], name, () => {
        this.run('berth env unset ' + name + ' ' + key, 'DELETE', org(name) + '/env/' + encodeURIComponent(key), { confirm: name })
      })
    },

    restartRemote() {
      const name = this.route.org
      this.run('berth account remote restart ' + name, 'POST', org(name) + '/remote/restart', {})
    },

    async createOrg() {
      const name = this.forms.org.trim()
      if (!name) return
      const body = { org: name }
      if (this.forms.gitName.trim()) body.name = this.forms.gitName.trim()
      if (this.forms.gitEmail.trim()) body.email = this.forms.gitEmail.trim()
      const out = await this.run('berth org create ' + name, 'POST', '/v1/orgs', body)
      if (out) {
        this.forms.org = ''
        this.forms.gitName = ''
        this.forms.gitEmail = ''
        this.orgs = await this.read('/v1/orgs').catch(() => this.orgs)
        // To the new org's page, which says it isn't running yet and offers to start it.
        location.hash = '#/orgs/' + encodeURIComponent(name)
      }
    },

    // destroyOrg removes an org for good. The API wants its name again, as for a restart, and a
    // caller that may do everything.
    destroyOrg() {
      const name = this.route.org
      this.ask('berth org destroy ' + name, [
        'It permanently removes ' + name + ': its container, its workspace, Claude\'s config and history, its keys and secrets, and its backups on that machine.',
        'It can\'t be undone.',
      ], name, async () => {
        const out = await this.run('berth org destroy ' + name, 'DELETE', org(name), { confirm: name })
        if (out) {
          this.orgs = await this.read('/v1/orgs').catch(() => this.orgs)
          location.hash = '#/'
        }
      })
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

    // --- every operation, by name -----------------------------------------------------------
    //
    // The API describes itself (/v1/openapi.json): each operation's arguments, and what a caller
    // needs for it. This sheet is made from that, so an operation is here the day it has an
    // endpoint, with or without a screen of its own.

    async loadOperations() {
      if (this.operations.length) return
      try {
        this.operations = operations(await this.read('/v1/openapi.json'))
        this.opError = noError()
      } catch (e) {
        this.opError = { message: e.message, hint: e.hint || '' }
      }
    },

    get opList() {
      const q = this.opFilter.trim().toLowerCase()
      return this.operations.filter((o) => !q || o.id.includes(q) || o.command.includes(q) || o.about.toLowerCase().includes(q))
    },

    get op() {
      return this.operations.find((o) => o.id === this.route.op) || null
    },

    get opFields() {
      return this.op ? this.op.fields.filter((f) => f.name !== 'confirm') : []
    },

    get opTitle() {
      return this.op ? this.op.command || this.op.id : ''
    },

    get opAbout() {
      return this.op ? this.op.about : ''
    },

    get opNeeds() {
      return this.op ? this.op.access : ''
    },

    get opAllowed() {
      return this.op !== null && this.may(this.op.access)
    },

    may(access) {
      if (access === 'read') return true
      if (access === 'write') return this.server.writes
      if (access === 'restart') return this.server.restarts
      return access === 'admin' && this.server.admin
    },

    fieldType(f) {
      if (f.secret) return 'password'
      return f.type === 'integer' ? 'number' : 'text'
    },

    opHref(id) {
      return '#/operations/' + id
    },

    // runOperation sends what the form holds: each argument where the API takes it from. One that
    // restarts or removes an org asks for its name first, which is the `confirm` the API wants.
    runOperation() {
      const op = this.op
      if (!op || !this.opAllowed) return
      let path = op.path
      const query = []
      const body = {}
      for (const f of op.fields) {
        if (f.name === 'confirm') continue
        const v = fieldValue(f, this.opValues[f.name])
        if (v === undefined) {
          if (f.required) {
            this.opError = { message: f.name + ' is required', hint: f.help }
            return
          }
          continue
        }
        if (f.where === 'path') path = path.replace('{' + f.name + '}', encodeURIComponent(v))
        else if (f.where === 'query') query.push(encodeURIComponent(f.name) + '=' + encodeURIComponent(Array.isArray(v) ? v.join(',') : v))
        else body[f.name] = v
      }
      this.opError = noError()
      if (query.length) path += '?' + query.join('&')
      const label = 'berth ' + (op.command || op.id)
      const go = async () => {
        this.opResult = ''
        if (op.method === 'GET') {
          try {
            this.opResult = JSON.stringify(await this.read(path), null, 2)
          } catch (e) {
            this.opError = { message: e.message, hint: e.hint || '' }
          }
          return
        }
        // A secret is sent once and dropped from the form: it isn't kept in the page.
        for (const f of op.fields) if (f.secret) delete this.opValues[f.name]
        const out = await this.run(label, op.method, path, body)
        if (out) this.opResult = JSON.stringify(out, null, 2)
      }
      if (!op.fields.some((f) => f.name === 'confirm')) return go()
      // What is said again is the org, or what the operation is about when it has none: the
      // backup to restore.
      const about = op.fields.find((f) => f.name === 'org') || op.fields.find((f) => f.required && f.name !== 'confirm')
      const name = String((about && fieldValue(about, this.opValues[about.name])) || '')
      const stops = about && about.name === 'org' ? 'It stops the work running in ' + name + ', or removes it.' : 'It can stop an org, or replace one.'
      body.confirm = name
      this.ask(label + ' ' + name, [op.about, stops], name, go)
    },

    // --- signing an org in --------------------------------------------------------------------
    //
    // A sign-in needs a terminal: it shows a link, and waits for a code. The API runs berth's own
    // sign-in command on a terminal of its own, where the org is, and this is what it prints and a
    // place to type. It is not a terminal emulator: what a terminal would draw is read as text.

    get termOpen() {
      return this.term !== null
    },

    get termTitle() {
      return this.term ? this.term.title : ''
    },

    get termRunning() {
      return this.term !== null && this.term.running
    },

    get termText() {
      return this.term ? screenText(this.term.raw) : ''
    },

    get termLinks() {
      return this.term ? links(this.term.raw) : []
    },

    get termError() {
      return this.term ? this.term.error : ''
    },

    get termEnded() {
      if (!this.term || this.term.running || this.term.error) return ''
      return this.term.exit === 0 ? 'Done.' : ''
    },

    // A sign-in that ended badly says so as a failure: what it printed, above, is why.
    get termFailed() {
      if (!this.term || this.term.running || this.term.error || this.term.exit === 0 || this.term.exit === null) return ''
      return 'It didn\'t finish: the command ended with exit code ' + this.term.exit + '. What it printed, above, says why.'
    },

    async signInOrg(command) {
      const name = this.route.org
      const abort = new AbortController()
      const term = { title: 'berth account ' + command + ' ' + name, raw: '', id: '', running: true, exit: null, error: '', abort }
      this.term = term
      this.termInput = ''
      try {
        // As wide as a terminal goes, so a long link stays on one line.
        await request('POST', '/v1/terminal', { command, org: name, cols: 1000, rows: 50 }, {
          signal: abort.signal,
          progress: (event, data) => {
            if (this.term === null || this.term.abort !== abort) return
            if (event === 'session') this.term.id = data.id
            else if (event === 'exit') this.term.exit = data
            else if (event === 'output') {
              this.term.raw = (this.term.raw + data.data).slice(-MAX_TERMINAL)
              this.$nextTick(() => { const el = this.$refs.term; if (el) el.scrollTop = el.scrollHeight })
            }
          },
        })
      } catch (e) {
        if (e.name !== 'AbortError' && this.term !== null && this.term.abort === abort) this.term.error = e.hint ? e.message + ' ' + e.hint : e.message
      } finally {
        if (this.term !== null && this.term.abort === abort) {
          this.term.running = false
          this.refresh()
        }
      }
    },

    // termType sends what was typed. A terminal that has ended, or isn't there yet, takes nothing.
    async termType(data) {
      const term = this.term
      if (!term || !term.running || !term.id) return
      try {
        await request('POST', '/v1/terminal/' + term.id + '/input', { data })
      } catch (e) {
        if (this.term === term) term.error = e.message
      }
    },

    termSend() {
      const text = this.termInput
      this.termInput = ''
      this.termType(text + '\r')
    },

    termKey(key) {
      this.termType(TERMINAL_KEYS[key] || '')
    },

    closeTerminal() {
      if (this.term) this.term.abort.abort()
      this.term = null
      this.termInput = ''
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
