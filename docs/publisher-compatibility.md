<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Audit discriminator compatibility inventory

This is **read-only source evidence**, not a production traffic audit. It checks
whether reserving top-level `schema_version` and `audit` conflicts with the known
local publisher implementations. It does not prove which revisions are deployed
or cover external clients, untracked code, runtime-generated envelope keys, or
historic messages still retained in a stream.

## Source snapshot inspected

The listed publisher files matched each recorded checkout's HEAD when inspected
on 2026-10-29. No peer files were modified by this inventory.

| Repository / revision | Primary envelope construction evidence | Result |
| --- | --- | --- |
| `lfx-v2-project-service` / `03cab43bccafebed1a63024af4a416827bc293d7` | `internal/infrastructure/nats/message.go`, service operations/subscribers, CLI reindex/audit-user sync, root-setup/admin-delete scripts | Keyed SDK envelopes set only legacy fields |
| `lfx-v2-committee-service` / `6d77101225c7ea1028d2ac2116560c0ec7128554` | `internal/domain/model/committee_message.go`, writers, writer/auditor migration script | Local DTO uses `action`, `headers`, `data`, `tags`, `indexing_config` |
| `lfx-v2-member-service` / `fdb7357e739922a12d6e6d5822f9a6ef3ad847bb` | `internal/domain/model/member_message.go`, `internal/service/messaging.go` | Same five-field local DTO; resource input is nested under `data` |
| `lfx-v2-mailing-list-service` / `55efd19179d7761b5f9ff15b3b29d3df8d3ebb5d` | `internal/domain/model/message.go`, datastream/domain propagation callers | Same five-field local DTO; resource input is nested under `data` |
| `lfx-v2-meeting-service` / `2fbe68b7cd6225bbad5d57d1b4fd1e953ecf706d` | `internal/infrastructure/eventing/nats_publisher.go`, host-credential backfill script | SDK envelopes or explicit legacy DTOs, with no reserved audit discriminator |
| `lfx-v2-survey-service` / `68f8b50bebb721b92fe80086e8b9ba62efcd2e0b` | `internal/infrastructure/eventing/nats_publisher.go` | Create/update/delete SDK envelopes omit both fields |
| `lfx-v2-voting-service` / `753688b739915b827b8f9356e66cbcd415f0f1bb` | `internal/infrastructure/eventing/nats_publisher.go` | Create/update/delete SDK envelopes omit both fields |
| `lfx-v2-formation-service` / `3150ecd71f4de64304ced1d8d4376b8b78992e30` | `internal/infrastructure/nats/indexer_publisher.go` | Four-field narrowed upsert DTO; SDK deletes omit both fields |
| `lfx-v1-sync-helper` / `792519c9c2953faf29d4bbcbbb98e76086dca232` | `AGENTS.md`, `cmd/lfx-v1-sync-helper/client_projects.go`, `client_committees.go` | No direct indexing-subject publisher found; project/committee writes call their APIs, whose publishers are listed above |

The source search and Go AST inspection covered non-generated, non-test Go
publisher code in those checkouts, including local indexer DTO tags, keyed SDK
initializers, and literal action/data envelope maps. The SDK initializers set
only `Action`, `Headers`, `Data`, `Tags`, and `IndexingConfig`. Searches found no
reserved top-level JSON field declarations in the inspected producers. This is
not a claim about arbitrary dynamic maps or every possible NATS publisher.

No collision was found in these known source paths. An upstream record field
named `audit` or `schema_version` remains safe **inside** `data` or `v1_data`:
the reservation applies only to envelope-level discriminator keys. A regression
test exercises successful legacy processing with both names nested in resource
data. The new SDK fields are `omitempty`, so leaving them unset does not add
zero/null discriminator fields to old envelopes.

## Required deployment verification

Before deploying the reservation guard:

1. Identify deployed publishers and operational/backfill clients for both
   `lfx.index.*` and `lfx.v1.index.*`. Compare their actual deployed source/build
   revisions with the inventory; inspect any additional producer before rollout.
2. Verify representative staging messages by their **envelope key names only**.
   Include retained/backlogged legacy messages and platform V1 traffic, not just
   freshly generated platform V2 messages. Do not copy raw payloads, headers,
   tokens, actor identifiers, or resource data into logs or review evidence.
3. If an existing custom publisher uses either name at envelope level, stop the
   guard rollout until it migrates that metadata under `data` or an isolated audit
   ingress strategy is agreed. Do not silently drop audit requirements to make
   the reservation appear backward-compatible.
4. Canary the guarded consumer and monitor admission failures by subject/reason.
   The existing JetStream failure log includes `error`, `subject`, and `consumer`
   before NAK/backoff (`internal/infrastructure/messaging/messaging_repository.go`).
   No new payload logging is needed. Investigate collisions before the bounded
   delivery/retention window expires; retain a durable repair source.

Keep audited publisher delivery disabled regardless of this inventory until
the [version 2 processing prerequisites](audit-v2-contract.md) are implemented.
