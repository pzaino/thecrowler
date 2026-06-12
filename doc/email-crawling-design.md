# Email crawling target architecture

## Status and scope

This document defines the target architecture for crawling mailboxes as CROWler
sources. It covers the `pkg/mail` provider boundary, connector, fetcher, MIME
parser, normalizer, listener, reconciler, state store, and integration with the
crawler scheduler and indexing pipeline.

This document primarily describes the target architecture. Parts of the MVP now
exist in `pkg/mail`; sections labeled as an implemented contract describe current
behavior, while the remaining sections may still describe planned integration.
The MVP supports incremental, read-only mailbox ingestion without allocating a
browser or VDI session when its connector and pipeline are invoked by a caller.

## Design principles

1. **Keep mail concerns together.** Provider protocols and provider-specific
   behavior, MIME parsing, message identity, mailbox checkpoints, and mail
   normalization belong in `pkg/mail`. They must not be spread across
   `pkg/crawler`, `pkg/browser`, or `pkg/scraper`.
2. **Dispatch before browser allocation.** A source identified as email must be
   routed to the mail crawler before `vdiPool.Acquire`. Mail crawling does not
   use Selenium, browser navigation, or `crawler.ProcessContext`'s web-only
   state.
3. **Use a provider-neutral core.** IMAP and provider APIs expose different
   cursors and event mechanisms. The pipeline should consume a common message
   model and checkpoint contract while adapters translate provider behavior.
4. **Reconciliation is authoritative.** Push notifications and IMAP IDLE are
   hints to run reconciliation; they are not the durable source of truth.
5. **Advance state only after durable output.** A message checkpoint must not
   move past work that has not been successfully committed or deliberately
   recorded as skipped/quarantined.
6. **Default to passive and safe behavior.** The MVP is read-only, does not
   fetch remote content referenced by a message, and applies explicit size,
   count, and time limits to untrusted MIME input.
7. **Make retries idempotent.** Re-fetching or reprocessing the same provider
   message must converge on the same stable document identity without creating
   duplicates.

## Non-goals

The following are explicitly outside the MVP:

- **No JavaScript execution.** HTML message bodies are parsed as static,
  untrusted content and are never loaded in a browser runtime.
- **No remote image loading by default.** External images, stylesheets, fonts,
  links, tracking pixels, and other remote resources referenced by HTML are not
  dereferenced. Explicit future enrichment must be separately configured and
  use the project's guarded fetch facilities.
- **No mailbox mutation in the MVP.** The crawler does not mark messages read,
  add or remove flags or labels, move messages, delete messages, create
  folders, or send mail.
- No SMTP delivery or outbound-mail support.
- No calendar, contacts, tasks, or chat ingestion merely because a provider API
  exposes them.
- No guarantee of preserving a provider's complete presentation rendering;
  normalization favors searchable, deterministic content.

## Package ownership

All provider and MIME logic belongs in `pkg/mail`.

`pkg/mail` owns:

- provider clients and authentication adapters;
- mailbox discovery and selection;
- provider cursors, IMAP UID semantics, and change translation;
- bounded message and attachment retrieval;
- MIME decoding, multipart selection, transfer decoding, and charset handling;
- canonical mail message, address, body, attachment, and change models;
- mail-specific normalization, sanitization, identity, and deduplication keys;
- listener and reconciliation orchestration;
- mailbox state/checkpoint interfaces and implementations that encode mail
  semantics;
- redaction of provider errors and mail configuration values.

Other packages own only their existing platform responsibilities:

- `pkg/crawler` classifies and dispatches sources, supplies shared lifecycle
  dependencies, and receives mail crawl results. It must not implement IMAP,
  provider API, or MIME rules.
- `pkg/database` supplies general persistence primitives and migrations. It may
  implement storage mechanics required by `pkg/mail`, but mail checkpoint
  meaning and transitions remain defined by `pkg/mail`.
- indexing packages persist normalized documents and attachments. They should
  accept a transport-neutral ingestion model rather than raw provider or MIME
  objects.
- `pkg/browser` and browser-backed scraping are not dependencies of
  `pkg/mail`.

A target package layout is:

```text
pkg/mail/
  config.go            typed source configuration and validation
  types.go             canonical mail and change models
  connector.go         provider-neutral connector interfaces
  fetcher.go           bounded retrieval orchestration
  mime.go              MIME parser interfaces and implementation
  normalize.go         canonical document normalization
  listener.go          change-hint listener lifecycle
  reconciler.go        authoritative incremental/full reconciliation
  state.go             checkpoint and lease contracts
  identity.go          stable IDs and deduplication keys
  errors.go            typed/retryable errors and redaction
  providers/
    imap/               IMAP/IMAPS adapter, UID and IDLE behavior
    ...                 future provider API adapters
```

Provider subpackages are implementation details behind interfaces exported by
`pkg/mail`. Callers should not branch on provider-specific types.

## Source configuration

Email sources should be explicit and typed. The implemented `pkg/mail`
configuration currently has the following shape; callers should begin with
`DefaultSourceConfig` and then set the source-specific values:

```yaml
crawling_config:
  source_type: email
email:
  connector:
    provider: imap
    endpoint: imaps://mail.example.com:993
    timeout: 30s
    tls:
      server_name: mail.example.com
      insecure_skip_verify: false
  auth:
    credential_ref: secret/mail-archive
    identity: archive-account
  mailboxes:
    include: [INBOX, Archive]
    exclude: [Spam, Trash]
  crawl:
    mode: poll
    batch_size: 100
    max_messages: 1000
    timeout: 10m
    limits:
      max_message_bytes: 26214400
      max_attachment_bytes: 10485760
      max_total_attachment_bytes: 26214400
      max_attachments: 50
      max_embedded_message_depth: 3
  extraction:
    attachments:
      include: true
      download: true
      include_inline: false
      allowed_media_types: [application/pdf, application/octet-stream]
  listener:
    enabled: false
  reconciliation:
    poll_interval: 5m
    full_sync_interval: 24h
    page_size: 100
    max_pages: 100
    lease_ttl: 2m
```

The serialized integration schema may evolve, but it must preserve these
boundaries:

- `source_type: email` declares intent. A recognized retrieval scheme may help
  validation, but should not silently turn an unrelated source into email.
- The endpoint contains no password, OAuth token, or client secret.
- `auth.credential_ref` resolves through the process-wide `email.credentials`
  map in `config.yaml`. Environment interpolation or the remote configuration
  distribution system can provide the credential values; secret material must
  not be persisted in source status, checkpoints, logs, events, or indexed
  documents.
- Mailbox inclusion/exclusion, retrieval mode, TLS requirements, timeouts, and
  resource limits are typed fields, not provider-specific arbitrary maps where
  avoidable.
- TLS verification is enabled by default. Any insecure development override
  must be explicit and must produce an operational warning.
- SMTP, POP3, local `maildir`, and local `mbox` URLs are not IMAP connector
  endpoints.

Configuration changes that alter endpoint, account, provider, or mailbox
selection invalidate incompatible checkpoints and require a controlled
reconciliation rather than blind state reuse.

The CROWler runtime must also be enabled in the main configuration. For example:

```yaml
email:
  enabled: true
  credentials:
    secret/mail-archive:
      username: ${CROWLER_MAIL_USERNAME}
      password: ${CROWLER_MAIL_PASSWORD}
```

The map key must match the source's `auth.credential_ref`. Gmail credentials use
`oauth_json`; Microsoft Graph credentials use `client_id` and `client_secret`.
Because this section is part of the root configuration it participates in local
startup loading, remote configuration loading, and SIGHUP reloads.

### Implemented IMAP/IMAPS MVP contract

This subsection documents the current `pkg/mail` IMAP connector and pipeline,
not every capability in the target architecture.

#### Source URL forms and required configuration

The IMAP provider accepts these endpoint forms:

```text
imaps://mail.example.com
imaps://mail.example.com:993
imap://mail.example.com
imap://mail.example.com:143
imaps://[2001:db8::10]:993
```

The scheme and host are required. The port is optional: `imaps` defaults to
`993`, while `imap` defaults to `143`. A custom port is allowed in the range
`1-65535`. Scheme matching is case-insensitive after parsing.

The endpoint is a server address only. Do not put a username, password, mailbox
name, URL query, or fragment in it. In particular, a URL path does **not**
select a mailbox; configure mailbox names through `email.mailboxes`. The
current validation rejects credentials, queries, fragments, whitespace,
missing hosts, and invalid ports.

A usable typed source configuration requires:

- `connector.provider: imap`;
- an `imap://` or `imaps://` `connector.endpoint`;
- a positive `connector.timeout`;
- `auth.credential_ref`, resolved by the caller before connector creation;
- positive crawl, listener-buffer, and reconciliation limits. Using
  `DefaultSourceConfig` supplies conservative values and selects `INBOX` by
  default.

`auth.identity` is the stable account component of mailbox state and document
identity. If it is empty, the resolved username is used. Operators should keep
this value stable across credential rotation and should change it when the
configured account changes.

#### TLS behavior

`imaps://` means implicit TLS: the TCP connection is wrapped in TLS before any
IMAP command or authentication exchange. TLS 1.2 is the minimum version,
certificate and hostname verification are enabled by default, and the endpoint
host is used for SNI/hostname verification unless `connector.tls.server_name`
overrides it. `connector.tls.insecure_skip_verify` disables certificate
verification and is intended only for explicitly accepted development/test
risk.

`imap://` currently means cleartext IMAP. It does **not** request STARTTLS, and
TLS options are rejected for this URL form. The low-level connector can perform
a required STARTTLS upgrade when constructed directly with the internal
`starttls` policy, but the typed source URL/configuration mapping does not yet
expose that policy. Use `imaps://` for normal deployments; credentials must not
be sent through `imap://` on an untrusted network.

#### Authentication limitations

The secret resolver must provide a non-empty username and exactly one of:

- a password, authenticated with IMAP `LOGIN`; or
- a bearer token, authenticated with SASL `OAUTHBEARER`.

Credentials in endpoint userinfo are forbidden. The MVP does not implement
interactive OAuth, authorization-code/device-code flows, token acquisition or
refresh, provider-specific SASL selection, client certificates, Kerberos/GSSAPI,
or multiple fallback authentication mechanisms. `auth.method` does not change
the connector's mechanism selection today; selection is based on whether the
resolved secret contains a password or token.

#### Mailbox discovery and selection

The connector issues an IMAP `LIST "" "*"`, drops mailboxes marked
`\NoSelect`, and compares configured mailbox names exactly after trimming
surrounding whitespace. Matching is otherwise case-sensitive and uses the
server-visible mailbox name; there is no glob, regular-expression, hierarchy,
label, or URL-path matching in the MVP.

- An empty include list selects every selectable mailbox.
- A non-empty include list selects only exact matches.
- Exclusions always win, including when a name also appears in the include
  list.
- `DefaultSourceConfig` includes only `INBOX` unless the caller changes it.

Each mailbox is selected read-only, and message bodies are fetched with `PEEK`,
so ingestion does not intentionally set `\Seen`. Connector operations and
mailboxes are processed serially because IMAP mailbox selection is
connection-scoped. The connector does not change flags, move or delete
messages, create folders, or send mail.

#### Polling and IDLE scope

The implemented pipeline is a bounded reconciliation pass: when invoked, it
lists the selected mailboxes, processes them serially, and requests ascending
UID pages after each mailbox's checkpoint. `PollingListener` applies the
configured reconciliation interval for connectors without push support. It
runs the first pass immediately, waits only after a pass completes so runs do
not overlap, and stops through context cancellation. Lifecycle owners may use
it to invoke a `Reconciler` directly or to emit coarse hints through an
`EventSink`; its scheduler boundary is injectable for deterministic tests.

`IMAPIdleListener` implements the provider-neutral `Listener` contract. It
uses the configured mailbox include order as the priority set (or all supplied
mailboxes when the include list is empty), excludes configured omissions, and
opens one authenticated connection per selected mailbox because IMAP selection
and IDLE are connection-scoped. On an unsolicited mailbox update it leaves
IDLE, submits a mailbox-scoped reconciliation hint through `EventSink`, and
then resumes IDLE. Cancellation or a session failure stops the peer sessions
and logs out/closes every client. Notifications remain advisory: scheduled
reconciliation is still authoritative and owns all checkpoint progress.

#### State and replay semantics

State is maintained independently for each source, provider, account identity,
and mailbox. The IMAP cursor is the mailbox's `UIDVALIDITY` plus the highest
committed UID:

- Normal reconciliation searches for UIDs greater than the committed UID,
  sorts and de-duplicates them, and advances in ascending pages.
- A page cursor advances only after every upsert in that page has been fetched,
  parsed, and durably emitted (or deliberately discarded by retry policy). A
  failed page is retried from its previous cursor, so emitters must upsert
  idempotently by stable document ID.
- Checkpoint commits use optimistic versions; a stale writer must fail rather
  than overwrite newer mailbox progress.
- A `UIDVALIDITY` change commits a reset at UID zero and starts a bounded
  rescan. The default reset window is 1,000 messages for the run that detects
  the reset; later runs continue from the committed UID if more remain.
- IMAP document identity includes source, account, mailbox, `UIDVALIDITY`, and
  UID when no stronger provider message ID exists. Moving a message or a
  `UIDVALIDITY` reset can therefore produce a new identity.

This is forward, UID-based ingestion rather than a full mailbox mirror. The MVP
does not discover expunges or flag-only changes for UIDs at or below the
checkpoint, does not reconcile destination deletions, and ignores provider
`delete` changes in the processing pipeline. A UID returned by search but
missing from metadata fetch is represented internally as a delete race, but it
is not propagated to an index deletion workflow.

#### Additional MVP limitations

- The connector uses one authenticated connection and serializes all commands;
  there is no connection pool or parallel mailbox/message fetch.
- Mailbox discovery always uses `LIST "" "*"`; subscriptions, namespaces,
  special-use roles, and provider labels are not interpreted.
- There is no capability-driven feature negotiation beyond the IMAP library's
  command handling, and no explicit COMPRESS, CONDSTORE, QRESYNC, NOTIFY, or
  UTF8=ACCEPT support.
- The connector buffers each fetched RFC 5322 message in memory after enforcing
  advertised and actual size limits; it does not stream message bytes directly
  from the socket into downstream parsing/storage.
- The package contains the connector, MIME/normalization pipeline, and state
  stores, but complete source-schema exposure, secret resolution, scheduler
  dispatch, listener lifecycle, and production output wiring remain integration
  work unless supplied by the caller.

### Implemented POP3/POP3S MVP contract

POP3 support is a legacy, polling-only compatibility option. It has materially
less mailbox information than IMAP or provider APIs: the connector exposes only
the account's single maildrop as `INBOX`, with no folder hierarchy, labels,
archive/trash distinction, or cross-folder move semantics. Its incremental
synchronization and deletion semantics are also weaker because they are inferred
by comparing complete maildrop snapshots rather than obtained from a durable
server history or mailbox-scoped UID stream. **POP3 should generally not be
preferred over IMAP or a provider API** when either alternative is available.
Use it only when the server offers no stronger read-only retrieval interface and
these limitations are acceptable.

The implemented connector behaves as follows:

- It accepts `pop3://` and `pop3s://` source endpoints and authenticates with
  `USER`/`PASS`. `pop3s://` uses implicit TLS. The typed `pop3://` mapping is
  cleartext; required `STLS` exists only as a low-level connector policy and is
  not exposed through the source URL/configuration mapping.
- It returns exactly one mailbox, canonically identified as `INBOX`, rejects
  other mailbox names, serializes operations on one authenticated connection,
  and does not implement listener mode. As with IMAP, `pkg/mail` does not run a
  timer: the surrounding scheduler must invoke each reconciliation poll.
- Each poll obtains the current `LIST`/`UIDL` view and compares it with the
  previous opaque cursor snapshot. Newly observed identities become `upsert`
  changes and previously known identities missing from the new snapshot become
  `delete` changes. A pending snapshot is stored in the cursor and paged without
  listing again until all of its changes have been returned; the pipeline commits
  each page only after its upserts have been processed successfully.
- POP3 message numbers are session-local and are never treated as durable
  identity. Before fetching a message, the connector lists the maildrop again to
  resolve the current message number and avoid silently fetching a different
  message after renumbering.
- When the server supplies `UIDL`, the connector uses that opaque value as the
  polling comparison key and fetch locator in `MessageRef.Version`. It
  deliberately does not promote UIDL to `ProviderMessageID`, because POP3 does
  not provide the same strong, portable identity guarantees as a provider API.
- When `UIDL` is unavailable, polling retrieves each message within the
  configured size limit and uses a SHA-256 hash of the complete RFC 5322 bytes,
  plus an occurrence number in current message-number order, as comparison and
  lookup evidence. The occurrence suffix keeps byte-identical copies distinct
  within the polling snapshot, but changes in duplicate ordering or population
  can still make this fallback ambiguous.
- Final normalized document identity follows the provider-neutral identity
  policy. Because POP3 references have neither a strong provider message ID nor
  an IMAP `UIDVALIDITY`/UID tuple, the processor uses the complete-message
  SHA-256 fingerprint scoped by source, account identity, and `INBOX`. The UIDL
  remains provenance/lookup evidence rather than the document ID; consequently,
  byte-identical POP3 messages can converge on the same document identity.

Deletion reporting is observational only. A missing UIDL or fingerprint means
that the message was absent from the latest maildrop snapshot; POP3 cannot say
whether it was deleted, downloaded-and-removed by another client, expired by
server retention, or moved through a provider feature outside POP3's view. The
current processing pipeline also ignores provider-neutral `delete` changes, so
POP3 disappearance does not delete an indexed document. This makes the
implementation suitable for incremental ingestion of newly visible messages,
not for maintaining an authoritative mirror or audit-grade deletion history.

## Core data model

The provider-neutral model separates provider metadata, raw content, parsed
MIME, and normalized output.

```go
type Mailbox struct {
    ID   string
    Name string
}

type MessageEnvelope struct {
    Date      time.Time
    Subject   string
    From      []Address
    Sender    []Address
    ReplyTo   []Address
    To        []Address
    CC        []Address
    BCC       []Address
    InReplyTo string
    MessageID string
}

type MessageRef struct {
    Provider          string
    AccountID         string
    Mailbox           Mailbox
    UID               uint32
    UIDValidity       uint32
    ProviderMessageID string
    ProviderThreadID  string
    Version           string
    InternalDate      time.Time
    Flags             []string
    Size              int64
    Headers           HeaderMap
    Envelope          *MessageEnvelope
}

type RawMessage struct {
    Ref    MessageRef
    RFC822 io.ReadCloser
}

type Cursor struct {
    Token       string
    UID         uint32
    UIDValidity uint32
}

type FetchOptions struct {
    Headers     []string
    IncludeBody bool
    MaxBytes    int64
}

type ParsedMessage struct {
    Ref         MessageRef
    MessageID   string
    ThreadID    string
    Date        time.Time
    From        []Address
    To          []Address
    CC          []Address
    BCC         []Address
    ReplyTo     []Address
    Subject     string
    Headers     HeaderMap
    TextBody    string
    HTMLBody    string
    Attachments []Attachment
}

type Change struct {
    Kind ChangeKind // upsert, delete, or reset
    Ref  MessageRef
}
```

The concrete types may differ, but these distinctions are required:

- `MessageRef` is cheap metadata used for listing, comparison, and retrieval.
- `RawMessage` is a bounded stream, not an unbounded byte slice.
- `ParsedMessage` contains decoded mail semantics but no provider client
  objects.
- Normalized output is transport-neutral and suitable for the existing index
  boundary.
- Deletion/tombstone changes are represented even if the MVP initially only
  indexes upserts; otherwise a future reconciler cannot converge after mailbox
  removals.

A canonical document ID should be derived from source identity plus stable
provider identity, for example:

```text
mail:<source-id>:<account-id>:<mailbox-id>:<provider-message-id>
```

The RFC `Message-ID` header is useful metadata but is not sufficient as the
primary key: it can be absent, malformed, duplicated, or shared between copies
in different mailboxes. Content hashes may support deduplication and change
recognition, but should not replace provider identity.

## Component architecture

### Connector

The connector establishes a read-only provider session and exposes capabilities
without leaking protocol details into the crawler.

```go
type Connector interface {
    Connect(ctx context.Context, cfg Config, credentials Credentials) (Session, error)
}

type Session interface {
    Capabilities(ctx context.Context) (Capabilities, error)
    ListMailboxes(ctx context.Context, selector MailboxSelector) ([]Mailbox, error)
    ListChanges(ctx context.Context, mailbox Mailbox, cursor Cursor, limit int) (ChangePage, error)
    OpenMessage(ctx context.Context, ref MessageRef) (RawMessage, error)
    Listen(ctx context.Context, mailboxes []Mailbox, hints chan<- Hint) error
    Close() error
}
```

Responsibilities:

- resolve provider/account identity after authentication;
- enforce TLS and authentication policy;
- translate provider pagination, history tokens, IMAP UID/UIDVALIDITY, and
  throttling into common capabilities and typed errors;
- expose read-only operations to the rest of `pkg/mail`;
- close connections promptly on cancellation.

For IMAP, the adapter uses UID-based retrieval, never sequence numbers for
persistent identity. A UIDVALIDITY change produces a reset signal and forces
mailbox reconciliation. The implemented POP3 adapter is a polling-only legacy
fallback with the weaker identity and deletion behavior documented above; new
deployments should prefer IMAP or provider APIs.

### Fetcher

The fetcher turns a `MessageRef` into a bounded raw message and coordinates
provider limits, retry policy, and cancellation.

Responsibilities:

- enforce maximum advertised and actual message size;
- stream RFC 5322/MIME bytes to the parser with context cancellation;
- cap concurrent downloads per source and globally;
- classify transient provider failures, permanent access failures, malformed
  messages, and policy skips;
- retry transient reads with bounded exponential backoff and provider
  `Retry-After` guidance;
- never dereference URLs found in message bodies or signatures;
- close streams and release provider resources on every path.

A message exceeding policy limits is recorded with a deterministic skipped or
quarantined outcome so that reconciliation can advance without retrying it
forever. The outcome includes safe metadata and the applied policy, not raw
secret-bearing content.

### MIME parser

The MIME parser is implemented in `pkg/mail`, including charset and transfer
encoding behavior. It consumes a bounded RFC 5322 stream and emits a
`ParsedMessage`.

Required behavior:

- parse folded headers and encoded words with explicit total/header limits;
- decode base64 and quoted-printable using streaming, size-limited readers;
- normalize supported charsets to UTF-8 and preserve a safe indication of
  undecodable input;
- traverse nested multiparts with maximum depth and part-count limits;
- handle `multipart/alternative` deterministically, retaining text and HTML
  candidates while choosing a preferred searchable body;
- treat `multipart/related` resources as attachments or inline parts without
  loading external resources;
- identify attachments from disposition, filename, content ID, and media type;
- sanitize filenames and never write an untrusted filename as a filesystem
  path;
- tolerate malformed but recoverable mail and return typed errors for unsafe or
  unrecoverable structures;
- preserve only configured headers, with denylisting/redaction for sensitive
  authentication and routing data where necessary.

MIME type declarations and file extensions are untrusted. If attachment type
sniffing is needed, it occurs on bounded local bytes and the detected and
claimed types are recorded separately.

HTML parsing remains static. The parser/normalizer must not instantiate a
browser, execute scripts, process active forms, or fetch `http:`, `https:`,
`cid:`, or other referenced resources. `cid:` references may be associated with
already-present MIME parts, but that association does not authorize a network
request.

### Normalizer

The normalizer converts `ParsedMessage` into deterministic index documents and
attachment records. This logic is mail-specific and therefore belongs in
`pkg/mail`, even when the resulting document type is shared with other
crawlers.

Responsibilities:

- canonicalize addresses while retaining display names separately;
- normalize dates while retaining provider internal date and original header
  date for auditability;
- produce stable plain text from `text/plain`, or static sanitized HTML when a
  plain body is unavailable;
- strip scripts, event handlers, active embeds, forms, and unsafe URL schemes
  from any stored/rendered HTML representation;
- retain links as metadata without visiting them;
- identify quoted replies and signatures only as optional derived fields; do
  not discard original searchable text by default;
- emit message, mailbox, thread, participants, subject, headers, attachment
  metadata, and content hashes in a consistent schema;
- generate stable document and attachment IDs;
- distinguish an empty message from a parse failure or policy skip.

Attachments are emitted as child records through the crawler indexing boundary.
By default those records contain metadata and hashes only. When the source
explicitly enables `extraction.attachments.download`, policy-approved bytes are
base64-encoded in the parent artifact's `downloaded_attachments` collection and
its corresponding child artifact so rules, plugins, and external-analysis
integrations can consume them without dereferencing untrusted content. General
text extraction remains a separate opt-in. The parser's count, size, aggregate
size, inline-part, and media-type policies apply before any bytes are exposed.

### Listener

The listener reduces ingestion latency. It is optional and never owns durable
progress.

Provider implementations may use IMAP IDLE, webhooks, or provider-specific
change notifications. The common listener:

- receives change hints and coalesces them by source/mailbox;
- places a reconcile request on a bounded queue;
- reconnects with bounded backoff after expected provider disconnects;
- periodically exits/re-establishes long-lived sessions when required by the
  protocol;
- drops or coalesces excess duplicate hints rather than allowing unbounded
  memory growth;
- never writes checkpoints based only on a notification;
- falls back to scheduled polling when listening is unsupported or unhealthy.

Webhook verification, if added for a provider, is part of that provider adapter
in `pkg/mail`; the HTTP server may live in the platform layer but must hand a
verified, provider-neutral hint to the mail listener.

### Reconciler

The reconciler is the authoritative mailbox convergence loop. It runs on the
normal source schedule, after listener hints, and periodically as a safety net.

For each selected mailbox it:

1. acquires a per-source/mailbox lease;
2. loads the committed cursor and mailbox identity from the state store;
3. lists provider changes or messages from that cursor;
4. fetches, parses, normalizes, and commits each upsert idempotently;
5. records tombstones/deletions when the provider can report them;
6. commits the next cursor only after all preceding outcomes in the page are
   durable;
7. renews or releases the lease and emits bounded metrics/status updates;
8. repeats until caught up or until the crawl's time/work budget is reached.

A page cursor must not be committed if an earlier message in that page remains
neither indexed nor durably classified as skipped/quarantined. Implementations
may process messages concurrently, but cursor advancement requires an ordered
commit barrier.

The reconciler supports two modes:

- **Incremental reconciliation** uses a valid provider cursor, history token,
  or IMAP UID checkpoint.
- **Full reconciliation** relists mailbox state when no checkpoint exists, a
  cursor expires, UIDVALIDITY changes, mailbox selection changes, or an
  operator requests repair. It compares stable identities and content/version
  values to avoid reprocessing unchanged messages.

Provider history gaps and reset signals are normal control flow, not silent
success. They trigger full reconciliation and an observable reset metric/event.

### State store

The state store holds durable mail ingestion state independently from source
configuration.

```go
type StateStore interface {
    AcquireLease(ctx context.Context, key MailboxKey, owner string, ttl time.Duration) (Lease, error)
    LoadCheckpoint(ctx context.Context, key MailboxKey) (Checkpoint, error)
    CommitCheckpoint(ctx context.Context, lease Lease, previous Version, next Checkpoint) error
    RecordOutcome(ctx context.Context, lease Lease, outcome MessageOutcome) error
    ReleaseLease(ctx context.Context, lease Lease) error
}
```

#### Persistence decision

Use **new email-specific relational tables** for durable checkpoints, leases,
and per-message reconciliation state. Do not store this state in the existing
`Sources.status`, `Sources.engine`, error, or timestamp columns, and do not put
it in `Sources.config` or `Sources.details` as a generic key-value document.

The existing source columns remain the source-level operational summary: they
answer whether a source is queued, processing, successful, or failed and which
engine owns the current source run. They cannot represent multiple mailboxes,
independent cursors, lease expiry, or compare-and-swap versions without
serializing unrelated mailbox work through one source row. `Sources.config` is
operator intent and must not be mutated by workers. `Sources.details` is an
unstructured JSON object for internal source details, but `pkg/database` does
not expose it as a generic transactional key-value store with per-key unique
constraints, row locking, expiry, or atomic compare-and-swap. Encoding mail
state there would make concurrent mailbox updates overwrite one another and
would make deduplication and retention dependent on DB-specific JSON behavior.

The target schema is two logical tables (names are normative; exact SQL types
are database-specific):

- `EmailMailboxState`: one row per selected mailbox, containing its committed
  cursor, IMAP UIDVALIDITY and committed UID where applicable, checkpoint
  schema version, configuration fingerprint, optimistic version, lease owner,
  lease expiration, monotonically increasing fencing token, last successful
  reconciliation, listener health, reset reason, and timestamps.
- `EmailMessageState`: a bounded reconciliation ledger containing provider
  message identity, canonical document ID, provider version/content hash,
  disposition (`indexed`, `deleted`, `skipped`, or `quarantined`), failure
  count and bounded/redacted failure details, last observed time, deletion or
  quarantine time, and timestamps. It stores no RFC 822 body or attachment
  bytes.

The initial checkpoint implementation and reconciliation-ledger schema live in
`pkg/mail` and `pkg/database`. Schema additions follow the existing database
convention: fresh-install setup schemas and versioned, idempotent migrations
are maintained for every supported database rather than editing an old
migration.

#### Keys and constraints

Mailbox and account identifiers in keys are provider-derived stable opaque IDs,
or deterministic hashes of their canonical provider values when the raw values
contain sensitive data or exceed portable index lengths. Display names are
metadata and never keys.

- `EmailMailboxState` primary key:
  `(source_id, provider, account_key, mailbox_key)`.
- `EmailMessageState` primary key:
  `(source_id, provider, account_key, mailbox_key, provider_message_key)`.
- `source_id` references `Sources(source_id)`. A source soft-delete disables
  processing; physical deletion may cascade its email state.
- `provider_message_key` is the provider's stable message identity. For IMAP it
  is derived from `(UIDVALIDITY, UID)`, not a sequence number. A UIDVALIDITY
  reset therefore creates a new identity domain rather than colliding with old
  UIDs.
- `document_id` is the canonical
  `mail:<source-id>:<account-id>:<mailbox-id>:<provider-message-id>` identity
  (or its lossless/hashed storage form) and has a unique constraint. The index
  sink uses the same value as its idempotent upsert key.
- The RFC `Message-ID`, thread ID, content hash, subject, and sender are not
  unique constraints. Equal content in two mailboxes remains two provider
  objects unless a separately specified product policy links them.
- Checkpoint `version` and `fencing_token` are non-negative and monotonic.
  Lease expiry, disposition, and cursor-shape checks should be constraints
  where all database engines can express them, with equivalent validation in
  Go elsewhere.

These constraints deduplicate retries and overlapping poll/listener runs while
preserving legitimate mailbox copies. A duplicate insert/upsert must resolve to
the existing message row; it must not manufacture a second document identity.

#### Transaction and ordering rules

Lease and checkpoint correctness must depend on affected-row checks inside
transactions, not on process-local mutexes:

1. `AcquireLease` starts a transaction and conditionally inserts the mailbox
   row or updates it only when the lease is absent, expired, or already held by
   the same owner. A successful acquisition increments and returns the fencing
   token. Zero affected rows means lease contention.
2. The worker reads the checkpoint version associated with that lease and
   performs provider I/O outside the database transaction. Transactions must
   not remain open during network fetches, MIME parsing, or index writes.
3. Each normalized document or deletion is durably and idempotently committed
   to the output sink before its successful `EmailMessageState` outcome can
   authorize checkpoint progress. If the output sink and state tables share a
   database and transaction boundary, the output upsert, message-state upsert,
   and checkpoint compare-and-swap should commit together. Otherwise the sink
   commits first; a crash may replay the upsert, which is safe because
   `document_id` is idempotent.
4. `RecordOutcome` upserts by the message primary key while verifying the lease
   owner, unexpired lease, and fencing token. Failure counts are incremented
   atomically and stored error text is redacted and size-bounded.
5. `CommitCheckpoint` uses one short transaction to verify the lease and
   fencing token, compare the stored checkpoint `version` with `previous`,
   persist all outcomes required for that page, and advance only through the
   highest contiguous set of durably handled messages. The update increments
   `version`; zero affected rows is a checkpoint conflict, never success.
6. `ReleaseLease` clears ownership only when owner and fencing token still
   match. Expired workers cannot clear or advance a successor's lease.

A poison message is either left before the committed cursor for retry or is
recorded with an explicit terminal `skipped`/`quarantined` policy in the same
checkpoint transaction. Logging an error and advancing without a durable
outcome is forbidden. Listener hints never advance state.

#### Retention

- `EmailMailboxState` is retained for the life of the source, including while
  the source is disabled. Removing a mailbox from selection marks its state
  inactive; retain it for a configurable grace period before purge so a
  temporary configuration change does not force an avoidable full crawl.
- Successful `EmailMessageState` rows are retained at least through the
  provider's maximum usable history window and one complete full-
  reconciliation interval. This preserves deletion detection and prevents a
  stale listener hint from recreating a duplicate. Deployments may retain only
  identity, version/hash, disposition, and timestamps after richer diagnostic
  fields expire.
- Tombstones are retained longer than the maximum replay/reconciliation window.
  Quarantined rows are retained until operator resolution or an explicit
  policy expiry. Failure details use a short configurable retention period and
  are cleared independently from identity rows.
- No raw message, decoded body, attachment content, credentials, or bearer
  tokens belong in either table. Provider cursors that contain sensitive
  opaque tokens must use the project's at-rest encryption mechanism before
  persistence.
- Retention cleanup runs in bounded batches, excludes rows covered by an active
  lease, and is restartable. Cleanup must not use source-level event retention
  as a substitute for state retention.

#### Cross-database implications

PostgreSQL is the currently supported database according to `pkg/database`;
SQLite and MySQL/MariaDB schemas are also maintained and must not silently
receive weaker correctness. The implementation should use the `database.Handler`
transaction API and DB-specific statements behind a common state-store
contract.

- PostgreSQL may use `INSERT ... ON CONFLICT`, `UPDATE ... RETURNING`, row
  locks, and `TIMESTAMPTZ`, but those features cannot leak into mail semantics.
- MySQL/MariaDB uses `INSERT ... ON DUPLICATE KEY UPDATE` or conditional
  updates and verifies affected rows. UTC timestamps and indexable key lengths
  must be chosen explicitly; JSON equality or JSON-path indexes are not part of
  correctness.
- SQLite uses `ON CONFLICT` plus conditional updates. Its single-writer model
  can serialize writes, but lease/version predicates are still required for
  correctness and for parity with production databases. Timestamps are stored
  and compared in one documented UTC representation.
- Avoid partial indexes, generated JSON keys, advisory locks, and database
  server clocks as the only expression of an invariant unless equivalent
  implementations and tests exist for all backends. Lease expiration should be
  evaluated using the database clock within each statement to avoid worker
  clock skew.
- Identifiers, uniqueness, foreign-key actions, transaction isolation, and
  affected-row behavior require backend-specific integration tests. The same
  tests must cover concurrent lease acquisition, stale fencing tokens,
  checkpoint conflicts, duplicate message upserts, UIDVALIDITY reset, and
  retention cleanup.

Database migrations and SQL implementations live in `pkg/database`, but
`pkg/mail` defines `Checkpoint`, `MessageOutcome`, reset, retention, and
transition semantics. An in-memory implementation should support deterministic
unit tests but is not evidence of database transaction correctness.

## End-to-end flow

```text
source scheduler
    |
    v
pre-VDI source classifier/dispatcher
    |
    +-- website ----------> existing VDI/browser crawler
    |
    +-- network ----------> existing network inspection path
    |
    `-- email ------------> pkg/mail Runner
                               |
                               +--> resolve credentials
                               +--> Connector.Connect
                               +--> discover/select mailboxes
                               +--> Listener ---- hints ----+
                               |                            |
                               `--> Reconciler <------------+
                                      |
                                      +--> StateStore
                                      +--> Fetcher
                                      +--> MIME parser
                                      +--> Normalizer
                                      `--> index/blob commit
```

The runner owns one crawl invocation and shared limits. A long-lived listener
should be managed by a mail service lifecycle rather than by holding a normal
crawl job open indefinitely. Both scheduled runs and listener hints invoke the
same reconciler.

## Crawler integration

### Classification and dispatch

Add an email protocol/source family and classify using parsed, case-normalized
schemes plus explicit source type. Initial retrieval schemes should be limited
to those actually implemented, such as `imap` and `imaps`. Unsupported schemes
must produce a validation error instead of falling through to NETInfo.

Dispatch must occur before acquiring a VDI slot:

```go
switch ClassifySource(src) {
case SourceProtocolWeb:
    // Acquire VDI and run the existing web path.
case SourceProtocolEmail:
    // Run pkg/mail without VDI/browser dependencies.
case SourceProtocolNetwork:
    // Run the existing network-only path.
default:
    // Fail source validation explicitly.
}
```

Email concurrency should have its own bounded worker/semaphore limits rather
than borrowing VDI capacity. Per-provider/account limits prevent one mailbox
from exhausting all connector sessions.

### Runner boundary

`pkg/crawler` should depend on a narrow mail runner interface:

```go
type MailRunner interface {
    Reconcile(ctx context.Context, request mail.ReconcileRequest) (mail.Result, error)
}
```

The request supplies source identity, validated mail configuration, credential
resolver, state store, output/index sink, logger/metrics, and crawl budget. It
must not supply a WebDriver or browser-shaped `ProcessContext`.

The result reports provider-neutral counts such as discovered, fetched,
indexed, unchanged, deleted, skipped, quarantined, retried, and failed, plus
whether the mailbox is caught up. Detailed provider errors remain typed and
redacted inside `pkg/mail`.

### Source lifecycle

The email branch must use the same transport-neutral source lifecycle as other
crawlers:

- set processing/running state when accepted;
- enforce cancellation and maximum crawl duration;
- update heartbeat/progress during long reconciliations;
- mark success only when committed work and checkpoints are consistent;
- distinguish retryable interruption from permanent configuration/auth errors;
- always release leases, close sessions, decrement wait groups, and finalize
  source state;
- include `source_type: email` in completion/error event details without
  exposing endpoints containing credentials or message content.

A time-budgeted run may succeed while reporting `caught_up: false`; the next run
continues from the committed checkpoint. A run must fail or remain retryable if
it cannot durably record an outcome for work before the next cursor.

## Reliability and failure policy

Errors should be typed along these axes:

- **configuration/permanent:** invalid endpoint, unsupported provider, missing
  secret, rejected mailbox selection;
- **authentication/authorization:** expired credentials, insufficient scopes,
  mailbox access denied;
- **transient provider:** disconnect, timeout, rate limit, temporary server
  failure;
- **message-local:** malformed MIME, unsupported charset, policy size limit;
- **state/output:** lease loss, checkpoint conflict, database/index failure.

Transient provider failures are retried within the run budget. Authentication
and configuration failures stop the source without hot-looping. Message-local
failures do not necessarily stop the mailbox: after bounded retries they are
recorded as quarantined/skipped according to policy, allowing ordered progress.
State and output failures never advance the affected checkpoint.

On process crashes, already committed documents may be retried before the
checkpoint is advanced. Stable IDs and upserts make that safe. Exact-once
network execution is not required; effectively-once indexed results are.

## Security and privacy

Mail content and credentials are highly sensitive. The implementation must:

- resolve credentials as late as possible and keep them out of configuration
  snapshots, status JSON, metrics labels, traces, and errors;
- redact provider URLs, usernames, tokens, authentication headers, and raw
  server responses before logging;
- avoid logging subjects, addresses, body excerpts, attachment names, or
  message IDs by default;
- bound message bytes, decoded bytes, headers, MIME depth, part count,
  attachment count, and decompression/extraction work;
- treat HTML, filenames, content types, URLs, and headers as untrusted input;
- use read-only provider scopes where available;
- encrypt checkpoints or provider tokens at rest when they contain sensitive
  opaque cursor data;
- apply retention and access control consistently to normalized mail and raw
  attachment storage;
- expose explicit controls for whether raw RFC 5322 bytes are retained. The
  safe default is to retain only normalized output and required attachments.

## Observability

Use low-cardinality metrics keyed by provider class and outcome, not mailbox
names, email addresses, message IDs, or source endpoints. Target metrics
include:

- reconciliation runs, duration, and caught-up status;
- messages discovered, fetched, indexed, unchanged, deleted, skipped,
  quarantined, and failed;
- bytes fetched and attachment bytes accepted/rejected;
- provider requests, retries, throttles, reconnects, and authentication errors;
- listener connected state, reconnect count, hint count, and hint coalescing;
- checkpoint age, commit conflicts, resets, lease contention, and mailbox lag
  where the provider exposes a safe estimate.

Structured events should carry source ID, provider class, mailbox opaque ID or
hash, outcome, and safe error category. They should not carry message content
or secret-bearing provider details.

## Testing strategy

Tests should concentrate on package boundaries and recovery behavior.

### `pkg/mail` unit tests

- connector contract tests with fake sessions and provider error translation;
- IMAP UID, UIDVALIDITY reset, pagination, IDLE reconnect, and read-only
  command behavior;
- bounded fetches, cancellation, throttling, and partial stream failures;
- MIME corpus tests for nested multiparts, alternatives, related parts,
  encodings, charsets, malformed headers, MIME bombs, and unsafe filenames;
- static HTML sanitization proving scripts and active content are removed and
  remote resources are not fetched;
- stable identity and normalization golden tests;
- checkpoint compare-and-swap, lease expiry, poison-message outcomes, ordered
  commit barriers, and reset transitions;
- property/fuzz tests for MIME and header parsing with strict resource limits.

### Crawler integration tests

- email sources dispatch before VDI acquisition and never request a browser;
- web and network dispatch behavior remains unchanged;
- source type and scheme conflicts fail explicitly;
- all success, timeout, cancellation, and failure paths finalize source state
  and release resources;
- a crash after output commit but before checkpoint commit causes an idempotent
  upsert on retry;
- listener hints coalesce into reconciliation and polling still catches missed
  hints.

Live-provider tests should be optional and isolated from the default suite.
Unit and integration tests should use fake connectors and fixture messages, not
real mailbox credentials.

## Delivery sequence

1. Add typed `email` source configuration and schema validation, with secret
   references and limits.
2. Refactor source classification/dispatch so non-web sources are selected
   before VDI acquisition; preserve current web/network behavior with tests.
3. Introduce `pkg/mail` core types, connector/session interfaces, in-memory
   state store, fake provider, and runner contract.
4. Implement bounded fetch, MIME parsing, normalization, stable IDs, and an
   idempotent output sink using fixture-driven tests.
5. Implement the reconciler, ordered checkpoint commits, leases, reset/full
   reconciliation, and database-backed state.
6. Add the first read-only provider adapter (IMAPS), then scheduled polling.
7. Add listener/IDLE as a latency optimization after polling reconciliation is
   proven reliable.
8. Add metrics, redaction tests, operational documentation, and optional
   provider-specific adapters.

This order establishes safe dispatch and deterministic ingestion before adding
long-lived notification machinery.
