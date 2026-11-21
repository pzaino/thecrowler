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

### Choosing values

Most deployments leave both options unset. Use `incognito: false` only when a
site behaves differently in a private session and you accept a normal profile;
the VDI still isolates that session from other sources. Use
`follow_redirects: true` for a single source that must reach its canonical URL
when the global policy is conservative, and `follow_redirects: false` for a
source whose redirects must never be silently accepted.

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
