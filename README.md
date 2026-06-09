# V.A.L.E.T.

V.A.L.E.T. associates RC accounts with MAC addresses visible on the local network and can mark an authorized account in the Hub when one of its registered devices is seen.

## OAuth setup

Register V.A.L.E.T. as an OAuth app with Recurse Center, then run the server with:

```sh
cp .env.example .env
make run
```

If RC requires an OAuth scope for Hub Visits, set it with:

```sh
export VALET_OAUTH_SCOPE="..."
```

By default, the app infers its redirect URL as `/login/complete` on the current host. For local development, register this OAuth redirect URI with RC:

```sh
http://localhost:8080/login/complete
```

You can also set it explicitly:

```sh
export VALET_OAUTH_REDIRECT_URL="http://localhost:8080/login/complete"
```

If RC's OAuth endpoint paths differ from the defaults, override them:

```sh
export VALET_OAUTH_AUTHORIZE_URL="https://www.recurse.com/oauth/authorize"
export VALET_OAUTH_TOKEN_URL="https://www.recurse.com/oauth/token"
```

OAuth access tokens are stored per email in the JSON data file. Treat `data/accounts.json` as sensitive.
