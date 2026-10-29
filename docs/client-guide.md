<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Client Guide: Indexing Resources in LFX

This guide explains how to send messages to the LFX Indexer Service to index your resources in OpenSearch.
It is a caller-facing walkthrough. The authoritative generic envelope,
`IndexingConfig`, OpenSearch document fields, event semantics, and schema
evolution policy live in [`docs/indexer-contract.md`](indexer-contract.md).

Existing unversioned publishers remain supported. Public audit envelope version
2 types/validators are available, but **the consumer rejects version 2 before
writing anything**. Do not start audited outbox delivery yet. The full contract
and enablement requirements live in [audit-v2-contract.md](audit-v2-contract.md).
Before deploying the discriminator reservation, follow the bounded source
inventory and staging checks in [publisher-compatibility.md](publisher-compatibility.md).

## Table of Contents

- [Overview](#overview)
- [Message Format](#message-format)
- [Audit Envelope Version 2](#audit-envelope-version-2)
- [Using indexing_config](#using-indexing_config)
- [Template Support](#template-support-in-indexing_config)
- [Field Reference](#field-reference)
- [Examples](#examples)
- [Best Practices](#best-practices)

## Overview

The LFX Indexer Service processes messages from NATS and indexes resources into OpenSearch for search and discovery. Clients should provide complete indexing metadata via the `indexing_config` field to ensure proper access control and search behavior.

## Message Format

Existing platform V2 messages use `IndexerMessageEnvelope` without setting the
new version/audit fields. Platform V2 (`lfx.index.*`) is not the same thing as
audit envelope version 2 (`schema_version: 2`).

```go
type IndexerMessageEnvelope struct {
    SchemaVersion  int                     `json:"schema_version,omitempty"` // Omit for existing messages; processing disabled
    Audit          *AuditEvent             `json:"audit,omitempty"`          // Required by envelope version 2
    Action         constants.MessageAction `json:"action"`                   // ActionCreated, ActionUpdated, or ActionDeleted
    Headers        map[string]string       `json:"headers"`                  // Authentication headers
    Data           any                     `json:"data"`                     // Object, or UID string for deletes
    Tags           []string                `json:"tags,omitempty"`           // Optional exact tags
    IndexingConfig *IndexingConfig         `json:"indexing_config,omitempty"` // Required for create/update; optional on legacy delete
}
```

### Publishing Messages

Publish to NATS subjects:

- **Object-specific**: `lfx.index.<object_type>` (e.g., `lfx.index.project`)

Do not add a version token to the resource subject suffix. A mixed-version
consumer fleet needs coordinated rollout or a separately provisioned isolated
audit stream; no new audited subject/subscription is provisioned in this release.

## Audit Envelope Version 2

This example shows the versioned **wire** format, not a message the current
consumer will process. `fixture.audit.v1` is illustrative/test-only, not an
approved production snapshot schema or authorization policy.

```json
{
  "schema_version": 2,
  "action": "updated",
  "headers": {
    "authorization": "Bearer <fresh-service-token>"
  },
  "data": {
    "uid": "fixture-resource",
    "name": "Updated Fixture"
  },
  "indexing_config": {
    "object_id": "fixture-resource",
    "access_check_object": "fixture_resource:fixture-resource",
    "access_check_relation": "viewer",
    "history_check_object": "project:fixture-project",
    "history_check_relation": "auditor"
  },
  "audit": {
    "event_id": "fixture-event-1",
    "publisher": "test-publisher",
    "occurred_at": "2026-10-29T16:00:00Z",
    "resource_revision": 42,
    "origin": "mutation",
    "attribution": {
      "actor_ref": "user:testuser01",
      "effective_actor_ref": "user:testuser01",
      "actor_profile": {
        "availability": "captured",
        "name": "Test User 1",
        "avatar_url": "https://example.com/avatar.png",
        "email": "user-1@example.com",
        "username": "testuser01"
      },
      "effective_actor_profile": {
        "availability": "captured",
        "name": "Test User 1",
        "avatar_url": "https://example.com/avatar.png",
        "email": "user-1@example.com",
        "username": "testuser01"
      }
    },
    "scope": {
      "before": ["project:fixture-project"],
      "after": ["project:fixture-project"]
    },
    "content": {
      "schema": "fixture.audit.v1",
      "representation": "snapshots",
      "before": {"name": "Previous Fixture"},
      "after": {"name": "Updated Fixture"}
    }
  }
}
```

- Commit immutable event data and the resource mutation in one PostgreSQL
  transaction. Do not persist the example's `headers` in the outbox; attach fresh
  service authentication when the relay dispatches it.
- Preserve event identity, source occurrence time/revision, attribution, scope,
  and approved snapshots on every retry. A new processing attempt is not a new
  business mutation.
- Capture the initiating/effective user's name, avatar URL, email, and username
  in the profile snapshots at source-operation time, not through a lookup during
  delivery or history reads. Equal refs require equal profiles. Each user profile
  explicitly includes all four attributes, using null when unavailable, and
  `availability: "captured"` for at least one captured attribute or `"unavailable"`
  when all are null. Service actors have no human profile. No live user account
  is required to interpret the recorded snapshot; later renames/deletion do not
  refresh it. Avatar URLs are not archived image bytes and must be credential-free,
  HTTPS, and query/fragment-free; use null when no approved stable URL is available.
- Actor profile snapshots are personal history data, not public search data or
  log context. History authorization, bounded retention, and erasure/replay
  controls must cover them before audited processing can be enabled.
- Creates require after scope/snapshot only; updates require both sides; deletes
  require before scope/snapshot only and matching UID/config identity. All audited
  deletes require complete concrete `IndexingConfig`; no legacy mismatch fallback.
- Use `types.DecodeV2Envelope` for untrusted JSON and `envelope.ValidateV2` for
  typed preflight, supplying an `AuditContentValidator` enforcing the registered
  schema's publisher/resource ownership and exact permitted fields. Nil validators
  reject mutations; no production registry is supplied here. Never use a permit-all
  callback or a sensitive-field denylist as a substitute.
- Structural preflight does not authenticate a publisher, authorize delegation or
  origin suppression, or enable storage. Current runtime admission rejects even
  a valid preflight result; do not treat it as a rollout-ready capability.
- Version 2 strings are literal and never template-expanded. Materialize intended
  substitutions before committing the outbox event. Free text/profile strings
  containing `{{ ... }}` remain valid prose; ID/auth references must be concrete.
- Projection-only origins (`reindex`, `backfill`, `sync_echo`) omit content and
  need explicit producer authorization. System-driven domain changes still use
  `mutation`. None of these versioned origins are processed yet.

The [owner contract](audit-v2-contract.md) defines strict parsing, numeric bounds,
snapshot rules, trust boundaries, deduplication, ordering, and rollout obligations.

## Using indexing_config

Create and update messages must include `indexing_config`. Legacy deletes may
omit it or supply complete deletion scope/history context: only matching config
context is retained, and a mismatched ID warns and falls back to the legacy
tombstone. Deletes replace the current document with a tombstone; they do not
remove a retained history record or create revision history. Audited deletes
have stricter requirements in the version 2 contract.

**Benefits:**

- Full control over indexing behavior
- Explicit access control configuration
- Works for any object type
- Consistent indexing across all clients

**Example:**

```json
{
  "action": "created",
  "headers": {
    "authorization": "Bearer <token>"
  },
  "data": {
    "uid": "proj-123",
    "name": "My Project",
    "slug": "my-project",
    "description": "A sample project"
  },
  "indexing_config": {
    "object_id": "proj-123",
    "public": true,
    "access_check_object": "project:proj-123",
    "access_check_relation": "viewer",
    "history_check_object": "project:proj-123",
    "history_check_relation": "historian",
    "sort_name": "my project",
    "name_and_aliases": ["My Project", "my-project"],
    "parent_refs": ["org:org-456"],
    "tags": ["uid:proj-123", "slug:my-project", "featured", "active"],
    "fulltext": "My Project - A sample project for demonstration"
  }
}
```

The server will:

- Use the provided config directly
- Set server-side fields: `latest`, timestamps, and principals
- Index the document with your exact specifications

### Template Support in `indexing_config`

To reduce message payload size and eliminate redundancy, `indexing_config` supports template variables that reference fields from the `data` object using `{{ field_name }}` syntax.

**Benefits:**

- **Reduced Payload Size**: Avoid repeating the same values multiple times
- **Single Source of Truth**: Data field values are the canonical source
- **Less Error-Prone**: No risk of mismatched values between data and config
- **Type Preservation**: Templates can preserve original data types

**Template Syntax:**

Templates use double curly braces: `{{ field_name }}`

**Features:**

1. **Simple Templates**: `"{{ uid }}"` - Entire field value (preserves type)
2. **Embedded Templates**: `"project:{{ uid }}"` - Template within a string
3. **Nested Fields**: `"{{ parent.id }}"` - Dot notation for nested objects
4. **Arrays**: `["{{ uid }}", "{{ parent_id }}"]` - Templates in arrays
5. **Multiple Templates**: `"{{ name }} - {{ description }}"` - Multiple in one string
6. **Escaping**: `"\{{ not_a_template }}"` - Use backslash to escape

**Example with Templates:**

```json
{
  "action": "created",
  "headers": {
    "authorization": "Bearer eyJhbGc..."
  },
  "data": {
    "uid": "proj-123",
    "name": "My Project",
    "slug": "my-project",
    "parent_id": "org-456",
    "metadata": {
      "organization": {
        "id": "org-789"
      }
    }
  },
  "indexing_config": {
    "object_id": "{{ uid }}",
    "public": true,
    "access_check_object": "project:{{ uid }}",
    "access_check_relation": "viewer",
    "history_check_object": "project:{{ uid }}",
    "history_check_relation": "historian",
    "sort_name": "{{ name }}",
    "name_and_aliases": ["{{ name }}", "{{ slug }}"],
    "parent_refs": ["org:{{ parent_id }}", "org:{{ metadata.organization.id }}"],
    "tags": ["uid:{{ uid }}", "slug:{{ slug }}"],
    "fulltext": "{{ name }} - Project in organization {{ metadata.organization.id }}"
  }
}
```

**Equivalent without templates** (notice the redundancy):

```json
{
  "action": "created",
  "headers": {
    "authorization": "Bearer eyJhbGc..."
  },
  "data": {
    "uid": "proj-123",
    "name": "My Project",
    "slug": "my-project",
    "parent_id": "org-456",
    "metadata": {
      "organization": {
        "id": "org-789"
      }
    }
  },
  "indexing_config": {
    "object_id": "proj-123",
    "public": true,
    "access_check_object": "project:proj-123",
    "access_check_relation": "viewer",
    "history_check_object": "project:proj-123",
    "history_check_relation": "historian",
    "sort_name": "My Project",
    "name_and_aliases": ["My Project", "my-project"],
    "parent_refs": ["org:org-456", "org:org-789"],
    "tags": ["uid:proj-123", "slug:my-project"],
    "fulltext": "My Project - Project in organization org-789"
  }
}
```

**Template Rules:**

1. **Field Resolution**: Templates reference fields in the `data` object
2. **Nested Access**: Use dot notation (e.g., `{{ parent.id }}`) for nested objects
3. **Type Preservation**: Simple templates (`"{{ field }}"`) preserve the original type
4. **String Conversion**: Embedded templates convert values to strings
5. **Error Handling**: Missing fields in required whole-value templates become empty
   and then fail required-field validation; missing fields in optional or embedded
   templates are skipped with a warning and blank substitution. Traversing through
   a non-object still fails processing.
6. **Escaping**: Use `\{{ }}` to include literal curly braces in values
7. **Whitespace**: Whitespace inside templates is trimmed (e.g., `{{  uid  }}` → `uid`)

**Type Preservation Example:**

```json
{
  "data": {
    "uid": "proj-123",
    "count": 42,
    "active": true
  },
  "indexing_config": {
    "object_id": "{{ uid }}",           // Becomes: "proj-123" (string)
    "sort_name": "Count: {{ count }}",  // Becomes: "Count: 42" (string - embedded)
    ...
  }
}
```

**Best Practices:**

- Use templates for repeated values (e.g., UIDs in multiple FGA fields)
- Use nested field access to avoid flattening your data structure
- Escape templates when you need literal `{{ }}` in your data
- Ensure referenced fields exist in `data` to avoid processing errors

## Field Reference

### Required Fields (All Messages)

| Field | Type | Description |
|-------|------|-------------|
| `action` | string | Operation type: `"created"`, `"updated"`, or `"deleted"` |
| `headers` | object | Authentication headers (must include `authorization` for V2) |
| `data` | object/string | Resource data (object for create/update, string ID for delete) |

### Top-Level Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tags` | array | No | Optional exact tags merged with `indexing_config.tags`. Prefer `indexing_config.tags` for new publishers. |
| `indexing_config` | object | Yes (create/update) | Indexing metadata controlling access control, search, and sort behavior. Not required for delete. |
| `schema_version` | integer | Omit for legacy | Explicit audit envelope version; only version 2 is defined, and runtime processing is disabled. |
| `audit` | object | Version 2 only | Immutable source event metadata and approved content; never silently downgraded or stored in current `data`. |

### IndexingConfig Fields

#### Required `indexing_config` Fields

| Field | Type | Description | Example |
|-------|------|-------------|---------|
| `object_id` | string | Unique identifier for the resource | `"proj-123"` |
| `access_check_object` | string | FGA object for access checks | `"project:proj-123"` |
| `access_check_relation` | string | FGA relation for access checks | `"viewer"` |
| `history_check_object` | string | FGA object for history checks | `"project:proj-123"` |
| `history_check_relation` | string | FGA relation for history checks | `"historian"` |

#### Optional `indexing_config` Fields

| Field | Type | Description | Example |
|-------|------|-------------|---------|
| `public` | boolean | Whether resource is publicly accessible | `true` |
| `sort_name` | string | Normalized name for sorting | `"my project"` |
| `name_and_aliases` | array | Names and aliases for search | `["My Project", "MP"]` |
| `parent_refs` | array | References to parent resources | `["org:org-456"]` |
| `tags` | array | Exact-match tags for filtering | `["uid:proj-123", "status:active"]` |
| `fulltext` | string | Full-text search content | `"searchable content"` |
| `contacts` | array | Contact information for the resource (see Contact structure below) | See example below |

#### Contact Structure

Each contact in the `contacts` array supports the following fields:

| Field | Type | Description | Example |
|-------|------|-------------|---------|
| `lfx_principal` | string | LFX principal ID for the contact | `"user:user-123"` |
| `name` | string | Display name of the contact | `"Test User 1"` |
| `emails` | array | Email addresses for the contact | `["user-1@example.com"]` |
| `bot` | boolean | Whether the contact is a bot account | `false` |
| `profile` | object | Additional profile information | `{"title": "Developer"}` |

**Example contacts array:**

```json
{
  "contacts": [
    {
      "lfx_principal": "user:user-123",
      "name": "Test User 1",
      "emails": ["user-1@example.com", "user-1-alt@example.com"],
      "bot": false,
      "profile": {
        "title": "Project Lead",
        "organization": "ACME Corp"
      }
    },
    {
      "name": "Test User 2",
      "emails": ["user-2@example.com"]
    }
  ]
}
```

**Note:** Contacts support template expansion, allowing you to reference fields from the `data` object:

```json
{
  "data": {
    "uid": "proj-123",
    "owner_id": "user-456",
    "owner_name": "Test User 1",
    "owner_email": "user-1@example.com"
  },
  "indexing_config": {
    "object_id": "{{ uid }}",
    "contacts": [
      {
        "lfx_principal": "{{ owner_id }}",
        "name": "{{ owner_name }}",
        "emails": ["{{ owner_email }}"]
      }
    ]
  }
}
```

### Server-Side Fields (Automatically Set)

These fields are **always set by the server** and should **not** be included in your message:

| Field | Description | Set By |
|-------|-------------|--------|
| `latest` | Boolean flag (always `true`) | Server |
| `created_at` | Timestamp for create actions | Server (from message timestamp) |
| `updated_at` | Timestamp for update actions | Server (from message timestamp) |
| `deleted_at` | Timestamp for delete actions | Server (from message timestamp) |
| `created_by` | Principal(s) who created the resource | Server (from auth headers) |
| `updated_by` | Principal(s) who updated the resource | Server (from auth headers) |
| `deleted_by` | Principal(s) who deleted the resource | Server (from auth headers) |
| `created_by_principals` | Principal IDs only | Server (extracted from tokens) |
| `updated_by_principals` | Principal IDs only | Server (extracted from tokens) |
| `deleted_by_principals` | Principal IDs only | Server (extracted from tokens) |
| `created_by_emails` | Email addresses only | Server (extracted from tokens) |
| `updated_by_emails` | Email addresses only | Server (extracted from tokens) |
| `deleted_by_emails` | Email addresses only | Server (extracted from tokens) |

## Examples

### Example 1: Create Resource

**NATS Subject:** `lfx.index.project`

```json
{
  "action": "created",
  "headers": {
    "authorization": "Bearer eyJhbGc..."
  },
  "data": {
    "uid": "proj-123",
    "name": "My Project",
    "slug": "my-project",
    "parent_id": "org-456"
  },
  "indexing_config": {
    "object_id": "proj-123",
    "public": true,
    "access_check_object": "project:proj-123",
    "access_check_relation": "viewer",
    "history_check_object": "project:proj-123",
    "history_check_relation": "historian",
    "sort_name": "my project",
    "name_and_aliases": ["My Project", "my-project"],
    "parent_refs": ["org:org-456"],
    "tags": ["uid:proj-123", "slug:my-project", "featured"],
    "fulltext": "My Project - A sample project"
  }
}
```

### Example 2: Update Resource

**NATS Subject:** `lfx.index.project`

```json
{
  "action": "updated",
  "headers": {
    "authorization": "Bearer eyJhbGc..."
  },
  "data": {
    "uid": "proj-123",
    "name": "Updated Project Name",
    "slug": "my-project"
  },
  "indexing_config": {
    "object_id": "proj-123",
    "public": false,
    "access_check_object": "project:proj-123",
    "access_check_relation": "viewer",
    "history_check_object": "project:proj-123",
    "history_check_relation": "historian",
    "sort_name": "updated project name",
    "name_and_aliases": ["Updated Project Name"]
  }
}
```

### Example 3: Delete Resource

**NATS Subject:** `lfx.index.project`

```json
{
  "action": "deleted",
  "headers": {
    "authorization": "Bearer eyJhbGc..."
  },
  "data": "proj-123"
}
```

For legacy deletes, `indexing_config` is optional. Providing a complete config
preserves matching scope/history context, but still does not create append-only
history. Top-level legacy tags and V1 data handling remains unchanged; do not
reuse those payloads as approved version 2 snapshots.

## Best Practices

### Access Control

1. **FGA Pattern**: Use the pattern `<type>:<id>#<relation>` for FGA queries
   - Example: `project:proj-123#viewer`

2. **Access vs History**: Distinguish between access and history permissions
   - `access_check_*`: Who can view the current state
   - `history_check_*`: Metadata for separately authorized history reads; no history endpoint or append-only store is currently implemented

3. **Public Flag**: Set appropriately to enable/disable public access
   - `true`: Resource is publicly searchable
   - `false`: Resource requires authorization

### Search Optimization

1. **sort_name**: Normalize for consistent sorting
   - Lowercase
   - Remove special characters
   - Example: "My Project!" → "my project"

2. **name_and_aliases**: Include all searchable variations
   - Full name
   - Short name
   - Common abbreviations
   - Slug/URL-friendly name

3. **parent_refs**: Maintain resource hierarchy
   - Use format: `<type>:<id>`
   - Example: `["org:org-456", "team:team-789"]`

4. **tags**: Add categorical tags
   - Keep tags concise
   - Use consistent naming conventions
   - Example: `["uid:proj-123", "status:active", "featured"]`

5. **fulltext**: Construct comprehensive search text
   - Include name, description, and other searchable content
   - Keep it concise but complete

### Authentication

1. **V2 Messages**: Always include `authorization` header

   ```json
   "headers": {
     "authorization": "Bearer <jwt-token>"
   }
   ```

2. **Token Requirements**:
   - Must be a valid JWT token
   - Validated against Heimdall service
   - Used to extract principal information for audit fields

### Error Handling

The deployed JetStream consumer does **not** return application `OK`/`ERROR`
results to publishers. Core `conn.Request()` on a captured subject can receive
a JetStream persistence acknowledgment, not a processing result. A durable
outbox relay uses JetStream publish and waits for `PubAck` before recording
delivery. Processing failures are NAKed/retried independently; monitor exhausted
deliveries and reconcile from durable source events. Do not publish version 2
yet: its admission rejection exhausts the existing bounded retry budget.

Common errors:

- Missing required `indexing_config` fields
- Invalid action type
- Missing authorization header
- Malformed FGA queries
- Reserved audit/version fields on a legacy message
- Unsupported version or version 2 processing not enabled

### Performance Considerations

1. **Message Size**: `indexing_config` increases payload size
   - Average overhead: ~500-1000 bytes
   - Consider compression for high-volume scenarios

2. **Processing Speed**: `indexing_config` is required and drives all indexing decisions
   - Reduced server CPU usage

3. **Batch Operations**: Use NATS queue groups for load balancing
   - Service uses queue group: `lfx.indexer.queue`
   - Messages distributed across instances

## Go Client Example

For Go services, use the public types package:

```go
import (
    "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
    "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// Create message with indexing config
publicFlag := true
envelope := &types.IndexerMessageEnvelope{
    Action: constants.ActionCreated,
    Headers: map[string]string{
        constants.AuthorizationHeader: "Bearer " + token,
    },
    Data: map[string]any{
        "id":   "proj-123",
        "name": "My Project",
    },
    IndexingConfig: &types.IndexingConfig{
        ObjectID:             "proj-123",
        Public:               &publicFlag,
        AccessCheckObject:    "project:proj-123",
        AccessCheckRelation:  "viewer",
        HistoryCheckObject:   "project:proj-123",
        HistoryCheckRelation: "historian",
        SortName:             "my project",
        NameAndAliases:       []string{"My Project"},
        Tags:                 []string{"uid:proj-123", "featured"},
    },
}

// Marshal and publish to NATS
data, _ := json.Marshal(envelope)
nc.Publish("lfx.index.project", data)
```

## Troubleshooting

### Issue: Document missing server-side fields

**Problem**: `latest`, timestamp, or principal fields are missing in OpenSearch.

**Solution**: Confirm successful processing in the consumer's operational evidence
and the indexed result; a publish acknowledgment is not processing confirmation.
Create/update documents get `created_at` or `updated_at`; delete documents get
`deleted_at`. Principal fields require a valid lower-case `authorization` header
on V2 messages or `x-username` / `x-email` on V1 messages.

### Issue: "indexing_config is required" error

**Problem**: Sending create/update messages without an `indexing_config` block.

**Solution**: Add `indexing_config` to your message. The indexer is data-agnostic and requires clients to provide complete indexing metadata.

### Issue: Access control not working

**Problem**: Users can't access indexed resources.

**Solution**: Verify FGA configuration:

- Check `access_check_object` matches your FGA object pattern
- Verify `access_check_relation` is configured in FGA
- Ensure `public` flag is set correctly

### Issue: Search not finding resources

**Problem**: Resources don't appear in search results.

**Solution**: Improve searchability fields:

- Add comprehensive `name_and_aliases`
- Set meaningful `fulltext` content
- Add relevant `tags`
- Verify `public` flag for public searches

## Additional Resources

- [Architecture Overview](../CLAUDE.md) - System architecture and design patterns
- [README](../README.md) - Project overview and setup instructions
- [Audit envelope version 2](audit-v2-contract.md) - Public contract, current rejection, and enablement requirements
- [History storage design](audit-history-design.md) - Remaining persistence/recovery work
- [OpenFGA Documentation](https://openfga.dev/) - Fine-Grained Authorization reference
