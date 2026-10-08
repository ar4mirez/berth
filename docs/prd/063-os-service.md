# PRD: `berth serve` as an OS service (#63)

Status: **done for the daemon**; the backup job on macOS and Windows are not (below) · Issue: [#63](https://github.com/ar4mirez/berth/issues/63) · Restarts orgs: no

## Problem

`berth serve` has three things depending on it (`--via-daemon`, MCP over HTTP, a registered host's API, #148), and
someone has to start it by hand.

## Decisions (mine; the issue predates the command layout)

- **`berth system service install|uninstall|status|logs`**, in the `system` group, where the issue says
  `berth service`.
- **A service of the user**, not of the system: a systemd user unit, a launchd agent. `berth serve` runs as the
  user that owns the orgs, and its socket is that user's. No `--system`.
- **The daemon only.** The backup schedule on Linux is a systemd user timer already (`berth-backup`), so there is
  nothing to migrate there. Moving macOS's backup job from cron to launchd would change what `schedule` does where
  ccenv's behaviour is the reference; it is left as a follow-up.
- **Windows:** berth runs there under WSL2 only (#60), where this is the systemd unit.

## Design

- `internal/app/service.go`. The manager is launchd on macOS (`uname -s`), else systemd when `systemctl --user`
  answers; `BERTH_SERVICE_MANAGER` names it outright.
- The unit runs `<the path berth was run as> --home <state root> serve [--listen …]`: the stable link, so an
  upgrade doesn't break it (the lesson from the cutover, as the issue says).
- The unit is given `DOCKER_HOST`, `XDG_CONFIG_HOME`, `BERTH_ENGINE`, `BERTH_SOCKET` (and `PATH`, on macOS) when
  they are set at install time.
- systemd: `Restart=on-failure`, `WantedBy=default.target`; `enable` and `restart`, so installing again applies a
  new unit. Linger is read with `loginctl`, and explained when it is off.
- launchd: `RunAtLoad`, `KeepAlive`, a log file under `~/Library/Logs/berth`; `bootout` then `bootstrap` in the
  user's GUI domain.
- `status` exits 0 only when it is running.

## Tasks

- [x] T1. `system service install|uninstall|status|logs`, both managers; the catalog and the reference.
- [x] T2. `TestBerthService` (the harness's fake `systemctl`, `loginctl`, `journalctl`, `launchctl`).
- [x] T3. A run on a real Mac: install, status, a command through it, kill it and see it restarted, uninstall.
- [x] T4. A CI job on Linux and macOS runners: install, the API answers on its socket, status, uninstall.
- [x] T5. Docs: `docs/api.md`, `docs/hosts.md`, `PARITY.md`.

## Acceptance criteria

| Criterion (from the issue) | How it is checked |
|---|---|
| Install, uninstall and status tests per OS in CI | the `service` job in `integration.yml`, on `ubuntu-latest` (systemd) and `macos-latest` (launchd), with the real managers; `TestBerthService` everywhere |
| Upgrading berth keeps the services working | the unit runs berth's stable link (`TestBerthService`: `ExecStart` is the path berth was run as). Not exercised across a real upgrade |
| The existing `berth-backup` timer is taken over without a missed night | nothing to take over: on Linux it already is a systemd user timer of that name, and this change doesn't touch it |

## Not done

- **The backup job as a launchd agent on macOS** (it stays on cron there), and with it "schedule moves onto this".
- **System units** (`--system`).
- **Packages installing the units, disabled** (#60).
- **Windows** beyond WSL2.
