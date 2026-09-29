---
description: Indexer contract policy for action constants, envelope shape, and OpenSearch field coordination
paths:
  - 'internal/domain/contracts/**'
  - 'internal/domain/services/indexer_service.go'
  - 'internal/infrastructure/storage/**'
  - 'pkg/types/**'
  - 'pkg/constants/**'
  - 'docs/indexer-contract.md'
---

# Indexer contract policy

- Use action constants from `pkg/constants`. Never inline string literals for action values at call sites.
- Do not mutate existing envelope fields in place. Add new fields additively and version the envelope when shape changes.
- Do not introduce new top-level OpenSearch fields without coordinating with `lfx-v2-query-service`, which owns the read-side schema.
- The canonical indexer contract lives in `lfx-v2-indexer-service/docs/indexer-contract.md`. Treat that document as the source of truth for envelope, actions, and field semantics.
