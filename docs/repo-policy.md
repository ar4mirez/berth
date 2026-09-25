# Repo policy: canonical repo form

An org may only use the repos registered in its `config/repos.txt`. Three places decide
whether a repo is one of them, and they must agree exactly:

| Where | Implementation | Sees |
|---|---|---|
| git's ssh transport (`image/git-ssh-guard`) | `repo_canon` in `image/repo-policy.sh` | the ssh host, `-p` port, and the upload-pack path |
| Claude's PreToolUse hook (`image/repo-guard.js`) | `canon` | `git remote add/set-url` URLs, download URLs |
| the host CLI (`ccenv`, then berth) | `repo_canon`, then `internal/repopolicy.Canon` | what the user types, `origin` of adopted folders |

Each one maps a repo reference to a **canonical form**, `host/path`, and compares it with the
canonical forms of the registered URLs. The rules below define that form.
[`testdata/canon.tsv`](../testdata/canon.tsv) pins them, and a Go test runs every row through all
three implementations (bash, node and Go) and fails if any of them disagrees.

The guiding rule is **fail closed**. An input that doesn't fit these rules has no canonical form
(the functions return the empty string), and an empty form never matches anything, so it is denied.
A registered URL that has no canonical form keeps its directory registered (the workspace sweep
leaves it alone) but allows no git access.

## Input

`canon(ref, host)`: `ref` is the reference. `host` is optional; the git guard passes the ssh host
it was asked to connect to.

1. **Characters.** `ref` must be non-empty printable ASCII (`!` to `~`): no spaces, control
   characters or non-ASCII. Otherwise: rejected.
2. **Case.** `ref` and `host` are lowercased, ASCII letters only (`A`–`Z` → `a`–`z`). Nothing
   else is case-folded, so no Unicode rule can make two different names collide (for example the
   Kelvin sign `K` lowercasing to `k`).

## Forms

`ref` is read in one of three forms, in this order (the same order git uses):

1. **URL**: it contains `://`. `scheme://[userinfo@]host[:port]/path`.
   - `scheme` must be `ssh`, `git+ssh`, `ssh+git`, `https` or `http`. Others (`git://`,
     `file://`, `ext::`, …) are rejected.
   - The authority is everything up to the first `/` after `://`. There must be a `/` and a path.
   - `userinfo` is everything up to the **last** `@` in the authority, and it is dropped. An `@`
     after the first `/` belongs to the path.
   - `port`, if present, must be 1 to 5 digits whose value is the scheme's default (22 for the
     ssh schemes, 443 for https, 80 for http). It is then dropped. **Any other port is rejected**,
     because a repo registered as `git@host:owner/repo` means port 22, and another port on the same
     name may be another server.
   - The `host` argument is ignored.
2. **scp-like**: no `://`, and a `:` comes before the first `/` (or there is no `/`).
   `[user@]host:path`.
   - `host` is the part before the first `:`, after the last `@` in it. Any user is accepted and
     dropped.
   - The `host` argument is ignored.
3. **Bare path**: anything else, such as `owner/repo` or the git guard's `/owner/repo.git`.
   - If the `host` argument is given, the path is on that host.
   - Otherwise, if the path has at least two segments and the first one contains a `.`, that
     segment is the host (`github.com/acme/app`, `gitlab.com/group/sub/repo`). GitHub owner names
     can't contain `.`, so this is unambiguous for the default host. It also makes the canonical
     form stable: `canon(canon(x)) == canon(x)`.
   - Otherwise the host is `github.com`.

## Path

1. Split on `/` and drop empty segments. This removes any number of leading, trailing and
   repeated slashes.
2. While the last segment ends in `.git` and is longer than `.git`, drop that suffix
   (`app.git.git` → `app`; a segment that is exactly `.git` stays). Stripping them all keeps the form
   stable (GitHub doesn't allow repo names ending in `.git` anyway).
3. There must be at least one segment. Each one must match `[a-z0-9._~-]+`, and must not be `.` or
   `..`. Otherwise: rejected.

## Host

It must be a fully qualified DNS name or an IPv4 address: **at least two** dot-separated labels
of `[a-z0-9-]` that don't start or end with `-`. Otherwise (empty, a single label such as
`localhost` or an ssh-config alias, `_`, IPv6 brackets, a trailing dot, …): rejected.

The git guard runs ssh with `-F /dev/null`, so an ssh-config alias could never resolve anyway.
Requiring a dot also keeps the canonical form stable, because a bare path's dotted first segment
is read back as its host.

## Output

`host + "/" + segments joined by "/"`. For example:

| ref | host arg | canonical |
|---|---|---|
| `acme/app` | | `github.com/acme/app` |
| `git@github.com:Acme/App.git` | | `github.com/acme/app` |
| `ssh://git@github.com:22/acme/app.git/` | | `github.com/acme/app` |
| `https://user:tok@gitlab.com/group/sub/repo` | | `gitlab.com/group/sub/repo` |
| `/acme/app.git` | `github.com` | `github.com/acme/app` |
| `ssh://github.com:2222/acme/app` | | *(rejected: non-default port)* |
| `https://github.com/x@github.com/acme/app` | | *(rejected: `@` in the path)* |
| `acme/Kapp` | | *(rejected: non-ASCII)* |

## Registry entries (`repos.txt`)

One entry per line: `<dir> <clone-url> [branch]`. Fields are separated by spaces and tabs (no
other whitespace), one trailing CR is ignored, and a last line without a newline counts. Blank
lines, lines whose first field starts with `#`, and lines without a URL are ignored.
- The URL `local` means a repo with no remote yet. Its canonical form is `local/<dir>`.
- A URL with no canonical form keeps `<dir>` registered, with canonical form `-` in
  `repo_entries` output, and matches nothing.

## Behaviour changes from the three earlier implementations

These are intentional (the earlier versions disagreed with each other):

- **Ports:** shell kept an https port in the host (`h:8443/…`). JS dropped every port. Now: default
  ports are dropped and any other port is rejected. The git guard also refuses `-p` with anything
  but 22.
- **Userinfo:** shell cut at the first `@` anywhere, so `https://github.com/x@github.com/acme/app`
  became `github.com/acme/app` and was allowed. Now only the authority's userinfo is dropped, and
  `@` is not a valid path character.
- **scp users:** only `git@` was recognised. `deploy@host:o/r` used to become
  `github.com/deploy@host:o/r`. Now any user works.
- **Slashes:** shell removed one leading and one trailing `/`. JS removed all, but kept `//` inside
  the path. Now all empty segments are dropped.
- **Lowercasing:** shell's `tr '[:upper:]' '[:lower:]'` depended on the locale, and JS's
  `toLowerCase` applied Unicode case mapping. Now ASCII only, and non-ASCII input is rejected.
- **Unparseable input:** earlier versions always produced *something*, such as
  `github.com/ssh://host`. Now there is no canonical form. Because "no form" is `''`, the JS guard
  also leaves `''` out of its allowed set; otherwise an unparseable registered URL would let every
  unparseable remote URL through. (The earlier code never produced `''`, so it didn't have this problem.)
- **Bare `host/owner/repo`:** it used to become `github.com/host/owner/repo`. Now a dotted first
  segment is the host (see above). ccenv's `repo_url` already special-cased this.
- **repos.txt lines:** the shell reader dropped a last line with no trailing newline (so the
  workspace sweep would quarantine that repo), and kept a CR in the URL. The JS reader split on any
  Unicode whitespace. Now all three read lines as described above.
- **Download checks in `repo-guard.js`** pass `github.com` as the host explicitly, so an owner
  containing a dot is never taken for a host.
