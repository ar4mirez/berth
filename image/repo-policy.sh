# Repo allowlist helpers, shared by the git guard and the workspace sweep (sourced, not executed).
# Registry: /config/repos.txt, one repo per line:  <dir> <clone-url> [branch]   (# comments)
# <clone-url> "local" = a repo created with `ccenv repo new --local` that has no remote yet.
# It lives on the read-only /config mount, so nothing inside the container can add to it.

REPOS_FILE="${REPOS_FILE:-/config/repos.txt}"

# repo_canon <url|owner/repo|upload-pack path> [host] -> host/path, or nothing if it isn't an acceptable repo.
# The rules are in docs/repo-policy.md, and testdata/canon.tsv pins them for this, repo-guard.js and berth.
# Always returns 0 (callers run under set -e). Written for bash 3.2 too: ccenv sources it on macOS.
repo_canon() {
  local LC_ALL=C   # ASCII-only matching and tr, whatever the caller's locale
  local u="${1:-}" host="${2:-}" path rest scheme auth port def last
  # Regexes live in variables: bash 3.2 and 5 disagree on quoting inside [[ =~ ]].
  local re_port='^[0-9]{1,5}$' re_seg='^[a-z0-9._~-]+$'
  local re_host='^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$'
  case "$u" in ""|*[!!-~]*) return 0 ;; esac   # printable ASCII only, no spaces
  case "$host" in *[!!-~]*) return 0 ;; esac
  u=$(printf '%s' "$u" | tr 'A-Z' 'a-z')
  host=$(printf '%s' "$host" | tr 'A-Z' 'a-z')

  if [[ "$u" == *://* ]]; then                                       # URL
    scheme="${u%%://*}" rest="${u#*://}"
    case "$scheme" in ssh|git+ssh|ssh+git) def=22 ;; https) def=443 ;; http) def=80 ;; *) return 0 ;; esac
    case "$rest" in */*) ;; *) return 0 ;; esac
    auth="${rest%%/*}" path="${rest#*/}"
    auth="${auth##*@}"
    if [[ "$auth" == *:* ]]; then
      port="${auth##*:}" auth="${auth%:*}"
      [[ "$port" =~ $re_port ]] || return 0
      [ "$((10#$port))" -eq "$def" ] || return 0
    fi
    host="$auth"
    [ -n "$host" ] || return 0
  elif [[ "$u" == *:* && "${u%%:*}" != */* ]]; then                  # scp-like [user@]host:path
    auth="${u%%:*}" path="${u#*:}"
    host="${auth##*@}"
    [ -n "$host" ] || return 0
  else                                                               # bare path
    path="$u"
  fi

  local parts=() segs=() p
  IFS=/ read -r -a parts <<< "$path"
  for p in "${parts[@]+"${parts[@]}"}"; do
    if [ -n "$p" ]; then segs+=("$p"); fi
  done
  if [ -z "$host" ]; then
    if [ "${#segs[@]}" -ge 2 ] && [[ "${segs[0]}" == *.* ]]; then
      host="${segs[0]}" segs=("${segs[@]:1}")
    else
      host=github.com
    fi
  fi
  [ "${#segs[@]}" -ge 1 ] || return 0
  last="${segs[${#segs[@]}-1]}"
  while [[ "$last" == *.git && "${#last}" -gt 4 ]]; do last="${last%.git}"; done
  segs[${#segs[@]}-1]="$last"
  for p in "${segs[@]}"; do
    [[ "$p" =~ $re_seg ]] || return 0
    case "$p" in .|..) return 0 ;; esac
  done
  [[ "$host" =~ $re_host ]] || return 0
  local IFS=/
  printf '%s/%s\n' "$host" "${segs[*]}"
}

repo_entries() {  # prints "dir canon url branch" for each registered repo
  [ -f "$REPOS_FILE" ] || return 0
  local line dir url branch c
  # "|| [ -n "$line" ]" keeps a last line that has no newline; a trailing CR is ignored.
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    IFS=$' \t' read -r dir url branch _ <<< "$line"
    case "$dir" in ""|\#*) continue ;; esac
    [ -n "$url" ] || continue
    if [ "$url" = local ]; then printf '%s local/%s local %s\n' "$dir" "$dir" "${branch:--}"; continue; fi
    c=$(repo_canon "$url")
    # No canonical form: the dir stays registered (not swept), but "-" matches no repo.
    printf '%s %s %s %s\n' "$dir" "${c:--}" "$url" "${branch:--}"
  done < "$REPOS_FILE"
}

repo_allowed_canon() { [ -n "$1" ] && [ "$1" != - ] && repo_entries | awk '{print $2}' | grep -qxF "$1"; }
repo_allowed_dir()   { repo_entries | awk '{print $1}' | grep -qxF "$1"; }
