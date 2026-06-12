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
