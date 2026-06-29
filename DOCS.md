# V.A.L.E.T.

This doc describes how the valet tool works and what it's options are.

## Hub monitor

The hub monitor passively monitors local network devices by using `ip neigh`. It also does an active scan every 10-20 minutes using `arp-scan --localnet`. That can take 5+ minutes on the recurse local network (because it's a /16). Tune the scan timeout with:

```sh
-hub-scan-timeout "6m"
```

### Presence verification

`ip neigh` keeps `STALE` neighbor entries (and the `arp-scan` cache can be up to ~20 minutes old), so a registered device showing up in a scan does not prove it is still on the network. Before marking someone in the Hub, the monitor confirms the device is actually present:

- If there is a fresh `REACHABLE` `ip neigh` entry, that is trusted directly — no extra probe.
- Otherwise the monitor sends a targeted ARP request to the device's IPv4 address (a single-target `arp-scan`, reusing the same binary and `CAP_NET_RAW`) and only marks the device present if it answers. ARP is used because iPhones and Androids frequently drop ICMP behind a firewall or while power-saving, yet still answer ARP (often from Wi-Fi firmware) while idle.

Verification is fail-closed: if the probe cannot run (e.g. `arp-scan` missing, unprivileged, no IPv4 address, or a timeout) the device is not marked, and the error is reported in the scan log.

## Listening addresses

V.A.L.E.T. always listens on two addresses, both serving the same app:

- :80 - plain HTTP (for resolving valet.recurse.com)
- :443 - HTTPs with self-signed cert (for resolving valet.recurse.com)

V.A.L.E.T. uses a "kiosk" mode so that it can reset the shared browser after an OAuth session. Since OAuth is browser-profile specific, each user gets a fresh browser profile.

Note: ports below 1024 (`:80`, `:443`) require either root or the `CAP_NET_BIND_SERVICE` capability, see systemd service unit file for how this is done.


## Kiosk mode

For a shared kiosk where the browser and V.A.L.E.T. run on the same machine, enable kiosk reset support with:

```sh
./valet-v2 -kiosk \
    -kiosk-url "https://10.100.0.3" \
    -oauth-redirect-url "https://10.100.0.3/login/complete" \
    -kiosk-browser-profile "/tmp/valet-kiosk-browser"
```

When kiosk mode is enabled, V.A.L.E.T. gives each browser profile an unguessable, `HttpOnly` kiosk cookie through a bootstrap redirect. Requests bearing that cookie can reset the browser after logout, OAuth cancellation, invalid OAuth state, or an OAuth email mismatch even when they arrive on the shared HTTPS listener. Other clients cannot trigger the reset script.

The embedded reset script treats the configured profile path as a base name and launches Chromium in a fresh per-run profile directory derived from it (e.g. `/tmp/valet-kiosk-browser.AbC123`). It signals any previously running kiosk browser but does not wait for it to exit — the new browser starts immediately in its own clean directory, and each browser removes its own profile directory when it exits, so temp space does not accumulate.


### HTTPS

V.A.L.E.T. loads the certificate and key at `-tls-cert`/`-tls-key` (defaults under `data/`). If either file is missing, it generates a self-signed certificate covering `localhost` and the machine's interface addresses and writes it to those paths. The kiosk Chromium process receives a certificate exception pinned to that certificate's public key, so the fresh kiosk profile can open the local HTTPS URL without weakening validation for other sites. Other browsers will still warn on a self-signed certificate; replace the generated files with a real certificate to avoid the warning.

## Running at boot

Because configuration is passed as flags, the invocation is self-contained — nothing needs to be exported into the service's environment. A ready-to-edit systemd unit lives at [`deploy/valet-v2.service`](deploy/valet-v2.service); it runs kiosk mode and serves both the kiosk and LAN on `:443`. Install it with:

```sh
sudo cp deploy/valet-v2.service /etc/systemd/system/
# edit User, paths, DISPLAY, and the OAuth client id/secret to match your machine
sudo systemctl daemon-reload
sudo systemctl enable --now valet-v2.service
```

The unit grants `CAP_NET_BIND_SERVICE` so the non-root service can bind `:80`/`:443`, plus `CAP_NET_RAW`/`CAP_NET_ADMIN` for `arp-scan`. Because the kiosk reset launches a browser, the service must reach a graphical session — set `DISPLAY`/`XAUTHORITY` (and, on Wayland, `WAYLAND_DISPLAY`) to match the logged-in seat. See valet-kiosk-reset.sh for local browser reset details.

## OAuth setup

V.A.L.E.T. is registered as an OAuth app with Recurse Center (done in my profile).

OAuth user access tokens are stored per email in the SQLite data file. Treat `data/accounts.db` as sensitive.

### Embedded OAuth credentials

For production builds the client id and secret are stored encrypted at rest in `secrets.age` (committed to the repo) and baked into the binary at build time, so the deployed binary needs no `-oauth-client-id`/`-oauth-client-secret` flags or `.env`.

`secrets.age` is an [age](https://github.com/FiloSottile/age) file holding:

```
CLIENT_ID=...
CLIENT_SECRET=...
```

It is passphrase-encrypted with the Recurse password. Building with `make build` prompts for that passphrase, decrypts the file, and injects the values via linker flags:

```sh
-ldflags "-X 'main.embeddedOAuthClientID=...' -X 'main.embeddedOAuthClientSecret=...'"
```

Those `main.embeddedOAuthClientID`/`main.embeddedOAuthClientSecret` variables (defined in `config.go`) are the defaults for the `-oauth-client-id`/`-oauth-client-secret` flags, so an explicit flag or `.env` value still overrides them.

A plain `go build .` / `go run .` skips decryption entirely; the embedded variables are empty, so local development supplies the credentials via flags.

The baked-in credentials are recoverable from the binary (e.g. `strings valet-v2`). That is acceptable here because the binary only ever runs on local hardware inside the Recurse Center and is not distributed; encryption only keeps the plaintext out of the repo and git history.

To rotate the credentials, re-encrypt with the same passphrase and rebuild:

```sh
printf 'CLIENT_ID=%s\nCLIENT_SECRET=%s\n' "$new_id" "$new_secret" \
  | age -p -o secrets.age
make build
```

When OAuth is configured, entering an email address starts OAuth immediately for accounts that do not already have a stored token. The app sends the entered address as a login hint and verifies on callback that the authorized RC account email matches the entered address.
