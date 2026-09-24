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

function entries() {
  let text = '';
  try { text = fs.readFileSync(REPOS, 'utf8'); } catch { return []; }
  return text.split('\n').map(l => l.trim()).filter(l => l && !l.startsWith('#'))
    .map(l => l.split(/\s+/)).filter(p => p.length >= 2)
    .map(([dir, url]) => ({ dir, canon: url === 'local' ? `local/${dir}` : canon(url) }));
}

function canon(u, host = 'github.com') {  // same rules as repo-policy.sh
  let p = u, h = host, m;
  if ((m = /^git@([^:]+):(.+)$/.exec(u))) { h = m[1]; p = m[2]; }
  else if ((m = /^(?:ssh|https?):\/\/(?:[^@/]+@)?([^/:]+)(?::\d+)?\/(.+)$/.exec(u))) { h = m[1]; p = m[2]; }
  p = p.replace(/^\/+/, '').replace(/\/+$/, '').replace(/\.git$/, '');
  return `${h}/${p}`.toLowerCase();
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
  const allowedCanon = new Set(list.map(e => e.canon));

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
    const urls = remote[1].split(/\s+/).filter(t => /^(git@|ssh:\/\/|https?:\/\/)/.test(t));
    const bad = urls.find(u => !allowedCanon.has(canon(u)));
    if (bad || urls.length === 0) decide(`Remote ${bad || '(unrecognized URL)'} is not an allowed repo for this org. ${HOW(bad ? canon(bad).split('/').slice(1).join('/') : '<owner/repo>')}`);
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
      const c = canon(`${m[1]}/${m[2]}`);
      if (!allowedCanon.has(c)) decide(`Downloading code from ${c.slice(c.indexOf('/') + 1)} (not an allowed repo) is blocked. ${HOW(c.slice(c.indexOf('/') + 1))}`);
    }
  }
  for (const tok of cmd.split(/[\s;&|()<>`"'=]+/)) {                // paths mentioned in the command
    if (!tok || /^[a-z]+:\/\//i.test(tok) || /^git@/.test(tok)) continue;
    if (tok.startsWith('/') || tok.startsWith('~') || tok === '..' || tok.startsWith('../') || tok.includes('/../')) {
      checkPath(tok, cwd, allowedDirs);
    }
  }
}

let data = '';
process.stdin.on('data', c => { data += c; });
process.stdin.on('end', () => {
  try { main(data); } catch (e) {
    if (mode === 'off') process.exit(0);
    decide(`repo-guard could not evaluate this action (${e.message}); blocked to be safe.`);  // fail closed
  }
  process.exit(0);
});
