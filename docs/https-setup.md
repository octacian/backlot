# Manual wildcard HTTPS setup on macOS

This operator walkthrough uses `dev.example.com`, LAN host `192.168.1.50` and
router/DNS server `192.168.1.1`. Substitute your own values in private files.
It provisions an existing gateway for the [HTTPS contract](https.md); Backlot
does not execute these steps. Inspect existing listeners/configuration first and
choose a new scope or reuse an explicitly agreed scope without overwriting another
gateway. Keep the operator domain, keys and machine configuration out of Git and
public reports.

## Obtain a public wildcard certificate without an API token

Install standard Caddy and Certbot through your normal package manager. With
Homebrew, `brew install caddy certbot` provides the two commands. Consult the
[Certbot macOS instructions](https://certbot.eff.org/instructions?os=osx&tab=wildcard&ws=other)
and [manual plugin guide](https://eff-certbot.readthedocs.io/en/stable/using.html#manual).
Manual DNS-01 needs access to edit TXT records in your authoritative public DNS
zone; it needs no DNS-provider API token or Caddy DNS module.

Keep Certbot's state and logs in private user directories:

```sh
BACKLOT_SETUP_DIR="$HOME/.config/backlot-caddy"
umask 077
mkdir -p "$BACKLOT_SETUP_DIR/letsencrypt" "$BACKLOT_SETUP_DIR/acme-work" "$BACKLOT_SETUP_DIR/acme-logs"
certbot certonly --manual --preferred-challenges dns \
  --config-dir "$BACKLOT_SETUP_DIR/letsencrypt" \
  --work-dir "$BACKLOT_SETUP_DIR/acme-work" \
  --logs-dir "$BACKLOT_SETUP_DIR/acme-logs" \
  --cert-name dev.example.com -d '*.dev.example.com'
```

Follow Certbot's account/terms prompts. When it asks for a TXT record at
`_acme-challenge.dev.example.com`, enter `_acme-challenge.dev` if your provider's
zone editor already appends `example.com`. Editors that expect a full name need
the full name instead. Do not duplicate the zone suffix. Use the exact displayed
TXT value and leave the prompt waiting until public propagation succeeds.

```sh
dig +short NS example.com
dig @ns1.your-dns-provider.example TXT _acme-challenge.dev.example.com +short
dig @1.1.1.1 TXT _acme-challenge.dev.example.com +short
dig @8.8.8.8 TXT _acme-challenge.dev.example.com +short
```

Query every authoritative name server returned by the first command, substituting
its real name for the example. Continue Certbot only once they and public
resolvers return the challenge value. Remove the challenge TXT when instructed.
Certificate files are under the selected config directory's
`live/dev.example.com/` lineage. Inspect them before configuring the gateway:

```sh
openssl x509 -in "$BACKLOT_SETUP_DIR/letsencrypt/live/dev.example.com/fullchain.pem" \
  -noout -subject -issuer -dates -ext subjectAltName
```

The wildcard covers one label such as `gateway-check.dev.example.com`, rather
than the bare suffix or two nested labels. Manual mode without authentication
hooks cannot renew unattended. Schedule operator renewal ahead of expiry and
repeat the challenge when needed; use the
[maintenance workflow](https.md#gateway-restarts-and-certificate-renewal) to
reread renewed files while preserving gateway configuration.

## Install a file-certificate JSON gateway

Create a private `caddy.json` outside the checkout. JSON does not expand `$HOME`;
replace the absolute paths and LAN address below. This example disables automatic
issuance and HTTP redirect listeners, loads the externally obtained certificate,
keeps administration on loopback and delegates wildcard application routing to
an explicitly empty Backlot scope:

```json
{
  "admin": {"listen": "127.0.0.1:2019"},
  "storage": {"module": "file_system", "root": "/Users/you/.config/backlot-caddy/storage"},
  "apps": {
    "tls": {
      "certificates": {
        "load_files": [{
          "certificate": "/Users/you/.config/backlot-caddy/letsencrypt/live/dev.example.com/fullchain.pem",
          "key": "/Users/you/.config/backlot-caddy/letsencrypt/live/dev.example.com/privkey.pem"
        }]
      }
    },
    "http": {
      "servers": {
        "backlot": {
          "listen": ["192.168.1.50:443"],
          "automatic_https": {"disable": true},
          "tls_connection_policies": [{}],
          "routes": [
            {
              "match": [{"host": ["gateway-check.dev.example.com"]}],
              "handle": [{"handler": "static_response", "body": "gateway ready"}],
              "terminal": true
            },
            {
              "match": [{"host": ["*.dev.example.com"]}],
              "handle": [{"@id": "backlot", "handler": "subroute", "routes": []}],
              "terminal": true
            }
          ]
        }
      }
    }
  }
}
```

Before starting, ensure port 443/2019 and the chosen scope are yours. Validate
without starting listeners:

```sh
caddy validate --config "$BACKLOT_SETUP_DIR/caddy.json"
```

Run Caddy in an operator-owned terminal with private autosave locations:

```sh
XDG_CONFIG_HOME="$BACKLOT_SETUP_DIR" XDG_DATA_HOME="$BACKLOT_SETUP_DIR" \
  caddy run --config "$BACKLOT_SETUP_DIR/caddy.json"
```

Binding 443 may fail with permission denied. If your macOS setup requires elevated
binding authority, explicitly start the same command through `sudo env` with those
absolute private environment paths and the absolute path reported by
`command -v caddy`. Keep the process and directories under the same chosen
authority on subsequent starts. Do not weaken private key permissions or grant
Backlot elevated rights. Ordinary Caddy restarts must use the saved effective
configuration (`--resume`), not this now-stale bootstrap; see the maintenance
workflow before certificate refresh or configuration reload.

## Configure application DNS and verify every consumer

On your router/local DNS server, configure `*.dev.example.com` to resolve to
`192.168.1.50`. Check the router's wildcard syntax/version and avoid modifying
unrelated zones. This local A record is separate from the public DNS-01 TXT.
If a VPN overrides system DNS, a macOS suffix-specific resolver can direct only
this application suffix to the router. Inspect any existing file before replacing
it; with operator approval, create `/etc/resolver/dev.example.com` containing:

```text
nameserver 192.168.1.1
```

That system directory requires administrator authority. Inspect `scutil --dns`
for the scoped resolver, then verify the actual system resolution and TLS path:

```sh
dig @192.168.1.1 gateway-check.dev.example.com +short
dscacheutil -q host -a name gateway-check.dev.example.com
curl --fail --show-error https://gateway-check.dev.example.com/
curl --fail --show-error http://127.0.0.1:2019/id/backlot
```

Plain `dig` can use a different resolver path from macOS application APIs; the
normal `curl` request must succeed without `--resolve`, `--insecure` or extra CA
files. A browser should render the health response without a certificate warning.

Using an explicitly pre-pulled diagnostic image, check normal container DNS and
public CA validation as well. Choose a unique name/label and remove only that
owned diagnostic container; for example:

```sh
docker image inspect alpine:3.21
BACKLOT_DNS_DIAG="backlot-dns-$(uuidgen)"
docker run --rm --name "$BACKLOT_DNS_DIAG" --label "backlot.operator-diagnostic=$BACKLOT_DNS_DIAG" \
  alpine:3.21 sh -c 'nslookup gateway-check.dev.example.com && wget -qO- https://gateway-check.dev.example.com/'
```

If this fails while the host succeeds, inspect Docker's DNS/VPN integration;
`nslookup gateway-check.dev.example.com 192.168.1.1` inside a diagnostic container
can distinguish router availability from the default resolver path. Do not call
the setup complete with a DNS override or TLS bypass hiding that failure.
Finish by configuring Backlot's private machine file as described in the HTTPS
guide. Gateway health is only a setup preflight: acceptance must still exercise
actual published scenes, SSR/API/WebSockets, redirects/cookies and scoped cleanup
from host, browser and container clients.
