# System packages

berth's image has the tools every org needs, and mise installs language toolchains inside an org. Some projects also
need **system packages** the image doesn't have: the shared libraries a browser links against, `libpq-dev`, fonts.
Nothing inside the container can install those, because apt needs root.

`berth pkg` lists them per org. berth builds an image for that org on top of its own, with the packages installed,
and runs the org on it.

```bash
berth pkg acme add @playwright-chromium      # a preset: what Playwright's Chromium needs
berth pkg acme add libpq-dev                 # any Debian package, by name
berth pkg acme                               # the list, and the org's image
berth pkg acme rm libpq-dev
berth pkg acme presets                       # the presets and what each installs
berth restart acme                           # apply it: the org starts on its new image
```

## What happens

- **`add` and `rm` build the org's image at once**, so a package that doesn't exist fails there and then, and the
  list goes back to what it was. `--no-build` only saves the list; the image is then built at the org's next start,
  or with `berth pkg acme build`.
- **Nothing is restarted.** The running container keeps its image. The new one applies at the org's next
  `berth restart acme` (or `up`), which stops the work running in it, so you pick the moment.
- **They survive every recreate**: `restart`, `up`, `env set`, `password rotate`, `token`. They are part of the
  image, not something installed into a running container.
- **The firewall isn't involved.** The packages are installed while the image is built, with Docker's own network,
  so the org needs no Debian mirror in its allowlist.
- **Only that org's image grows.** Other orgs keep running on berth's image.

## The list

`config/packages.txt` in the org's directory: one Debian package name or `@preset` per line, `#` for comments. It is
in the org's backups, and readable inside the container as `/config/packages.txt`. An entry that is neither a
package name nor a known preset is left out, and berth says so.

A package is named as Debian names it (`libnss3`, `fonts-noto-color-emoji`). berth's image is Debian 12.

## The image

An org with packages runs on `berth/claude-env-<org>:<base>-<list>`. `<base>` is berth's image and `<list>` the
package list, so a change to either is a new image:

- after `berth upgrade` brings a new berth image, the org's is rebuilt on top of it at its next start;
- an org with no packages runs on berth's image, as before.

The image is built from two things: berth's image, pulled from the release or built locally as usual, and one
`apt-get install` of the org's packages. The firewall, the git guards and the entrypoint are berth's, unchanged.

Older images of an org aren't removed: `docker image ls 'berth/claude-env-*'` lists them, and `docker image prune`
clears the ones nothing uses.

## Presets

| Preset | Installs |
|---|---|
| `@playwright-chromium` | The libraries and fonts Playwright's Chromium needs to start headless and render text as CI does (Latin, emoji, symbols, CJK, Thai): Playwright's own Debian 12 list, without `xvfb` and `xfonts-scalable`, which only headed runs need |

After adding it and restarting the org, `bunx playwright install chromium` (or `npx`) downloads the browser inside
the org as before, and it launches.

## On a registered host

`berth pkg acme@box1 …` works the same way: the list is in the org's directory on that host, and the image is built
there.
