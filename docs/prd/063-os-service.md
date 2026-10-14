# PRD: `berth serve` as an OS service (#63)

Status: **done** (the daemon first, then the rest: below) · Issue: [#63](https://github.com/ar4mirez/berth/issues/63) · Restarts orgs: no

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

## Follow-up: the rest of the issue

Done after the daemon, in one change:

- **The backup schedule on macOS is a launchd agent**, `dev.berth.backup` (`internal/app/schedule_launchd.go`).
  - It uses `StartCalendarInterval`, runs at background priority, and logs to `~/Library/Logs/berth/backup.log`.
  - `schedule` writes and loads it, and removes the cron line an older berth set, so there's no gap and no double
    run. `status`, `run` (`launchctl kickstart`) and `off` act on it.
  - The decision above kept cron because ccenv is the reference there. ccenv's behaviour stays exactly as it was
    on Linux. On macOS it is a recorded divergence (PARITY.md). `BERTH_SERVICE_MANAGER=systemd` keeps ccenv's
    choice anywhere, and the parity suite sets it, so it runs on a Mac as it does on Linux.
- **System units:** `--system` on `install|uninstall|status|logs` (`internal/app/service_system.go`).
  - It writes `/etc/systemd/system/berth-serve.service` with `User=` set to the account that ran sudo, its
    `HOME`, and no `--home`. It needs root.
  - Its socket falls back to `~/.config/berth/berth.sock`, which that account's clients find
    (`api.ClientSocketPath`).
  - This reverses "No `--system`" above, for machines where a session or linger isn't wanted.
- **Packages:** the `.deb`, `.rpm` and Arch packages, and both AUR packages, install
  `/usr/lib/systemd/user/berth-serve.service`, which runs `/usr/bin/berth serve` and is enabled for no one.
  - The unit is generated (`gendocs units`) from the same code as berth's own.
  - `status` and `uninstall` know it.
  - The packages workflow checks it is installed and not enabled.
- **Windows** stays WSL2 only (#60). There, berth is the Linux build, and these are the systemd units.

Checked by:
- `TestBerthScheduleLaunchd`, `TestBerthService` (`--system` refusals), `TestSystemUnit`;
- the `service` CI job, with real managers: `--system` with root's systemd on Linux, including a client finding
  the socket, and the schedule agent with real launchd on macOS, including taking over a cron line;
- the packages workflow's `native` job.
