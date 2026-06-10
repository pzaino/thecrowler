# Email crawling target architecture

## Status and scope

This document defines the target architecture for crawling mailboxes as CROWler
sources. It covers the `pkg/mail` provider boundary, connector, fetcher, MIME
parser, normalizer, listener, reconciler, state store, and integration with the
crawler scheduler and indexing pipeline.

This is an architectural target, not a statement that these components already
exist. The MVP should support incremental, read-only mailbox ingestion without
allocating a browser or VDI session.

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

Email sources should be explicit and typed:

```yaml
crawling_config:
  source_type: email
email:
  provider: imap
  endpoint: imaps://mail.example.com:993
  credential_ref: secret/mail-archive
  mailboxes:
    include: [INBOX, Archive]
    exclude: [Spam, Trash]
  mode: poll
  limits:
    max_message_bytes: 26214400
    max_attachment_bytes: 10485760
    max_total_attachment_bytes: 26214400
    max_attachments: 50
    max_embedded_message_depth: 3
```

The exact serialized schema may evolve, but it must preserve these boundaries:

- `source_type: email` declares intent. A recognized retrieval scheme may help
  validation, but should not silently turn an unrelated source into email.
- The endpoint contains no password, OAuth token, or client secret.
- `credential_ref` resolves through the project's secret mechanism. Secret
  material must not be persisted in source status, checkpoints, logs, events,
  or indexed documents.
- Mailbox inclusion/exclusion, retrieval mode, TLS requirements, timeouts, and
  resource limits are typed fields, not provider-specific arbitrary maps where
  avoidable.
- TLS verification is enabled by default. Any insecure development override
  must be explicit and must produce an operational warning.
- SMTP URLs are invalid as crawl sources.

Configuration changes that alter endpoint, account, provider, or mailbox
selection invalidate incompatible checkpoints and trigger a controlled
reconciliation rather than reusing state blindly.

## Core data model

The provider-neutral model separates provider metadata, raw content, parsed
MIME, and normalized output.

```go
type Mailbox struct {
    ID   string
    Name string
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
mailbox reconciliation. POP3 is not part of the initial target unless a stable
identity and deletion policy are designed explicitly.

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

Attachments should be emitted as child records or blobs through an explicit
index/storage interface. General text extraction from an attachment may be a
later pipeline stage, but raw attachment bytes must never be passed through the
web crawler merely to reuse page processing.

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
configuration. Checkpoints must not be rewritten into source JSON because that
mixes operator intent with worker-owned mutable state and makes atomic progress
hard to guarantee.

```go
type StateStore interface {
    AcquireLease(ctx context.Context, key MailboxKey, owner string, ttl time.Duration) (Lease, error)
    LoadCheckpoint(ctx context.Context, key MailboxKey) (Checkpoint, error)
    CommitCheckpoint(ctx context.Context, lease Lease, previous Version, next Checkpoint) error
    RecordOutcome(ctx context.Context, lease Lease, outcome MessageOutcome) error
    ReleaseLease(ctx context.Context, lease Lease) error
}
```

The persistent model should include:

- source, provider account, and mailbox identities;
- provider cursor/history token or IMAP UIDVALIDITY and last committed UID;
- checkpoint schema/config fingerprint and optimistic-lock version;
- lease owner and expiration;
- last successful reconciliation and listener health timestamps;
- bounded failure/quarantine metadata needed to prevent poison-message loops;
- optional per-message version/content hash when the provider cursor cannot
  fully express updates and deletions.

Checkpoint commits use compare-and-swap or an equivalent transaction so stale
workers cannot overwrite newer progress. Leases prevent routine concurrent
runs, while idempotent document writes and checkpoint versioning protect
against lease expiry, worker crashes, and duplicate delivery.

Database migrations and SQL implementations can live in `pkg/database`, but
`pkg/mail` defines the `Checkpoint`, `MessageOutcome`, reset, and transition
semantics. An in-memory implementation should support deterministic unit tests.

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
