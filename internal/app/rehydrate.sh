run() { echo "  -> ($PWD) $*"; "$@" >/tmp/rehydrate.log 2>&1 || { echo "     failed, see: $(tail -3 /tmp/rehydrate.log | tr '\n' ' ')"; }; }
echo "-- mise toolchains"
mise install 2>&1 | tail -1
echo "-- project dependencies"
find /workspace -maxdepth 6 \( -name node_modules -o -name .git -o -name .venv -o -name target -o -name vendor \) -prune -o \
  -type f \( -name package-lock.json -o -name pnpm-lock.yaml -o -name yarn.lock -o -name bun.lock -o -name uv.lock \
  -o -name requirements.txt -o -name Gemfile.lock -o -name go.sum -o -name Cargo.lock \) -print | sort | \
while read -r f; do
  cd "$(dirname "$f")" || continue
  case "$(basename "$f")" in
    package-lock.json) [ -d node_modules ] || run npm ci ;;
    pnpm-lock.yaml)    [ -d node_modules ] || run corepack pnpm install --frozen-lockfile ;;
    yarn.lock)         [ -d node_modules ] || run corepack yarn install ;;
    bun.lock)          [ -d node_modules ] || run mise exec bun -- bun install ;;
    uv.lock)           [ -d .venv ] || run uv sync ;;
    requirements.txt)  [ -f pyproject.toml ] || [ -d .venv ] || { run uv venv -q && run uv pip install -q -r requirements.txt; } ;;
    Gemfile.lock)      command -v bundle >/dev/null && run bundle install ;;
    go.sum)            run go mod download ;;
    Cargo.lock)        run cargo fetch ;;
  esac
done
echo "-- done"
