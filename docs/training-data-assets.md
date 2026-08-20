# Training-data asset pipeline

This is an opt-in, fail-open pipeline for capturing authenticated inference
traffic and turning eligible text/code conversations into governed dataset
artifacts. It is disabled by default and is not a website analytics, billing,
usage-reporting or operational-log feature.

## Scope

Captured routes are limited to authenticated inference POST requests:

- Anthropic Messages
- OpenAI Responses
- OpenAI Chat Completions
- Alpha Search
- Gemini `generateContent` and `streamGenerateContent`

Model discovery, dashboards, admin APIs, billing, usage pages, image/video
generation and task/status APIs are excluded. Internal account IDs and group
IDs are not written to capture manifests or the training-data schema.

## Data flow

```text
authenticated inference request
        │
        ├─ original bytes continue through the gateway unchanged
        │
        └─ redacted request/header copy + response stream
                    │
                    ▼
           bounded in-memory queue
                    │
                    ▼
          private local zstd spool
                    │
                    ▼
       encrypted private raw object store
                    │
                    ▼
 current rights check + curation/redaction
                    │
                    ▼
 immutable JSONL/provenance bundle
                    │
                    ▼
 current rights check + recipient filter
                    │
                    ▼
immutable buyer delivery bundle
```

Each accepted capture keeps four correlated views under one `capture_id`:

1. the sanitized archival copy of the client request;
2. every replayable request actually sent to an upstream attempt;
3. the upstream response headers plus the decoded JSON/SSE bytes consumed by
   the gateway; and
4. the exact response bytes written back to the client.

For streaming traffic, byte offsets, lengths and relative timestamps are
checkpointed alongside the response bodies. A restart or disk failure never
silently upgrades a partial record to complete: recovery reconciles persisted
files, marks missing timing tails with `at_ms=-1`, and records an incomplete
reason.

Capture failures never fail a model request. Queue/spool exhaustion marks a
capture incomplete or skips it. The default byte limits are 64 MiB queued in
memory, 4 GiB on disk and 32 MiB for a single captured request body. Queue
reservations include events currently being written, so parallel writer shards
cannot evade the aggregate byte gate.

The current capture candidate is intentionally text/code-only. Invalid JSON,
opaque binary bodies, images, audio, video, model discovery and asynchronous
media task APIs are excluded rather than copied into a training corpus without
a protocol-specific policy. External URLs are recorded only when they occur in
the request JSON; the capture path does not download third-party content.

## Privacy and credentials

- User and API-key numeric IDs are converted to scoped HMAC-SHA-256 references.
- The HMAC key must be random, at least 32 bytes and stable for the lifetime of
  existing rights/captures. Rotation requires a deliberate reference migration.
- Authorization, cookies, API keys, identity/IP headers, common standalone
  tokens and PEM private keys are redacted from stored request/header copies.
- Raw response content remains restricted to the raw vault because a model can
  echo sensitive data. Curation applies a second privacy/credential pass.
- Curated and delivery bundles never contain raw capture files, system turns,
  developer turns or tool-result turns.
- Curation requires a successful 2xx upstream attempt and a known upstream
  model; local gateway responses and unattributed content are excluded.
- Automated email/phone/credential redaction is a first-pass control, not a
  claim of anonymization. Curated manifests require human review, and buyer
  bundles cannot be created without a reviewer and review reference.

## Rights precedence

Capture is allowed only when the current user or API-key grant is `eligible`
and includes `model_training`. A user-level or key-level `withdrawn`,
`excluded`, `expired` or `legal_hold` grant vetoes capture. Curation also
requires an allowed dataset type and at least one allowed recipient. Delivery
re-checks the current ledger and filters to the named recipient and purpose, so
a withdrawal after capture prevents later delivery.

## Configuration

See `deploy/config.example.yaml` under `training_data`. Credentials belong in
the server environment or another root-only secret source and must not be
committed. Enabling requires:

- `subject_hmac_key`
- a private `raw_store` bucket and credentials
- server-side encryption (`AES256` or `aws:kms`)
- a writable private spool directory
- an absolute spool path backed by a dedicated persistent mount/partition
- queue/spool capacity and monitoring appropriate for traffic

Curated and delivery object stores are reserved for later upload/finalization;
the current CLI creates immutable local bundles first so they can be reviewed
and checksum-verified before any external delivery.

The long-term storage zones are deliberately separate:

- **Capture vault:** private S3-compatible storage for sanitized archival
  request/response material and provenance evidence. Buyers never receive
  access to this bucket.
- **Curated lake:** versioned, normalized datasets. The current pilot emits
  JSONL plus provenance and checksums; production scale still requires
  Parquet+Zstandard partitioning and a catalog/high-watermark process.
- **Buyer delivery zone:** one immutable prefix, encryption boundary and
  `release_id` per recipient and contract. Direct access to the capture vault
  is prohibited.

## Operator CLI

The CLI lives at `backend/cmd/training-data`. From `backend/`:

```bash
go run ./cmd/training-data help
```

Core workflow:

1. Derive a subject reference.
2. Record an eligible grant with contract, dataset types and recipients.
3. Export raw capture directories into a secure offline work area.
4. Run `curate`; inspect `manifest.json`, `dataset-card.md` and exclusions.
5. Complete privacy, quality and code-license sampling, then run `bundle` for
   one recipient/contract with `--reviewed-by` and `--review-reference`.
6. Run `verify` before transfer.

Both `curate` and `bundle` fail if the destination already exists. Outputs use
private permissions and include `SHA256SUMS`.

## Withdrawal and deletion boundary

`rights-withdraw` increments the append-only rights ledger, stamps
`revoked_at`, and by default creates idempotent deletion targets for raw,
spool, prompt-audit, curated-release, buyer-release and buyer-notice scopes.

Creating these target rows is not proof that deletion has completed. This
candidate does not yet include an automatic executor capable of rebuilding
aggregated curated shards, deleting all external buyer copies or proving buyer
notification. A production rollout must remain blocked until each applicable
target has an executor, retry/lease behavior, legal-hold handling and evidence,
and an end-to-end withdrawal drill has completed. Never mark a request
`completed` merely because its target rows exist.

## three.js boundary

three.js is a future visualization client, not a storage format. It may render
metadata-only graphs such as client request → upstream attempts → client
response, dataset lineage, model/language distributions and withdrawal impact.
It must not fetch prompts or model outputs directly, and it is not part of the
current backend candidate.

## Build and rollout boundary

Reliable upstream payload capture requires these backend source changes and a
new Sub2API image. The website/frontend does not need to be rebuilt for capture
itself. Production remains on the official image until a separately authorized
rollout completes the protected image pipeline, database/Compose backup,
private-bucket checks, migration review, limited rights grant, monitoring and
rollback rehearsal.

## Delivery roadmap

1. **Rights and schemas:** migration, scoped HMAC references, append-only rights
   events, immutable local artifacts and withdrawal target creation.
2. **Capture pilot:** authenticated inference-only capture, bounded spool,
   private raw S3 upload, restart recovery and fake-upstream tests.
3. **Dataset production:** streaming/batched curation at scale, Parquet,
   stronger PII/code-license classifiers, deduplication, review reports and
   release registration.
4. **Controlled delivery:** customer-specific encrypted upload, access audit,
   buyer notification/deletion evidence and a small single-customer pilot.
5. **Visualization:** metadata-only APIs and optional three.js lineage views.

As of 2026-08-20, stages 1 and the local/offline portion of stage 2 are the
implemented candidate. Automatic deletion execution, Parquet/catalog output,
curated/delivery S3 publication, buyer access auditing, alerts and three.js are
explicit production blockers or later stages, not completed features.

## Production gate

Do not enable or deploy this feature without explicit production authorization.
Required gates include database and Compose backups, private bucket and
encryption verification, a stable HMAC key, least-privilege test grants,
spool/queue alerts, capture completeness checks, curation review, withdrawal
and deletion drills, and the normal protected image pipeline. No manual
rsync/build or direct production container edits are permitted.
