# Repo allowlist helpers, shared by the git guard and the workspace sweep (sourced, not executed).
# Registry: /config/repos.txt, one repo per line:  <dir> <clone-url> [branch]   (# comments)
# <clone-url> "local" = a repo created with `ccenv repo new --local` that has no remote yet.
# It lives on the read-only /config mount, so nothing inside the container can add to it.

REPOS_FILE="${REPOS_FILE:-/config/repos.txt}"

# repo_canon <url|owner/repo|upload-pack path> [host] -> host/owner/repo (lowercase, no .git)
repo_canon() {
  local u="$1" host="${2:-}" path
  case "$u" in
    git@*:*)      host="${u#git@}"; host="${host%%:*}"; path="${u#*:}" ;;
    ssh://*)      path="${u#ssh://}"; path="${path#*@}"; host="${path%%/*}"; host="${host%%:*}"; path="${path#*/}" ;;
    http://*|https://*) path="${u#*://}"; path="${path#*@}"; host="${path%%/*}"; path="${path#*/}" ;;
    *)            path="$u"; host="${host:-github.com}" ;;
  esac
  path="${path#/}"; path="${path%/}"; path="${path%.git}"
  printf '%s/%s\n' "$host" "$path" | tr '[:upper:]' '[:lower:]'   # tr, not ${,,}: also sourced by ccenv on macOS bash 3
}

repo_entries() {  # prints "dir canon url branch" for each registered repo
  [ -f "$REPOS_FILE" ] || return 0
  local dir url branch
  while read -r dir url branch _; do
    case "$dir" in ""|\#*) continue ;; esac
    [ -n "$url" ] || continue
    if [ "$url" = local ]; then printf '%s local/%s local %s\n' "$dir" "$dir" "${branch:--}"; continue; fi
    printf '%s %s %s %s\n' "$dir" "$(repo_canon "$url")" "$url" "${branch:--}"
  done < "$REPOS_FILE"
}

repo_allowed_canon() { repo_entries | awk '{print $2}' | grep -qxF "$1"; }
repo_allowed_dir()   { repo_entries | awk '{print $1}' | grep -qxF "$1"; }
