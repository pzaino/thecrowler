# Email source API payloads

Email sources use the same privileged console routes as other sources:

| Operation | Method and route | Request type |
| --- | --- | --- |
| Create | `POST /v1/source/add` | `addSourceRequest` |
| Update | `POST /v1/source/update` | `updateSourceRequest` |
| Read one status | `GET /v1/source/status?q=<source-url>` | — |
| Read all statuses | `GET /v1/source/statuses` | — |

Console routes are available only when `api.enable_console` is enabled. Protect
these routes with authentication and network controls. POST bodies are the JSON
objects shown below; do not wrap them in a `q` property.

## Create an email source

The smallest practical payload declares the source type, repeats the retrieval
URI in `crawling_config.site`, and supplies the canonical `email` configuration.
Fields omitted from `email` receive the [safe defaults](#safe-defaults).

```json
{
  "url": "https://mail-source.example.test/archive",
  "category_id": 7,
  "usr_id": 42,
  "restricted": 0,
  "disabled": false,
  "flags": 0,
  "config": {
    "version": "1.0",
    "format_version": "1.0",
    "source_name": "Security mailbox archive",
    "crawling_config": {
      "site": "imaps://mail.example.test:993",
      "source_type": "email"
    },
    "email": {
      "connector": {
        "provider": "imap",
        "endpoint": "imaps://mail.example.test:993",
        "proxy_url": "http://proxy.example.test:8080"
      },
      "auth": {
        "method": "password",
        "credential_ref": "email/security-archive",
        "identity": "reader@example.test"
      },
      "mailboxes": {
        "include": ["INBOX", "Alerts"],
        "exclude": ["Alerts/Quarantine"]
      }
    }
  }
}
```

`url` is the source row's stable API identifier. The email retrieval location is
`config.crawling_config.site` and `config.email.connector.endpoint`; keeping
those two values identical avoids ambiguous configuration. `restricted: 0` is a
conservative choice for an email source because remote link following is
controlled separately by `config.email.extraction.links`.

A successful create returns HTTP `201` with a message containing the new source
ID.

## Update an email source

Identify the source with `source_id` (preferred) or `url`. When `config` is
present, it replaces the complete stored source configuration; it is **not** a
partial/deep merge. Send the complete `SourceConfig`, including all email fields
you want to retain.

```json
{
  "source_id": 123,
  "config": {
    "version": "1.0",
    "format_version": "1.0",
    "source_name": "Security mailbox archive",
    "crawling_config": {
      "site": "imaps://mail.example.test:993",
      "source_type": "email"
    },
    "email": {
      "connector": {
        "provider": "imap",
        "endpoint": "imaps://mail.example.test:993"
      },
      "auth": {
        "method": "password",
        "credential_ref": "email/security-archive-v2",
        "identity": "reader@example.test"
      },
      "mailboxes": {
        "include": ["INBOX", "Alerts", "Incidents"]
      },
      "crawl": {
        "mode": "listen"
      },
      "listener": {
        "enabled": true
      }
    }
  }
}
```

The route returns HTTP `200` with `{"message":"Source updated successfully"}`.
Although the route is registered with a nominal `204` success code, the shared
response helper changes it to `200` because the API always returns a JSON body.

The current update request uses zero values to mean "not supplied" for scalar
fields. Consequently, use a dedicated lifecycle endpoint or verify behavior
before attempting to change `disabled` from `true` to `false`, or numeric fields
to `0`, through `/v1/source/update`. Configuration replacement does not have
that ambiguity because `config` is a pointer field.

## Canonical type and field names

New payloads should use these exact names:

- `crawling_config.source_type`: `"email"`.
- `config.email`: the canonical `EmailSourceConfig` envelope.
- `email.connector.provider` and `email.connector.endpoint`.
- `email.auth.credential_ref` for external authentication material.
- `email.crawl.mode`: `"poll"` or `"listen"`.
- `email.listener.enabled`: must agree with `email.crawl.mode`.

The decoder accepts historical `mail`, `email_config`, and `mail_config`
envelopes, including those names below `custom`, but responses serialize the
canonical field as `email`. Use `email` in all new clients.

## Providers and endpoint URL schemes

Provider and endpoint scheme must match. Schemes are case-normalized during
validation.

| `connector.provider` | Accepted endpoint scheme | Example | Authentication reference required? |
| --- | --- | --- | --- |
| `imap` | `imap://` or `imaps://` | `imaps://mail.example.test:993` | Yes |
| `pop3` | `pop3://` or `pop3s://` | `pop3s://mail.example.test:995` | Yes |
| `gmail` | `gmail://` | `gmail://reader@example.test` | Yes |
| `graph-mail` | `graph-mail://` | `graph-mail://tenant.example.test/mailbox` | Yes |
| `maildir` | `maildir:///` plus an absolute path | `maildir:///var/mail/archive` | No |
| `mbox` | `mbox:///` plus an absolute file path | `mbox:///var/mail/archive.mbox` | No |

Network endpoints require a host and may include a valid port. They must not
contain query strings, fragments, whitespace, or embedded passwords. A Gmail
URI may use its user component as the mailbox identity, but it must not include
a password. `maildir` and `mbox` endpoints require an absolute local path, no
host, and no TLS options.

Network providers may set the optional `connector.proxy_url` to an `http://`,
`https://`, or `socks5://` URL. The proxy applies to provider crawling and to
provider-facing listener activity, including IMAP IDLE connections and
reconnects. Gmail and Microsoft Graph OAuth and API requests use the same
proxy. Proxy URL userinfo may carry proxy credentials, but responses and
configuration diagnostics redact the complete value.

Prefer encrypted `imaps://` and `pop3s://` endpoints. Connector TLS certificate
verification is enabled by default. `connector.tls.insecure_skip_verify` is an
explicit development-only opt-out and is valid only with an encrypted IMAP or
POP3 endpoint.

## Authentication references and placeholder secrets

API source payloads should select credentials by name rather than carry a
password, token, OAuth document, client secret, or private key. The value of
`email.auth.credential_ref` is looked up in the process-wide
`email.credentials` map.

```yaml
email:
  enabled: true
  credentials:
    email/security-archive:
      username: reader@example.test
      password: ${EMAIL_SECURITY_ARCHIVE_PASSWORD}
    email/google-reader:
      oauth_json: ${EMAIL_GOOGLE_OAUTH_JSON}
    email/graph-reader:
      client_id: 00000000-0000-0000-0000-000000000000
      client_secret: ${EMAIL_GRAPH_CLIENT_SECRET}
```

The values above are placeholders. Store real secrets in the deployment's
secret manager or environment interpolation path. Do not put literal secrets in
API examples, source `extensions`, logs, or version control. Status/config
responses redact secret-looking values found in email extension maps and redact
`auth.credential_ref` so secret-store naming conventions are not disclosed.
That redaction is a last line of defense, not a supported secret transport.

`auth.credential_ref` is mandatory for network providers and optional for
`maildir` and `mbox`. Status APIs return the stable `[REDACTED]` marker in place
of a configured reference.

## Polling and listener modes

`email.crawl.mode` controls how changes trigger reconciliation:

- `poll` is the default and works with every provider. Keep
  `listener.enabled` false or omit it.
- `listen` enables provider change hints while periodic reconciliation remains
  the authoritative safety net. Set `listener.enabled` to true.

Listener mode is supported for `imap`, `gmail`, and `graph-mail`. The associated
notification transports are IMAP IDLE, Gmail push, and Microsoft Graph webhook,
respectively. `pop3`, `maildir`, and `mbox` reject listener mode and must poll.
Notifications request reconciliation; they do not advance durable cursors by
themselves.

A minimal listener override is:

```json
{
  "crawl": {"mode": "listen"},
  "listener": {"enabled": true}
}
```

Duration fields in JSON are Go `time.Duration` values encoded as integer
nanoseconds. To avoid unit mistakes, omit them to use defaults unless the client
intentionally supplies nanoseconds. For example, `30000000000` is 30 seconds.

## Safe defaults

Defaults are applied before explicitly supplied `config.email` fields are
overlaid:

| Field | Default |
| --- | --- |
| `connector.timeout` | 30 seconds |
| `mailboxes.include` | `["INBOX"]` |
| `crawl.mode` | `poll` |
| `crawl.batch_size` | 100 |
| `crawl.max_messages` | 1,000 |
| `crawl.timeout` | 10 minutes |
| `crawl.limits.max_message_bytes` | 25 MiB |
| `crawl.limits.max_attachment_bytes` | 10 MiB |
| `crawl.limits.max_total_attachment_bytes` | 25 MiB |
| `crawl.limits.max_attachments` | 50 |
| `crawl.limits.max_header_bytes` | 1 MiB |
| `crawl.limits.max_embedded_message_depth` | 3 |
| `crawl.limits.max_mime_depth` | 30 |
| `crawl.limits.max_mime_parts` | 1,000 |
| `extraction.links.extract` | true |
| `extraction.links.allowed_schemes` | `["http", "https"]` |
| `extraction.links.follow_remote` | false |
| `extraction.links.max_links_per_message` | 100 |
| `extraction.attachments.include` | false |
| `listener.enabled` | false |
| `listener.buffer_size` | 128 |
| `listener.coalesce_window` | 1 second |
| `listener.reconnect_backoff` | 5 seconds |
| `listener.max_reconnect_backoff` | 1 minute |
| `listener.idle_reissue_interval` | 25 minutes |
| `reconciliation.poll_interval` | 5 minutes |
| `reconciliation.full_sync_interval` | 24 hours |
| `reconciliation.page_size` | 100 |
| `reconciliation.max_pages` | 100 |
| `reconciliation.lease_ttl` | 2 minutes |
| all `safety.allow_*` fields | false |

These defaults keep mailbox access read-only, disable JavaScript and remote
message resources, do not fetch links, do not emit attachments, bound parser
resource use, and retain periodic reconciliation.

### Downloading attachments for automated analysis

Attachment collection is disabled by default. To make policy-approved binary
content available to rules, plugins, or external-analysis integrations, enable
both attachment inclusion and downloading on the source:

```yaml
email:
  extraction:
    attachments:
      include: true
      download: true
      include_inline: false
      allowed_media_types:
        - application/pdf
        - application/octet-stream
  crawl:
    limits:
      max_attachment_bytes: 10485760
      max_total_attachment_bytes: 26214400
      max_attachments: 50
```

Each downloaded attachment is emitted in the parent email artifact's
`downloaded_attachments` array and in its child `email_attachment` artifact.
The `content_base64` field contains the decoded MIME payload, while `filename`,
`content_type`, `size`, and `sha256` support routing and integrity checks before
submitting the bytes to services such as malware scanners or sandboxes. Decode
`content_base64` before constructing a service's binary or multipart upload.

The existing attachment count, per-file size, aggregate size, inline-part, and
media-type policies are applied before content is exposed. A blocked or
oversized attachment therefore has no binary artifact. Keep `download: false`
(or omit it) when metadata and hashes are sufficient, because storing base64
increases index size by roughly one third and the content must be treated as
untrusted. `download: true` without `include: true` is rejected.

Remote link fetching requires all of the following:

1. `extraction.links.extract: true`;
2. `extraction.links.follow_remote: true`; and
3. a non-empty `extraction.links.allowlist`, unless the operator explicitly sets
   `safety.allow_unrestricted_links: true`.

Mailbox mutation, JavaScript execution, and loading remote message resources
are unsupported and validation rejects attempts to enable them.

## Status fields

`GET /v1/source/status?q=<source-url>` and `GET /v1/source/statuses` return the
usual source row plus `email_status` when durable email state exists:

```json
{
  "email_status": {
    "listener_status": "active",
    "last_synchronized_at": "2026-06-10T13:14:15Z",
    "cursor_summary": {
      "mailbox_count": 2,
      "checkpointed_mailboxes": 2,
      "has_token_cursor": false,
      "has_history_cursor": false,
      "has_uid_cursor": true
    },
    "processed_count": 1250,
    "failed_count": 3,
    "last_error_category": "transient"
  }
}
```

`listener_status` in the source status response is derived as follows:

- `stopped`: no active mailbox state;
- `starting`: active mailbox state exists but no healthy-listener timestamp is
  recorded;
- `active`: a healthy-listener timestamp is recorded;
- `degraded`: at least one latest message outcome is a failure.

The underlying listener event lifecycle uses `stopped`, `starting`, `running`,
`degraded`, `reconnecting`, and `failed`. The current aggregate source status
query derives only the four values above and retains `active` for its healthy
aggregate value.
`last_error_category` is `transient`, `permanent`, or `unknown`, and is omitted
when there is no latest failure. `email_status` itself is omitted until email
state has been persisted.

The cursor summary deliberately exposes only counts and cursor families. It
never returns mailbox identifiers, cursor values, raw provider errors, message
content, attachments, or credentials.

## Validation failures

Invalid email configuration on create or update returns HTTP `400` using the
standard API error shape:

```json
{
  "error_code": 400,
  "error": "invalid source configuration: invalid email configuration: connector.provider \"smtp\" is unsupported",
  "message": "Error performing addSource: %v"
}
```

Representative failures include:

- `crawling_config.source_type` is `email` but `config.email` is absent;
- unsupported providers such as `smtp`, or a provider/scheme mismatch;
- missing `auth.credential_ref` for a network provider;
- credentials, whitespace, query parameters, or fragments in an endpoint;
- an empty mailbox name, or the same mailbox in both include and exclude lists;
- non-positive bounds/timeouts, a batch larger than `max_messages`, or an
  attachment/header limit larger than its enclosing message limit;
- `crawl.mode` other than `poll` or `listen`, or disagreement between
  `crawl.mode` and `listener.enabled`;
- listener mode for `pop3`, `maildir`, or `mbox`;
- attachment inline/download/text extraction without attachment inclusion;
- remote link following without extraction and an allowlist/explicit
  unrestricted opt-in;
- enabling unsupported remote resources, JavaScript, or mailbox mutation; and
- reconciliation intervals, pages, or lease settings outside their required
  positive ordering constraints.

Validation error text is diagnostic and may evolve; clients should branch on
the HTTP status and `error_code`, not exact string matching. Request diagnostics
are redacted before being returned, but clients must still avoid sending
secrets in source payloads.

## Listener ownership and scaled engine dispatch

Provider listeners are process-wide lifecycle owners, not crawler-worker tasks.
When multiple `events` replicas are deployed, only the replica selected by
`events.master_events_manager` starts email listeners. Clone Events Managers
continue to serve the Events API but do not open duplicate provider sessions.

A listener notification does not broadcast work to crawler engines and does not
select an engine itself. For an idle source, it changes the existing source
status to `pending`. The normal `update_sources` claim function then selects and
changes that source to `processing` in the same transaction. Its `FOR UPDATE
SKIP LOCKED` row locking ensures exactly one engine replica receives it.
Repeated notifications for an already-pending source naturally coalesce through
the existing status.

A notification never changes a `processing` source back to `pending`, because
that would make an actively owned source eligible for a second engine. Such a
notification is only an advisory hint: the active reconciliation may already
discover the change, and the configured reconciliation polling remains the
safety net for a change that races with the end of the active run. Durable mail
checkpoints, rather than notification payloads, remain authoritative.

IMAP listener mode uses an IDLE session for each included mailbox. Gmail and
Microsoft Graph sources currently use the configured reconciliation poll
interval on the master listener owner as their safety-net trigger; externally
reachable push/webhook ingress and subscription renewal remain separate
provider integration work.
