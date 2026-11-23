# Sticky sessions and per-source proxies (2.3.1)

> **2.3.1 release note:** browser-backed Sources can now opt into one
> microproxy data-plane session rotation at the start of each new crawl, and
> `crawling_config.proxy` now actually configures the browser. Both features
> are opt-in, add no database schema change, and leave ordinary sources on the
> existing proxy path. Sticky support requires one microproxy process per VDI
> and a trusted global `vdi[].sticky_session` profile.

This page is the operator guide for the 2.3.1 per-source sticky-session boundary
and the completed `crawling_config.proxy` override.

## What 2.3.1 does

For a **new browser-backed source crawl** whose source document contains
`crawling_config.sticky_session: true`, the engine:

1. acquires the VDI it will actually use and validates that the fenced crawl
   attempt is still authorized;
2. resolves the leased VDI by name against the trusted global
   `vdi[].sticky_session` profile;
3. resolves and validates one effective downstream browser proxy;
4. sends one logical authenticated `POST /api/v1/sessions/rotate` to that VDI's
   microproxy **data-plane** API with a unique `Idempotency-Key` (transport
   uncertainty may replay the same key a bounded number of times);
5. requires HTTP 200 with `status: rotated`, the configured provider and a
   positive generation;
6. rechecks cancellation and the fenced claim;
7. only then creates the WebDriver session, configured to use the sticky
   listener, and starts the source crawl.

The whole source crawl then uses that pinned microproxy session. Pages, Selenium
refreshes, retries and reconnects inside the same source crawl do not rotate it
again.

Ordinary sources perform no rotation API call. A source with
`sticky_session: false` performs no rotation even when its VDI has a sticky
profile. Email and network-only sources reject the opt-in.

The engine never rotates on a per-page, HTTP 403/429, failed-navigation or
redirect trigger, and CROWler 2.3.1 has no microproxy-control integration, IP
verification, provider failover or IP-history feature.

## Source configuration

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
selected VDI's trusted `sticky_session.proxy_url` listener after conservative
normalization (scheme and host case, effective port, no credentials/path/query/
fragment). It is not the rotation API URL.

A malformed `crawling_config` on a browser-backed source fails the crawl closed
because it may hide the sticky opt-in. Email and network-only sources keep the
historical log-and-continue behavior for an unrelated malformed
`crawling_config`; sticky mode itself remains unsupported for those source
families.

A non-sticky source may use an independent listener:

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

For **sticky** sources, no explicit source override selects the VDI's
`sticky_session.proxy_url` even if `vdi[].proxy_url` is a different rotating
listener. Any non-empty canonical, legacy or explicit `custom.vdi` override must
match that sticky listener. `no_proxy`, a rotating listener, an unrelated proxy
or a malformed URL is a configuration error before any rotation request or
browser startup.

## Global VDI configuration

```yaml
vdi:
  - name: vdi-1
    type: chrome
    host: selenium-vdi1
    port: 4444
    proxy_url: "http://microproxy-vdi1:8080"
    sticky_session:
      proxy_url: "http://microproxy-vdi1:8081"
      api_url: "http://microproxy-vdi1:8094"
      token_env: "MICROPROXY_VDI1_SESSION_API_KEY"
      provider: "residential-sticky"
```

The engine reads the bearer token from the environment variable named by
`token_env`. Write the literal variable name; do **not** use `${...}`
interpolation for `token_env`, because configuration interpolation would replace
the name with the secret itself. The token is never read from the source
document and never written to logs, API errors or metrics. Microproxy receives
its own token through its own independent `MICROPROXY_SESSION_API_KEY`; the
engine and microproxy containers do not need to share an environment.

Change or remove a sticky profile with a service restart. The reload path can
retain existing VDI pool slots, so SIGHUP is not a reliable way to retire an old
trusted profile.

The rotation API origin is always operator configuration. It is never derived
by rewriting a source-specified proxy URL, because the engine and Selenium may
use different network names or namespaces for the same microproxy.

## Microproxy contract

The engine uses the running microproxy data plane, not `microproxy-control`:

```http
POST {api_url}/api/v1/sessions/rotate
Authorization: Bearer <token from token_env>
Idempotency-Key: <unique per source crawl attempt>
```

A successful response is HTTP 200:

```json
{"status":"rotated","provider":"residential-sticky","generation":2}
```

A replay of the latest retained key is also HTTP 200 with `"replayed": true` and
the original generation. A retained older key returns 409
`stale_idempotency_key`; this is a failure and the browser is not started. 401,
400, 409 and 503 are definitive failures; CROWler does not mint a new key and
does not fall back to direct browsing. Transport failures and uncertain
timeouts are retried a bounded number of times with the **same** key.

`generation` is process-local. A microproxy restart begins again at generation
1 and forgets idempotency history, so CROWler does not assume generations are
globally monotonic.

The management request uses a dedicated HTTP transport with no ambient proxy,
normal TLS verification, no cookie jar and no redirect following. The complete
rotation boundary is bounded by a 20-second source-crawl deadline, individual
attempts are bounded by the client transport timeouts, and the response body is
capped at 4 KiB. Errors are sanitized categories such as `auth_failure`,
`stale_key`, `unavailable`, `invalid_response`, `cancelled` and `timeout`.

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
- The **browser** must reach `sticky_session.proxy_url`.
- The **engine** must reach `sticky_session.api_url`.
- Firefox supports HTTP and SOCKS5 downstream listeners but not HTTPS. A
  Firefox sticky source with an HTTPS sticky listener is rejected before the
  rotation request, so it cannot consume a new upstream identity that the
  browser could never use. Chromium/Chrome support HTTP, HTTPS and SOCKS5.
- Keep the data-plane API on a private network. Do not publish the rotation port
  to the host or the Internet. Use a distinct token per VDI.
- Keep the microproxy listener bound to the expected provider. The listener
  binding, not the rotation API alone, is what makes the browser's egress path
  match the provider that was rotated.
- Do not put tokens, session identifiers or rotation URLs in source JSON.
  `custom.vdi` cannot change `api_url`, `token_env`, `provider` or the trusted
  profile.

## Troubleshooting

| Symptom | Meaning | Action |
| --- | --- | --- |
| `sticky_session: engine token environment variable ... is not set` | The engine process does not have the configured token variable. | Set the variable in the engine deployment (not in `config.yaml`) and restart/reload. |
| `auth_failure (HTTP 401)` | Missing, wrong or malformed bearer credentials. | Compare the engine variable with microproxy's `session_control.bearer_token`; do not paste either into logs. |
| `stale_key (HTTP 409, code stale_idempotency_key)` | Another rotation superseded this attempt's key. | Treat as a failed source attempt. Do not reuse the key; the next new source crawl mints a fresh one. |
| `unavailable (HTTP 503)` | Microproxy could not generate a new identity; previous state was preserved. | Check microproxy/provider health and retry the source later. |
| `invalid_response` / `provider_mismatch` | The API returned malformed data or a different provider than configured. | Verify the listener-to-provider binding and the `provider` field; do not start the browser manually as a workaround. |
| `cancelled` / `timeout` | The crawl was superseded/cancelled or the API did not answer in time. | Confirm the source attempt was not superseded; check engine-to-API networking. |
| Rotation succeeded but pages use the wrong listener | The browser proxy and rotation profile are inconsistent. | Check `sticky_session.proxy_url`, any source proxy override, and the microproxy listener configuration. |
| Generation starts at 1 again | Microproxy restarted. | Expected. CROWler mints a fresh key for the next source crawl; generations are not global counters. |

Rotating the session **identifier** does not guarantee a different public egress
IP. The upstream provider decides the mapping, and microproxy may retain or
reassign an IP.

## Explicit 2.3.1 limitations

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
- No source-supplied rotation endpoint or bearer secret.
- A deployment that aliases multiple VDIs to one microproxy process is
  unsupported because those VDIs would rotate and share one identity.
