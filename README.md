# V.A.L.E.T.

V.A.L.E.T. associates RC accounts with MAC addresses visible on the local network and can mark an authorized account in the Hub when one of its registered devices is seen.

All configuration is passed as command-line flags. Run `./valet-v2 -h` for the full list.

## OAuth setup

Register V.A.L.E.T. as an OAuth app with Recurse Center, then run the server with:

```sh
make run VALET_FLAGS="-oauth-client-id ... -oauth-client-secret ..."
```

If RC requires an OAuth scope for Hub Visits, set it with:

```sh
-oauth-scope "..."
```

By default, the app infers its redirect URL as `/login/complete` on the current host. For local development, register this OAuth redirect URI with RC:

```sh
http://localhost:8080/login/complete
```

You can also set it explicitly:

```sh
-oauth-redirect-url "http://localhost:8080/login/complete"
```

If RC's OAuth endpoint paths differ from the defaults, override them:

```sh
-oauth-authorize-url "https://www.recurse.com/oauth/authorize"
-oauth-token-url "https://www.recurse.com/oauth/token"
```

OAuth access tokens are stored per email in the SQLite data file. Treat `data/accounts.db` as sensitive.

When OAuth is configured, entering an email address starts OAuth immediately for accounts that do not already have a stored token. The app sends the entered address as a login hint and verifies on callback that the authorized RC account email matches the entered address.

## Hub monitor

The hub monitor checks local network devices every minute by default. On larger subnets, `arp-scan --localnet` can take longer than a small timeout, especially on a Raspberry Pi. Tune the scan timeout with:

```sh
-hub-scan-timeout "6m"
```

## Kiosk mode

For a shared kiosk where the browser and V.A.L.E.T. run on the same machine, enable kiosk reset support with:

```sh
./valet-v2 -kiosk \
	-kiosk-url "http://127.0.0.1:3000" \
	-kiosk-browser-profile "/tmp/valet-kiosk-browser"
```

When kiosk mode is enabled, V.A.L.E.T. runs its embedded reset script at startup and after local loopback requests for logout, OAuth cancellation, invalid OAuth state, or an OAuth email mismatch. Remote LAN requests cannot trigger the reset script.

The embedded reset script kills the Chromium process using the configured profile, removes that profile directory, and starts Chromium again in kiosk mode with a clean profile. Set `-kiosk-reset-command` only if you need to override the embedded script with a custom shell command; the command receives the browser profile directory, kiosk URL, browser executable, and browser log file as `$1` through `$4`.

## Running at boot

Because configuration is passed as flags, the invocation is self-contained — nothing needs to be exported into the service's environment. A systemd unit only needs the flags on `ExecStart`, for example:

```ini
[Service]
ExecStart=/home/ben/valet-v2 -kiosk \
	-oauth-client-id ... \
	-oauth-client-secret ...
```

Note that the kiosk reset launches a browser, so the service must run inside a graphical session (for example as a systemd user service started after `graphical-session.target`, so `DISPLAY`/`WAYLAND_DISPLAY` are available).
