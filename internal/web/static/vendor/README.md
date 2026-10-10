# Vendored scripts

The web UI loads nothing from another origin, so what it needs is here, as published.

| File | Package | Version | From | npm integrity of the package |
|---|---|---|---|---|
| `alpine-csp.min.js` | [`@alpinejs/csp`](https://www.npmjs.com/package/@alpinejs/csp) (MIT, Caleb Porzio) | 3.17.4 | `dist/cdn.min.js` in the package | `sha512-SlRXmqO6kYhnxlg+99etmuzJtE9Lk4QbKjBHqerXzaMflJqoJXdz/SI3IvHJGZ/vRVyC3bR0SSBz40oY7goBeg==` |

To update one: download the package from the npm registry, check its integrity against the registry's, copy the
file, and change this table. `TestVendored` holds the file's SHA-256.
