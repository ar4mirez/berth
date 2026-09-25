#!/usr/bin/env node
// PreToolUse hook (managed settings): keeps Claude inside the org's allowed repos.
// Allowed = directories registered in /config/repos.txt (read-only here; managed with `ccenv repo`).
//   - no cloning / adding remotes / tampering with git's transport (repos are set up via ccenv)
//   - no reading or editing inside /workspace folders that aren't registered repos
// REPO_POLICY: enforce (default) | warn | off
'use strict';
const fs = require('fs');
const path = require('path');

const WS = '/workspace';
const REPOS = '/config/repos.txt';
const SECRET_DIRS = ['/opt/claude-secrets', '/config/secrets'];
const mode = (process.env.REPO_POLICY || 'enforce').toLowerCase();
const org = (() => { try { return fs.readFileSync('/etc/claude-env/org', 'utf8').trim(); } catch { return '<org>'; } })();

// Registered repos. canon is '' for a URL with no canonical form: the dir stays registered, but it
// allows no repo. Fields are split on spaces and tabs only, and a trailing CR is ignored (as in
// repo-policy.sh's repo_entries).
function entries(file = REPOS) {
  let text = '';
  try { text = fs.readFileSync(file, 'utf8'); } catch { return []; }
  return text.split('\n').map(l => l.replace(/\r$/, '').replace(/^[ \t]+|[ \t]+$/g, '')).filter(l => l && !l.startsWith('#'))
    .map(l => l.split(/[ \t]+/)).filter(p => p.length >= 2)
    .map(([dir, url]) => ({ dir, canon: url === 'local' ? `local/${dir}` : canon(url) }));
}

// canon(ref, host) -> 'host/path', or '' if ref isn't an acceptable repo. The rules are in
// docs/repo-policy.md, and testdata/canon.tsv pins them for this, repo-policy.sh and berth.
const DEFAULT_PORT = new Map([['ssh', 22], ['git+ssh', 22], ['ssh+git', 22], ['https', 443], ['http', 80]]);
const HOST_RE = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/;   // at least one dot
const SEG_RE = /^[a-z0-9._~-]+$/;
const lowerASCII = (s) => s.replace(/[A-Z]/g, (c) => String.fromCharCode(c.charCodeAt(0) + 32));

function canon(ref, hostArg = '') {
  if (typeof ref !== 'string' || !/^[!-~]+$/.test(ref) || !/^[!-~]*$/.test(hostArg)) return '';
  const u = lowerASCII(ref);
  let host = lowerASCII(hostArg), path;
  const sep = u.indexOf('://');
  if (sep >= 0) {                                               // URL
    const def = DEFAULT_PORT.get(u.slice(0, sep));
    if (def === undefined) return '';
    const rest = u.slice(sep + 3), slash = rest.indexOf('/');
    if (slash < 0) return '';
    let auth = rest.slice(0, slash);
    path = rest.slice(slash + 1);
    auth = auth.slice(auth.lastIndexOf('@') + 1);
    const colon = auth.lastIndexOf(':');
    if (colon >= 0) {
      const port = auth.slice(colon + 1);
      auth = auth.slice(0, colon);
      if (!/^[0-9]{1,5}$/.test(port) || Number(port) !== def) return '';
    }
    host = auth;
    if (!host) return '';
  } else if (u.includes(':') && !u.slice(0, u.indexOf(':')).includes('/')) {  // scp-like [user@]host:path
    const auth = u.slice(0, u.indexOf(':'));
    path = u.slice(u.indexOf(':') + 1);
    host = auth.slice(auth.lastIndexOf('@') + 1);
    if (!host) return '';
  } else {                                                      // bare path
    path = u;
  }
  const segs = path.split('/').filter(Boolean);
  if (!host) host = segs.length >= 2 && segs[0].includes('.') ? segs.shift() : 'github.com';
  if (segs.length === 0) return '';
  let last = segs[segs.length - 1];
  while (last.endsWith('.git') && last.length > 4) last = last.slice(0, -4);
  segs[segs.length - 1] = last;
  if (!segs.every((s) => SEG_RE.test(s) && s !== '.' && s !== '..') || !HOST_RE.test(host)) return '';
  return `${host}/${segs.join('/')}`;
}

function decide(reason) {
  if (mode === 'warn') {
    process.stdout.write(JSON.stringify({ systemMessage: `repo policy (warn): ${reason}` }));
  } else {
    process.stdout.write(JSON.stringify({ hookSpecificOutput: {
      hookEventName: 'PreToolUse', permissionDecision: 'deny', permissionDecisionReason: reason } }));
  }
  process.exit(0);
}

const HOW = (x) => `Only repos set up with ccenv can be used in this container ('${org}'). ` +
  `Don't work around this: tell the user, and they can run on the host: ccenv repo add ${org} ${x}`;

function checkPath(p, cwd, allowedDirs) {
  if (!p || typeof p !== 'string') return;
  const home = process.env.HOME || '/home/node';
  const abs = path.resolve(cwd, p.replace(/^~(?=\/|$)/, home));
  for (const s of SECRET_DIRS) if (abs === s || abs.startsWith(s + '/')) decide(`${abs} holds credentials and is off-limits.`);
  if (abs === WS || !abs.startsWith(WS + '/')) return;
  const top = abs.slice(WS.length + 1).split('/')[0];
  if (top === '.claude' || /[*?[{]/.test(top) || allowedDirs.has(top)) return;
  decide(`${WS}/${top} is not an allowed repo for this org. ${HOW('<owner/repo>')}`);
}

function main(input) {
  if (mode === 'off') return;
  const ev = JSON.parse(input || '{}');
  const cwd = ev.cwd || process.cwd();
  const ti = ev.tool_input || {};
  const list = entries();
  const allowedDirs = new Set(list.map(e => e.dir));
  const allowedCanon = new Set(list.map(e => e.canon).filter(Boolean));   // '' never allows anything

  checkPath(cwd, '/', allowedDirs);                                  // where the tool runs
  for (const k of ['file_path', 'path', 'notebook_path']) checkPath(ti[k], cwd, allowedDirs);
  if (ev.tool_name === 'Glob' && ti.pattern && ti.pattern.startsWith('/')) checkPath(ti.pattern, cwd, allowedDirs);

  if (ev.tool_name !== 'Bash' || typeof ti.command !== 'string') return;
  const cmd = ti.command;

  if (/\bgit\b[^;&|\n]*\bclone\b/.test(cmd) || /\bgh\s+repo\s+(clone|fork)\b/.test(cmd) ||
      /\bgit\b[^;&|\n]*\bsubmodule\s+add\b/.test(cmd)) {
    decide(`Cloning is done through ccenv, not inside the container. ${HOW('<owner/repo>')}`);
  }
  if (/GIT_SSH(_COMMAND)?=|core\.sshCommand|insteadOf|GIT_CONFIG_(PARAMETERS|COUNT|KEY_|VALUE_)|ssh\.variant|git-ssh-guard|sudoers|\/opt\/claude-secrets/i.test(cmd)) {
    decide('Changing how git connects (ssh command, URL rewrites, the git guard) is not allowed in this container.');
  }
  const remote = /\bgit\b[^;&|\n]*\bremote\s+(?:add|set-url)\b([^;&|\n]*)/.exec(cmd);
  if (remote) {
    // Anything URL-shaped (any scheme://, any user@host:) is checked; canon rejects what it can't place.
    const urls = remote[1].split(/\s+/).filter(t => /^([\w.-]+@[^\s:/]+:|[a-z][a-z0-9+.-]*:\/\/)/i.test(t));
    const bad = urls.find(u => !allowedCanon.has(canon(u)));
    const where = bad && canon(bad) ? canon(bad).split('/').slice(1).join('/') : '<owner/repo>';
    if (bad || urls.length === 0) decide(`Remote ${bad || '(unrecognized URL)'} is not an allowed repo for this org. ${HOW(where)}`);
  }
  // Downloading an unregistered repo's code through the API/CDN instead of git.
  if (/\b(gh\s+api|curl|wget|http)\b/.test(cmd)) {
    const pats = [
      /\brepos\/([\w.-]+)\/([\w.-]+)\/(?:tarball|zipball|contents|git\/|archive|readme)/g,
      /codeload\.github\.com\/([\w.-]+)\/([\w.-]+)/g,
      /raw\.githubusercontent\.com\/([\w.-]+)\/([\w.-]+)/g,
      /github\.com\/([\w.-]+)\/([\w.-]+)\/(?:archive|raw|releases\/download|blob)\//g,
    ];
    for (const re of pats) for (const m of cmd.matchAll(re)) {
      const c = canon(`${m[1]}/${m[2]}`, 'github.com');   // explicit host: an owner with a dot is still an owner
      const name = c ? c.slice(c.indexOf('/') + 1) : `${m[1]}/${m[2]}`;
      if (!allowedCanon.has(c)) decide(`Downloading code from ${name} (not an allowed repo) is blocked. ${HOW(name)}`);
    }
  }
  for (const tok of cmd.split(/[\s;&|()<>`"'=]+/)) {                // paths mentioned in the command
    if (!tok || /^[a-z]+:\/\//i.test(tok) || /^git@/.test(tok)) continue;
    if (tok.startsWith('/') || tok.startsWith('~') || tok === '..' || tok.startsWith('../') || tok.includes('/../')) {
      checkPath(tok, cwd, allowedDirs);
    }
  }
}

module.exports = { canon, entries };   // for berth's three-language canon test

if (require.main === module) {
  let data = '';
  process.stdin.on('data', c => { data += c; });
  process.stdin.on('end', () => {
    try { main(data); } catch (e) {
      if (mode === 'off') process.exit(0);
      decide(`repo-guard could not evaluate this action (${e.message}); blocked to be safe.`);  // fail closed
    }
    process.exit(0);
  });
}
