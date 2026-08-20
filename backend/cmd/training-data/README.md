# training-data CLI

Operator-only commands for the opt-in training-data asset pipeline. Run from
`backend/` with the same configuration and database credentials as Sub2API.

The CLI never overwrites output directories. `curate` and `bundle` create
immutable directories containing JSONL, provenance, a manifest and
`SHA256SUMS`; raw capture bodies are never copied into a curated or delivery
bundle.

```bash
go run ./cmd/training-data subject-ref --scope user --id 123

go run ./cmd/training-data rights-upsert \
  --scope user --id 123 \
  --contract contract-2026-001 \
  --dataset-types chat,code \
  --recipients buyer-a \
  --actor ops@example \
  --reason "signed training-data consent"

go run ./cmd/training-data curate \
  --input /secure/raw-export \
  --output /secure/curated/chat-2026-08-20 \
  --dataset-type chat

go run ./cmd/training-data bundle \
  --input /secure/curated/chat-2026-08-20 \
  --output /secure/delivery/buyer-a-release-001 \
  --release-id buyer-a-release-001 \
  --recipient buyer-a \
  --contract contract-2026-001 \
  --reviewed-by reviewer@example \
  --review-reference review-ticket-001

go run ./cmd/training-data verify \
  --input /secure/delivery/buyer-a-release-001
```

Withdrawing rights creates deletion targets by default:

```bash
go run ./cmd/training-data rights-withdraw \
  --scope user --id 123 \
  --actor ops@example \
  --reason "user withdrew consent" \
  --idempotency-key withdrawal-user-123-2026-08-20
```
