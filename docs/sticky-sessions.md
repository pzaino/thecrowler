# Sticky sessions and per-source proxies (2.3.1)

> **2.3.1 release note:** browser-backed Sources can now opt into one
> microproxy data-plane session rotation at the start of each new crawl, and
> `crawling_config.proxy` now actually configures the browser. A VDI may make
> its sticky profile the default by selecting its listener as the ordinary
> `proxy_url`, keep it as an opt-in standby, or let a source select a complete
> operator-approved profile. Chrome/Chromium rotate through a disposable
> VDI-local management browser first, so the path works when only the browser
> can reach microproxy's management API; eligible CDP failures fall back to the
> engine-direct client with the same key. These features add no database schema
> change. Sticky support requires one microproxy process per VDI.

This page is the operator guide for the sticky-session boundary, the effective
per-crawl VDI selection, and the completed `crawling_config.proxy` override.

## What 2.3.1 does

For a **new browser-backed source crawl**, the engine resolves one effective
VDI decision before creating any source browser:

1. acquires the VDI it will actually use and validates that the fenced crawl
   attempt is still authorized;
2. starts from that VDI's global defaults and applies the source's positional
   `custom.vdi` overrides to an isolated per-crawl copy;
3. determines the effective browser proxy and whether sticky rotation is
   selected. The decision is explicit configuration, never inferred from a port
   number;
4. selects a complete sticky profile. It may be the VDI default, the source's
   approved custom profile, or an approved profile selected by an explicit
   matching proxy listener;
5. validates the profile, downstream listener, browser compatibility and the
   operator's approval allowlist before any network I/O;
6. for Chrome/Chromium, creates a **disposable management browser on that VDI**
   and sends one logical authenticated `POST /api/v1/sessions/rotate` from a
   same-origin page under the browser-visible microproxy API origin, using the
   VDI-local `vdi_api_url` when configured (otherwise `api_url`). If that CDP
   route is unavailable or its network outcome is uncertain, the engine retries
   the **same key** through the existing engine-direct `api_url` client. Firefox
   skips CDP and uses the engine-direct client only;
7. requires HTTP 200 with `status: rotated`, the configured provider and a
   positive generation;
8. rechecks cancellation and the fenced claim;
9. only after the temporary management browser is fully closed, creates the
   real source WebDriver session using the same effective VDI/proxy and starts
   the crawl.

The whole source crawl then uses that pinned microproxy session. Pages, Selenium
refreshes, retries and reconnects inside the same source crawl do not rotate it
again.

A conventional or direct source performs no rotation API call, even when its
VDI has a standby sticky profile. Email and network-only sources reject an
explicit sticky request and do not perform a browser rotation.

The engine never rotates on a per-page, HTTP 403/429, failed-navigation or
redirect trigger, and CROWler 2.3.1 has no microproxy-control integration, IP
verification, provider failover or IP-history feature.

## Effective sticky selection

The source can influence the decision in three ways:

- `crawling_config.sticky_session: true` explicitly requires rotation using the
  effective profile.
- `crawling_config.sticky_session: false` explicitly disables inherited sticky
  mode. If the effective proxy is still a known sticky listener, validation
  fails; choose a conventional or direct proxy instead.
- Omitting the field inherits the effective VDI decision.

The VDI default participates as follows:

- If the VDI's ordinary `proxy_url` selects the same listener as
  `vdi[].sticky_session`, ordinary browser-backed sources inherit sticky mode.
- If `proxy_url` is a different listener, the profile is a standby. It is used
  only by an explicit source request or a source proxy that selects the known
  sticky listener. A conventional source keeps using the ordinary listener.
- A source selecting a configured sticky listener through
  `crawling_config.proxy`, the legacy top-level `proxy_url`, or
  `custom.vdi[].proxy_url` rotates instead of browsing that listener unrotated.
- A VDI with neither an ordinary proxy nor a sticky profile is direct; it never
  rotates and never performs a management request.

A Source may select a **complete** custom profile even when the global VDI has
no sticky default. A proxy URL that does not match a configured sticky profile
listener is treated as a conventional proxy: CROWler cannot infer stickiness
from an arbitrary URL, and a port number alone never identifies a rotating or
sticky listener.

```json
{
  "crawling_config": {
    "site": "https://example.org/",
    "source_type": "website"
  },
  "custom": {
    "vdi": [
      {
        "proxy_url": "http://microproxy-vdi1:8091",
        "sticky_session": {
          "proxy_url": "http://microproxy-vdi1:8091",
          "api_url": "http://microproxy-vdi1:8095",
          "vdi_api_url": "http://127.0.0.1:8095",
          "token_env": "MICROPROXY_VDI1_ALT_SESSION_API_KEY",
          "provider": "residential-sticky-alt"
        }
      }
    ]
  }
}
```

`custom.vdi` is matched positionally against the configured VDI list. The
nested profile is a full replacement, not a field-level merge. It must exactly
match the VDI's `sticky_session` default or one entry in
`approved_sticky_profiles`; otherwise the crawl fails before any rotation or
browser startup. The allowlist is operator-only and is never copied from a
source document. This prevents a source author from naming an arbitrary engine
environment variable or sending a bearer token to an attacker-controlled
management origin. A complete source document example is available at
[`examples/source-sticky-profile.json`](../examples/source-sticky-profile.json).

API and engine instances should share the same VDI default and allowlist. The
API validates a submitted source against its configuration snapshot, while the
engine revalidates the stored document against its own snapshot immediately
before the crawl; a mismatch fails closed.

## Source configuration

An explicit source opt-in can use the inherited profile:

```json
{
  "version": "1.0.0",
  "format_version": "1.0.0",
  "source_name": "Example sticky source",
  "crawling_config": {
    "site": "https://example.org/",
    "source_type": "website",
    "sticky_session": true,
    "proxy": "http://microproxy-vdi1:8081"
  }
}
```

`proxy` may be omitted. When supplied for a sticky source, it must equal the
selected effective profile's `proxy_url` listener after conservative
normalization (scheme and host case, effective port, no credentials/path/query/
fragment). It is not the rotation API URL.

A malformed `crawling_config`, `custom.vdi` entry, or nested `sticky_session`
object on a browser-backed source fails the crawl closed because it may hide or
redirect the sticky decision. Email and network-only sources keep the historical
log-and-continue behavior for unrelated malformed session fields; explicit
sticky mode remains unsupported for those source families.

A non-sticky source may use an independent listener and replace a sticky global
default for its own crawl:

```json
{
  "version": "1.0.0",
  "format_version": "1.0.0",
  "source_name": "Example independent proxy source",
  "crawling_config": {
    "site": "https://example.net/",
    "source_type": "website",
    "proxy": "http://regular-proxy:3128"
  }
}
```

## Proxy precedence

For **non-sticky** sources:

1. `crawling_config.proxy`, when non-empty;
2. the historical top-level source `proxy_url` (already-stored documents only;
   it is not a supported schema field);
3. an explicit `custom.vdi[].proxy_url` for the leased VDI;
4. the global `vdi[].proxy_url`;
5. direct.

The exact lowercase `no_proxy` sentinel still disables proxying at the legacy
top-level and `custom.vdi` layers. It is not valid for
`crawling_config.proxy`; omit the canonical field to inherit. A malformed
non-empty canonical proxy fails the crawl.

For **sticky** sources, the selected profile's `proxy_url` is authoritative. An
explicit `crawling_config.proxy`, legacy top-level `proxy_url`, or
`custom.vdi[].proxy_url` must match it after normalization. `no_proxy`, the
ordinary rotating listener, an unrelated proxy or a malformed URL is a
configuration error before any rotation request or browser startup. The global
ordinary `vdi[].proxy_url` being different does not conflict when the source
explicitly selected sticky mode.

## Global VDI configuration

```yaml
vdi:
  - name: vdi-1
    type: chrome
    host: selenium-vdi1
    port: 4444
    # Leave this as the rotating listener for a standby profile, or set it to
    # the sticky listener to make sticky the default for ordinary sources.
    proxy_url: "http://microproxy-vdi1:8080"
    sticky_session:
      proxy_url: "http://microproxy-vdi1:8081"
      # Engine-direct fallback (and CDP default when vdi_api_url is omitted).
      api_url: "http://microproxy-vdi1:8094"
      # Optional browser-namespace origin for Chrome/Chromium CDP rotation.
      vdi_api_url: "http://127.0.0.1:8094"
      token_env: "MICROPROXY_VDI1_SESSION_API_KEY"
      provider: "residential-sticky"
    # Optional operator allowlist for source-selected complete profiles.
    approved_sticky_profiles:
      - proxy_url: "http://microproxy-vdi1:8091"
        api_url: "http://microproxy-vdi1:8095"
        vdi_api_url: "http://127.0.0.1:8095"
        token_env: "MICROPROXY_VDI1_ALT_SESSION_API_KEY"
        provider: "residential-sticky-alt"
```

The four original `sticky_session` fields are required together when a profile
is present. An absent profile is valid. `vdi_api_url` is optional. Each approved
profile must be complete and use a distinct sticky listener. Configurations
with duplicate profiles or duplicate sticky listeners are rejected because
selection would be ambiguous.

### Deployment example A: browser-only local API

Use this when microproxy's management API is reachable from Chrome on the VDI
but not from the engine. `api_url` is still required by the profile, but it is
only attempted if CDP becomes unavailable; a correct CDP deployment does not
need the engine to reach it.

```yaml
vdi:
  - name: vdi-1
    type: chrome
    host: selenium-vdi1
    port: 4444
    proxy_url: "http://127.0.0.1:8080"
    sticky_session:
      proxy_url: "http://127.0.0.1:8081"
      api_url: "http://127.0.0.1:8094"       # engine fallback; may be unreachable
      vdi_api_url: "http://127.0.0.1:8094"   # Chrome in this VDI
      token_env: "MICROPROXY_VDI1_SESSION_API_KEY"
      provider: "residential-sticky"
```

### Deployment example B: CDP with engine-direct fallback

Use this when both the engine and Chrome can reach the same microproxy process
through their respective network names.

```yaml
vdi:
  - name: vdi-1
    type: chrome
    host: selenium-vdi1
    port: 4444
    proxy_url: "http://microproxy-vdi1:8080"
    sticky_session:
      proxy_url: "http://microproxy-vdi1:8081"
      api_url: "http://microproxy-vdi1.internal:8094"  # engine-direct fallback
      vdi_api_url: "http://127.0.0.1:8094"             # Chrome in the VDI
      token_env: "MICROPROXY_VDI1_SESSION_API_KEY"
      provider: "residential-sticky"
```

### Deployment example C: direct VDI default, source-specific sticky profile

Use this when a VDI has no global sticky default but the operator has approved an
additional profile for selected sources. The source references the complete
profile positionally.

```yaml
vdi:
  - name: vdi-1
    type: chrome
    host: selenium-vdi1
    port: 4444
    proxy_url: ""
    approved_sticky_profiles:
      - proxy_url: "http://microproxy-vdi1:8091"
        api_url: "http://microproxy-vdi1:8095"
        vdi_api_url: "http://127.0.0.1:8095"
        token_env: "MICROPROXY_VDI1_ALT_SESSION_API_KEY"
        provider: "residential-sticky-alt"
```

```json
{
  "crawling_config": {
    "site": "https://example.org/",
    "source_type": "website"
  },
  "custom": {
    "vdi": [
      {
        "proxy_url": "http://microproxy-vdi1:8091",
        "sticky_session": {
          "proxy_url": "http://microproxy-vdi1:8091",
          "api_url": "http://microproxy-vdi1:8095",
          "vdi_api_url": "http://127.0.0.1:8095",
          "token_env": "MICROPROXY_VDI1_ALT_SESSION_API_KEY",
          "provider": "residential-sticky-alt"
        }
      }
    ]
  }
}
```

The source rotates before its browser starts; other sources on the same VDI
remain direct.

### Deployment example D: globally sticky VDI

Use this when every ordinary browser-backed source on the VDI should rotate
through the same profile. The ordinary listener must equal the sticky listener;
otherwise the profile is treated as an opt-in standby.

```yaml
vdi:
  - name: vdi-1
    type: chrome
    host: selenium-vdi1
    port: 4444
    proxy_url: "http://microproxy-vdi1:8081"
    sticky_session:
      proxy_url: "http://microproxy-vdi1:8081"
      api_url: "http://microproxy-vdi1:8094"
      vdi_api_url: "http://127.0.0.1:8094"
      token_env: "MICROPROXY_VDI1_SESSION_API_KEY"
      provider: "residential-sticky"
```

A source can still override this with a conventional proxy for its own crawl;
that source does not rotate.

The engine reads the bearer token from the environment variable named by the
selected profile's `token_env`. Write the literal variable name; do **not** use
`${...}` interpolation for `token_env`, because configuration interpolation
would replace the name with the secret itself. A source can select only an
operator-approved profile, so it cannot name an arbitrary environment variable.
The token is never read from the source document and never written to logs, API
errors or metrics. Microproxy receives its own token through its own independent
`MICROPROXY_SESSION_API_KEY`; the engine and microproxy containers do not need
to share an environment.

Change or remove a sticky profile or an approved profile with a service restart.
The reload path can retain existing VDI pool slots, so SIGHUP is not a reliable
way to retire an old trusted profile.

The rotation API origin is always operator configuration. It is never derived
by rewriting a source-specified proxy URL, because the engine and Selenium may
use different network names or namespaces for the same microproxy.

### CDP-first rotation and `vdi_api_url`

For Chrome and Chromium sources, the default transport is a **temporary
management browser on the leased VDI**, not the engine's HTTP client. This is
required when the engine cannot reach microproxy's management API but Chrome on
the VDI can reach it through its own network namespace.

- `vdi_api_url` is the microproxy data-plane API origin **as seen from that
  temporary Chrome browser**. For a microproxy process sharing the VDI's
  network namespace, `http://127.0.0.1:8094` is typical.
- `api_url` is always the **engine-direct fallback** origin and remains the CDP
  origin when `vdi_api_url` is omitted. Older profiles without `vdi_api_url`
  therefore keep working: CDP first tries the browser-visible interpretation of
  `api_url`, then falls back to the engine-direct client.
- `127.0.0.1` in `vdi_api_url` means the browser's network namespace, not the
  engine and not necessarily the Docker host. If Selenium and microproxy run in
  isolated containers, use a private hostname or network mapping that Chrome in
  the VDI can reach. Never assume the engine can reach the same address.
- Both configured origins must address the **same live microproxy process**.
  Microproxy idempotency is process-local; two different processes can each
  accept the same key.

The disposable browser:

- is created on the **already leased VDI** using that VDI's host, port and
  browser type, so a source-level `custom.vdi[].host`, `port` or `type` cannot
  retarget the privileged management browser;
- uses a fresh Selenium session and temporary browser profile;
- does not inherit the source's rotating/sticky proxy listener: it starts with
  `--no-proxy-server`;
- never opens the source website, source URL or arbitrary target path;
- navigates to a fixed credential-free bootstrap path on the trusted management
  origin, verifies the exact final URL and `location.origin`, and only then
  executes a fixed same-origin `fetch` through CDP;
- passes the bearer token and idempotency key as CDP function arguments, not as
  page source, URL query values, tab-wide headers or browser storage;
- uses `redirect: 'error'`, `credentials: 'omit'`, no request body and a 4 KiB
  response cap; and
- is closed on every path before the source browser is created. A cleanup
  failure quarantines the VDI instead of releasing it or starting the source
  browser.

The POST is same-origin to microproxy, so it does not depend on the target
website's CORS or CSP. The fixed bootstrap path must still render a normal
document that can execute JavaScript. A plain 404 document is the expected
case; a browser image that cannot execute JavaScript on that document makes the
CDP route unavailable and triggers the engine-direct fallback.

Transport selection is deliberately asymmetric:

| CDP outcome | Engine-direct fallback |
| --- | --- |
| Valid HTTP 200, expected provider and positive generation | No |
| Firefox or unsupported CDP browser | Yes |
| Disposable browser/session creation or bootstrap failure, with safe cleanup | Yes (same key) |
| Disposable-session creation timeout or any failure where no session can be proven absent | No; quarantine the VDI |
| CDP fetch network failure, redirect, timeout or otherwise uncertain after the POST may have been sent | Yes (same key only) |
| HTTP 400/401/403/405/409 or `stale_idempotency_key` | No |
| HTTP 503 or other definitive server failure | No |
| Any other definitive non-2xx status (for example 404 or 429) | No |
| Malformed 200 or provider mismatch | No |
| Temporary-browser cleanup failure | No; quarantine the VDI |
| Crawl cancellation or fence loss | No |

The total source-boundary rotation budget is bounded at 120 seconds. CDP stages
are separately bounded (session creation, bootstrap, POST, cleanup), and the
direct client keeps its existing bounded retries. No temporary browser, CDP
command or management request may outlive the source cancellation/fence
context, except the bounded cleanup attempt that runs after cancellation.

The engine-to-Selenium control channel carries the management browser's CDP
arguments, including the bearer token and idempotency key. Keep the VDI/Selenium
endpoint on a trusted, preferably TLS-protected private network. The CDP
arguments are redacted from CROWler's own Debug5 CDP logging.

## Microproxy contract

Both transports use the running microproxy data plane, not
`microproxy-control`. Chrome/Chromium calls this from the disposable VDI-local
browser using `vdi_api_url` (or `api_url` when `vdi_api_url` is omitted); the
engine-direct fallback uses `api_url`:

```http
POST {vdi_api_url or api_url}/api/v1/sessions/rotate
Authorization: Bearer <token from token_env>
Idempotency-Key: <unique per source crawl attempt>
```

The same key, token and provider are retained across CDP, engine-direct fallback
and direct-client transport retries.

A successful response is HTTP 200:

```json
{"status":"rotated","provider":"residential-sticky","generation":2}
```

A replay of the latest retained key is also HTTP 200 with `"replayed": true` and
the original generation. A retained older key returns 409
`stale_idempotency_key`; this is a failure and the browser is not started. 401,
400, 409 and 503 are definitive failures; CROWler does not mint a new key and
does not switch the source browser to direct egress. The engine-direct
management fallback is a second transport for the same rotation request, not a
fallback to non-sticky browsing. Transport failures and uncertain timeouts are
retried a bounded number of times with the **same** key.

`generation` is process-local. A microproxy restart begins again at generation
1 and forgets idempotency history, so CROWler does not assume generations are
globally monotonic.

The CDP POST runs in a disposable Chrome/Chromium page whose origin is verified
against the trusted management origin; the engine-direct backup uses a dedicated
HTTP transport with no ambient proxy, normal TLS verification, no cookie jar and
no redirect following. The complete rotation boundary is bounded by a 120-second
source-crawl deadline; CDP stages have their own shorter bounds, and the direct
client keeps its bounded retries. The microproxy response body is capped at
4 KiB. Errors are sanitized categories such as `auth_failure`, `stale_key`,
`unavailable`, `invalid_response`, `cancelled` and `timeout`.

## Deployment topology and security

- Run **one microproxy data-plane process per VDI**. A process has one active
  sticky identity per enabled provider, shared by all browser clients of that
  process. Do not alias several VDIs to one microproxy process.
- A VDI lease is exclusive, so in normal operation source A closes its browser
  and releases the VDI before source B can rotate that VDI's microproxy
  identity. Independent VDIs may crawl in parallel with independent profiles
  and tokens.
- The engine attempts both browser `Close` and `Quit` before releasing a slot.
  If both fail, it logs the failure but cannot prove the browser process is
  gone; treat that VDI and its microproxy process as suspect and restart them
  before relying on the next rotation.
- The **browser** must reach the selected profile's `proxy_url`. For
  Chrome/Chromium CDP rotation it must also reach that profile's `vdi_api_url`
  when configured, or the browser-visible interpretation of `api_url` when it
  is omitted.
- The **engine** must reach the selected profile's `api_url` only when CDP
  fails and the engine-direct fallback is needed. A deployment where only the
  VDI namespace can reach the management API is valid while CDP succeeds; the
  fallback will fail closed if it cannot connect.
- Firefox supports HTTP and SOCKS5 downstream listeners but not HTTPS. A
  Firefox sticky source with an HTTPS sticky listener is rejected before the
  rotation request, so it cannot consume a new upstream identity that the
  browser could never use. Chromium/Chrome support HTTP, HTTPS and SOCKS5.
- Keep the data-plane API on a private network. Do not publish the rotation port
  to the host or the Internet. Use a distinct token per VDI. HTTPS
  `vdi_api_url` endpoints use Chrome's normal certificate verification; install
  the private CA in the VDI browser if required, never disable TLS verification.
- Keep the microproxy listener bound to the expected provider. The listener
  binding, not the rotation API alone, is what makes the browser's egress path
  match the provider that was rotated.
- Do not put tokens, session identifiers or rotation URLs in source JSON. A
  source may select an operator-approved complete profile, but it cannot add,
  extend or redirect the trusted profile. The privileged management browser is
  created on the leased Selenium identity, so a source-level
  `custom.vdi[].host`, `port` or `type` override cannot retarget the temporary
  browser that receives the token.

## Troubleshooting

| Symptom | Meaning | Action |
| --- | --- | --- |
| `sticky_session: engine token environment variable ... is not set` | The engine process does not have the configured token variable. | Set the variable in the engine deployment (not in `config.yaml`) and restart/reload. |
| `auth_failure (HTTP 401)` | Missing, wrong or malformed bearer credentials. | Compare the engine variable with microproxy's `session_control.bearer_token`; do not paste either into logs. |
| `stale_key (HTTP 409, code stale_idempotency_key)` | Another rotation superseded this attempt's key. | Treat as a failed source attempt. Do not reuse the key; the next new source crawl mints a fresh one. |
| `unavailable (HTTP 503)` | Microproxy could not generate a new identity; previous state was preserved. | Check microproxy/provider health and retry the source later. |
| `invalid_response` / `provider_mismatch` | The API returned malformed data or a different provider than configured. | Verify the listener-to-provider binding and the `provider` field; do not start the browser manually as a workaround. |
| `custom.vdi[...].sticky_session is not approved by the operator` | The source supplied a profile that is neither the VDI default nor an `approved_sticky_profiles` entry. | Add the exact complete profile to the VDI allowlist (operator action) or change the source to an approved profile. The runtime checks stored sources too. |
| `sticky_session:false would browse the configured sticky listener` | The source disabled rotation but its effective proxy is still a known sticky listener. | Select a conventional/direct proxy for that source or remove the false override. |
| Ordinary source rotates unexpectedly | The VDI ordinary `proxy_url` equals a configured sticky listener, or the source selected one. | Use a different ordinary listener for a standby profile, or set `crawling_config.sticky_session:false` with a safe conventional proxy. Do not infer mode from the port number. |
| Global sticky profile is not used | `vdi[].proxy_url` points at a different ordinary listener (standby) and the source did not opt in. | Set `vdi[].proxy_url` to the sticky listener for a global default, or set `crawling_config.sticky_session:true` on the source. |
| `CDP sticky rotation unavailable ... bootstrap` | Chrome could not create a usable same-origin document, or the bootstrap redirected/left the trusted origin. | Verify `vdi_api_url` is reachable **from inside the VDI browser**, returns a normal document on the fixed bootstrap path, and does not redirect. The engine then tries the direct `api_url` fallback. |
| `CDP temporary browser cleanup failed` | The disposable browser could not be proven closed. | The VDI is quarantined and not released. Restart the VDI/Selenium container and its microproxy before retrying; do not reuse the slot manually. |
| CDP succeeds but direct fallback also runs | Should not happen for a validated 200. | Treat as a defect: capture sanitized logs and inspect the CDP result classification. Direct fallback is only for unavailable/uncertain CDP outcomes, never for a valid rotation. |
| `vdi_api_url` works with `curl` on the host but CDP fails | `127.0.0.1` or a host address was tested from the wrong namespace. | Test from inside the VDI/browser namespace and configure `vdi_api_url` with an address Chrome can reach. Do not assume the engine and browser share a namespace. |
| `cancelled` / `timeout` | The crawl was superseded/cancelled or the API did not answer in time. | Confirm the source attempt was not superseded; check engine-to-API networking. |
| Rotation succeeded but pages use the wrong listener | The browser proxy and rotation profile are inconsistent. | Check `sticky_session.proxy_url`, any source proxy override, and the microproxy listener configuration. |
| Generation starts at 1 again | Microproxy restarted. | Expected. CROWler mints a fresh key for the next source crawl; generations are not global counters. |

Rotating the session **identifier** does not guarantee a different public egress
IP. The upstream provider decides the mapping, and microproxy may retain or
reassign an IP.

## Explicit 2.3.1 limitations

- CDP-first rotation depends on Chrome/Chromium executing JavaScript on a normal
  document served by the trusted management origin. If the bootstrap document
  cannot be created, executed or verified, the engine falls back to the
  engine-direct client; it does not weaken browser security or disable CORS,
  CSP, sandboxing or TLS verification.
- When both `vdi_api_url` and `api_url` are configured, they must address the
  same live microproxy process. Idempotency de-duplication is process-local and
  cannot detect a misaddressed second process.
- Each Chrome/Chromium sticky source boundary creates and destroys one additional
  short-lived browser. This is intentional overhead, not a persistent browser
  pool.
- No periodic, per-page, on-403/429, failed-navigation or redirect-triggered
  rotation.
- No different sticky identity for each concurrent browser/client behind one
  microproxy process.
- No microproxy-control integration. Only the live data-plane session API is
  used.
- No upstream-provider IP verification, IP history, proxy pools, provider
  failover or rotation via Action Rules.
- No new database table, migration, distributed session registry or long-lived
  session persistence.
- A source can select only the VDI default or an operator-approved profile. It
  cannot supply a bearer token value, an unapproved `token_env`, or an arbitrary
  management origin.
- Sticky mode is never inferred from a port number. A global profile is a
  default only when the ordinary `proxy_url` selects its listener; otherwise it
  is a standby opt-in.
- Email and network-only sources are outside the sticky source boundary. An
  explicit sticky request is rejected. Optional email web-link crawling uses the
  VDI's ordinary proxy and does not perform a source-start rotation.
- A deployment that aliases multiple VDIs to one microproxy process is
  unsupported because those VDIs would rotate and share one identity.
