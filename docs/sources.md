# TheCROWler Sources

TheCROWler use the concept of "sources" to define a website "entry-point" from
where to start the crawling, scrapping and interaction process.

A source is a combination of:

- a URL
- a crawling scope
- a flagset
- a configuration file (which is expressed in YAML format and has a well defined
  schema).

The configuration file is used to define the rulesets to be used for the source
and the interactions to be performed.

The flagset is used to define the flags to be used for the source.

The URL is the entry-point of the source.

A source can also be "enabled" or "disabled". If a source is "enabled", then it
will be used by TheCROWler. If a source is "disabled", then it will be ignored.

The crawling scope is used to define the scope of the crawling. The crawling
scope has 4 possible values:

- `page`: The crawling will be limited to the current page only (aka just the
  source entry-point).
- `FQDN`: The crawling will be limited to the current FQDN only (aka all the
  pages of the current FQDN, which includes the hostname, for example
  "www.example.com").
- `domain`: The crawling will be limited to the current domain only (aka all the
  pages of the current domain, which includes ALL found hostnames within the
  domain, for example "example.com").
- `l1 domain`: The crawling will be limited to the current l1 domain only (aka
  all the pages of the current l1 domain, which includes ALL found hostnames and
  ALL found subdomains within the l1 domain, for example ".com").
- `global`: The crawling will be global (aka all the pages of the current source
  and everything else on the entire internet that is linked from the source and
  then recursively crawled as well).

## Per-source crawling options

Some crawl behaviour can be overridden for a single source without touching the
global `config.yaml`. These options live directly under `crawling_config` in the
source configuration document and are validated by
`schemas/crowler-source-config-schema.json`.

```yaml
crawling_config:
  site: "https://example.com/"
  source_type: website
  incognito: false        # optional; overrides private browsing for this source
  follow_redirects: true  # optional; overrides http_headers.follow_redirects
```

### `incognito`

| Source value | Behaviour |
| --- | --- |
| omitted | Preserve the historical default: Chrome and Chromium launch in private mode |
| `true` | Launch a private/incognito browser session |
| `false` | Launch a normal browser session |

This option is honoured by Chrome and Chromium. Firefox has no equivalent launch
flag, so the setting is ignored there and no Chrome-specific argument is ever
passed to a Firefox session.

The value is a strict JSON boolean. `"false"` (a string), `0`, `{}`, `[]` and an
explicit JSON `null` are all rejected when the source is created or updated; a
rejected document never falls back to a different browsing mode. Only omitting
the key means "inherit the default". This matches the JSON schema, which declares
the property as `type: boolean`.

There is deliberately no global `crawler.incognito` key: private browsing can
only be changed per source, so one source can never alter the process-wide
default for others.

Sessions stay isolated in both modes. The VDI gives every browser session its own
profile, and source-specific cookies, local storage, cache and credentials are
never shared between sources, whether or not `incognito` is enabled.

### `follow_redirects`

| Global `http_headers.follow_redirects` | Source value | Effective |
| --- | --- | --- |
| `false` | omitted | `false` |
| `false` | `true` | `true` |
| `true` | `false` | `false` |
| `true` | omitted | `true` |

The source value is a strict JSON boolean and participates in the same
reject-malformed-document rules as `incognito`: omit the key to inherit, use
`true` or `false` to override, and expect an explicit `null` to be rejected. The
global configuration is never rewritten by a source override.

What the option actually controls:

- **HTTP metadata collection** (`httpinfo`) uses the effective value directly.
- **Browser navigation** always lets the browser perform the HTTP redirect,
  because Selenium cannot stop at a `3xx` and a cross-origin redirect has already
  happened by the time the page is inspectable. When the effective value is
  `false`, a navigation that lands on a different resource is **refused**: it is
  retried up to `retries_on_redirect` times and then fails the page instead of
  being indexed as the requested URL. When the effective value is `true`, the
  redirect is accepted and the final URL is used for indexing and metadata.

  "Different resource" is deliberately strict. Only standards-equivalent
  spellings of the same destination are treated as the same resource: case in the
  scheme and host, the scheme's default port, and an empty path versus `/` at the
  origin root. Everything else is a redirect, including `/hotel` versus
  `/hotel/`, repeated slashes, `%2F` versus `/`, path case, a different query or
  query order, and any non-default port. A destination that is not an absolute
  `http(s)` URL (for example `file:`, `about:` or a browser error page) always
  fails closed.

Redirect acceptance is never a way to bypass policy. The final URL is still
subject to the source's restrictions, `unwanted_urls` patterns, block patterns
and ownership checks. Redirect chains in the Go HTTP client are bounded and
loops produce an error. MCP Streamable HTTP keeps its own stricter rule and
never follows redirects; this source option does not affect it.

### `proxy`

`crawling_config.proxy` is the canonical per-source downstream browser proxy
listener. It must be an absolute `http`, `https` or `socks5` URL with a host and
an explicit port. It must not contain credentials, a path, a query or a
fragment. Authentication belongs to the proxy itself, not to the browser
listener URL. Chromium/Chrome accept HTTP, HTTPS and SOCKS5; Firefox accepts
HTTP and SOCKS5 only, so an HTTPS listener is rejected for a Firefox VDI before
the browser starts.

```yaml
crawling_config:
  site: "https://example.net/"
  proxy: "http://regular-proxy:3128"
```

For a source that does not use sticky sessions, the documented precedence is:

1. `crawling_config.proxy`, when non-empty;
2. the historical top-level source `proxy_url` for already-stored documents;
3. an explicit `custom.vdi[].proxy_url` for the leased VDI;
4. the global `vdi[].proxy_url`;
5. otherwise a direct connection.

The top-level `proxy_url` is **not** a supported schema field. It is read only
for backward compatibility with documents created before
`crawling_config.proxy` was completed at runtime. New source documents must use
`crawling_config.proxy`.

The exact lowercase sentinel `no_proxy` still disables proxying at the legacy
top-level and `custom.vdi` layers. It is not a valid value for
`crawling_config.proxy`; omit the canonical field to inherit the next layer.
A non-empty malformed `crawling_config.proxy` fails the crawl instead of
silently falling back to direct egress. Blank values inherit.

The value is resolved once at the source-crawl boundary and the same value is
used to configure Chromium or Firefox. Two sources using different proxies on
the same VDI pool slot do not share a proxy decision.

### `sticky_session`

`crawling_config.sticky_session` is a strict optional boolean that explicitly
overrides the effective VDI decision for this source:

- omitted: inherit the effective decision. A VDI whose ordinary `proxy_url`
  selects its sticky listener rotates automatically; a standby profile requires
  an explicit opt-in or an approved source profile. A source selecting a known
  sticky listener through an ordinary proxy override also rotates.
- `true`: require one rotation using the effective complete profile. A missing
  profile or a conflicting explicit source proxy fails closed.
- `false`: disable inherited sticky mode. If the effective proxy is still a
  known sticky listener, validation fails; choose a conventional or direct
  proxy for this source.
- an explicit `null`, string, number, array or object is rejected.

```yaml
crawling_config:
  site: "https://example.org/"
  source_type: website
  sticky_session: true
  # Optional. When supplied it must equal the effective profile's proxy_url
  # listener; it is not the rotation API URL.
  proxy: "http://microproxy-vdi1:8081"
```

A source may instead select a complete operator-approved profile through the
positional `custom.vdi` array, even when the global VDI has no sticky default:

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

The nested profile is a full replacement, not a partial merge. It must exactly
match the VDI's `sticky_session` default or an entry in
`vdi[].approved_sticky_profiles`; otherwise the crawl fails before any rotation
or browser creation. The allowlist is operator-only and is never copied from a
source document. A source cannot introduce a new `token_env` or management
origin.

A source can select the downstream proxy (conventional, rotating or the sticky
listener as documented above) but cannot redirect the pipeline to another
Selenium VDI. The acquired VDI's `name`, `host`, `port`, effective `sslmode`
scheme and browser `type` are authoritative; a differing `custom.vdi[]` value
fails closed before rotation or WebDriver creation. Host comparison is
case-insensitive after trimming, but no DNS alias resolution is performed.
The `sslmode` decision mirrors the launcher: exact-case `enable` means HTTPS
and every other value means HTTP. An accepted equivalent value is replaced by
the leased value in the effective session; the stored source document is not
rewritten.

The legacy `service_type`, `path` and `driver_path` fields still merge for
compatibility (`use_service` is not merged), but none of them is consulted by
the pooled remote WebDriver launch and cannot redirect the acquired VDI.

The trusted microproxy connection is configured per VDI with
`vdi[].sticky_session.{proxy_url,api_url,vdi_api_url,token_env,provider}` and
the optional `vdi[].approved_sticky_profiles` allowlist (see
[Configuration](configuration/config_yaml.md)). `vdi_api_url` is the optional
Chrome/Chromium browser-visible management origin; `api_url` remains the
engine-direct fallback. They must address the same live microproxy process. The
engine bearer token is read from the engine environment variable named by the
selected profile's `token_env`; it is never stored in the source document or the
global YAML.

For Chrome and Chromium, rotation is attempted first through a disposable
management browser on the leased VDI so it works when only the browser can reach
microproxy's management API. If that route is unavailable or uncertain, the
engine retries the same key through the engine-direct `api_url`. Firefox uses
the engine-direct client directly. Definitive API rejections, malformed 200s,
wrong providers, cancellation and temporary-browser cleanup failures do not
fall back.

When a sticky profile is selected, the engine uses that profile's listener even
when `vdi[].proxy_url` points at a different ordinary listener. A conflicting
`crawling_config.proxy`, `custom.vdi[].proxy_url` or legacy top-level override is
a configuration error, not a silent override. Sticky sources fail closed when
the effective profile is missing or unapproved, when the VDI name is ambiguous,
or when the configured token is missing.

Rotation happens **once per new source crawl**, before browser creation. Pages,
refreshes, retries and Selenium reconnects inside that crawl reuse the same
pinned session. Explicit sticky mode is rejected for email and network-only
sources.

This requests a new upstream sticky-session identifier. The upstream provider
may assign a different egress IP, but microproxy does not guarantee a different
public IP. CROWler 2.3.1 does not implement periodic or per-page rotation,
rotation on HTTP 403/429, failed navigation or redirect, per-client identities
behind one microproxy process, IP verification or retry loops, or
provider-control-plane integration. See
[Sticky sessions and per-source proxies](sticky-sessions.md) for the operator
topology, troubleshooting and limitations.

### Choosing values

Most deployments leave these options unset. Use `incognito: false` only when a
site behaves differently in a private session and you accept a normal profile;
the VDI still isolates that session from other sources. Use
`follow_redirects: true` for a single source that must reach its canonical URL
when the global policy is conservative, and `follow_redirects: false` for a
source whose redirects must never be silently accepted. Use
`crawling_config.proxy` only for a source-specific downstream listener, and
enable `sticky_session` only on a deployment that runs one microproxy data plane
per VDI and has configured the matching trusted profile.

## Using addSource and removeSource commands

The `addSource` and `removeSource` commands are used to add and remove sources
from the source configuration.

To add a configuration with addSource, you have to write the configuration in a
YAML file and then use the `addSource` command to add the source pinpointing the
YAML file.

This will ensure that the source is added (or updated) in the DB and that the
configuration file is uploaded in the config field of the source.

To remove a source, you have to use the `removeSource` command and the URL of
the source you want to remove.

## Adding Sources configuration validation in VSCode

To add Sources configuration validation in VSCode, you can use the
following extension:

Open (or create) your VSCode settings.json file and add the following:

```json
"yaml.schemas": {
   "./schemas/crowler-source-config-schema.json": "*-sourcecfg.y*ml",
}
```

Then, ensure you call all your config files with the `-sourcecfg.yaml` or
`-sourcecfg.yml` extension.

This will allow you to validate your Sources configurations in VSCode
as you type them.
