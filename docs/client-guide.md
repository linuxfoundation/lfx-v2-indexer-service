# Client Guide: Indexing Resources in LFX

This guide explains how to send messages to the LFX Indexer Service to index your resources in OpenSearch.

## Table of Contents

- [Overview](#overview)
- [Message Format](#message-format)
- [Two Approaches to Indexing](#two-approaches-to-indexing)
  - [Approach 1: Server-Side Enrichment](#approach-1-server-side-enrichment-default)
  - [Approach 2: Client-Provided Configuration](#approach-2-client-provided-configuration-indexing_config)
- [Field Reference](#field-reference)
- [Examples](#examples)
- [Best Practices](#best-practices)

## Overview

The LFX Indexer Service processes messages from NATS and indexes resources into OpenSearch for search and discovery. There are two ways to index your resources:

1. **Server-Side Enrichment**: Send minimal data and let the server compute indexing metadata
2. **Client-Provided Configuration**: Send complete indexing metadata via `indexing_config` to bypass enrichers

## Message Format

All indexing messages follow the `IndexerMessageEnvelope` structure:

```go
type IndexerMessageEnvelope struct {
    Action         string                 // "created", "updated", or "deleted"
    Headers        map[string]string      // Authentication headers
    Data           any                    // Resource data (map or string for deletes)
    Tags           []string               // Optional: fields to index as tags
    IndexingConfig *IndexingConfig        // Optional: pre-computed indexing metadata
}
```

### Publishing Messages

Publish to NATS subjects:

- **Object-specific**: `lfx.index.<object_type>` (e.g., `lfx.index.project`)

## Two Approaches to Indexing

### Approach 1: Server-Side Enrichment (Default)

Send your resource data and let the server handle indexing metadata via registered enrichers.

**Pros:**

- Simpler message payloads
- Server handles access control logic
- Centralized enrichment rules

**Cons:**

- Requires server-side enricher implementation for your object type
- Less control over indexing behavior
- Potential performance overhead from server-side computation

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
    "public": true,
    "description": "A sample project"
  },
  "tags": ["uid", "slug"]
}
```

The server will:

- Use the `project` enricher to compute access control fields
- Extract `object_id` from `uid`
- Build FGA (Fine-Grained Authorization) queries
- Set searchability fields

### Approach 2: Client-Provided Configuration (`indexing_config`)

Provide complete indexing metadata to bypass server-side enrichers and have full control.

**Pros:**

- No server-side enricher required
- Full control over indexing behavior
- Better performance (no server-side computation)
- Works for any object type

**Cons:**

- Larger message payloads
- Client must compute access control metadata
- Client responsible for correctness

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
  "tags": ["uid", "slug"],
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
    "tags": ["featured", "active"],
    "fulltext": "My Project - A sample project for demonstration"
  }
}
```

The server will:

- Use the provided config directly (no enricher called)
- Set server-side fields: `latest`, timestamps, and principals
- Index the document with your exact specifications

## Field Reference

### Required Fields (All Messages)

| Field | Type | Description |
|-------|------|-------------|
| `action` | string | Operation type: `"created"`, `"updated"`, or `"deleted"` |
| `headers` | object | Authentication headers (must include `authorization` for V2) |
| `data` | object/string | Resource data (object for create/update, string ID for delete) |

### Optional Top-Level Fields

| Field | Type | Description |
|-------|------|-------------|
| `tags` | array | Field names from `data` to index as searchable tags |
| `indexing_config` | object | Pre-computed indexing metadata (see below) |

### IndexingConfig Fields

#### Required (when using `indexing_config`)

| Field | Type | Description | Example |
|-------|------|-------------|---------|
| `object_id` | string | Unique identifier for the resource | `"proj-123"` |
| `access_check_object` | string | FGA object for access checks | `"project:proj-123"` |
| `access_check_relation` | string | FGA relation for access checks | `"viewer"` |
| `history_check_object` | string | FGA object for history checks | `"project:proj-123"` |
| `history_check_relation` | string | FGA relation for history checks | `"historian"` |

#### Optional (when using `indexing_config`)

| Field | Type | Description | Example |
|-------|------|-------------|---------|
| `public` | boolean | Whether resource is publicly accessible | `true` |
| `sort_name` | string | Normalized name for sorting | `"my project"` |
| `name_and_aliases` | array | Names and aliases for search | `["My Project", "MP"]` |
| `parent_refs` | array | References to parent resources | `["org:org-456"]` |
| `tags` | array | Additional static tags for the document | `["featured", "active"]` |
| `fulltext` | string | Full-text search content | `"searchable content"` |

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

### Example 1: Create with Server-Side Enrichment

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
    "public": true,
    "parent_id": "org-456"
  },
  "tags": ["uid", "slug"]
}
```

### Example 2: Create with Client-Provided Configuration

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
  "tags": ["uid", "slug"],
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
    "tags": ["featured"],
    "fulltext": "My Project - A sample project"
  }
}
```

### Example 3: Update with Configuration

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

### Example 4: Delete Operation

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

Note: For delete operations, `indexing_config` is not needed. The server only requires the resource ID.

## Best Practices

### When to Use `indexing_config`

✅ **Use `indexing_config` when:**

- Your object type doesn't have a server-side enricher
- You need precise control over access control queries
- You want to optimize performance by pre-computing metadata
- You're building a new service and want to control indexing behavior

❌ **Avoid `indexing_config` when:**

- A server-side enricher already exists and works well
- You want centralized access control logic
- You prefer simpler message payloads

### Access Control Best Practices

1. **FGA Pattern**: Use the pattern `<type>:<id>#<relation>` for FGA queries
   - Example: `project:proj-123#viewer`

2. **Access vs History**: Distinguish between access and history permissions
   - `access_check_*`: Who can view the current state
   - `history_check_*`: Who can view the change history

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
   - Example: `["featured", "active", "verified"]`

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

The indexer service will reply with:

- `"OK"` on success
- `"ERROR: <details>"` on failure

Common errors:

- Missing required `indexing_config` fields
- Invalid action type
- Missing authorization header
- Malformed FGA queries

### Performance Considerations

1. **Message Size**: `indexing_config` increases payload size
   - Average overhead: ~500-1000 bytes
   - Consider compression for high-volume scenarios

2. **Processing Speed**: `indexing_config` bypasses enrichers
   - Faster processing (no enricher lookup/execution)
   - Reduced server CPU usage

3. **Batch Operations**: Use NATS queue groups for load balancing
   - Service uses queue group: `lfx.indexer.queue`
   - Messages distributed across instances

## Go Client Example

For Go services, use the public types package:

```go
import (
    "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
    "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// Create message with indexing config
publicFlag := true
envelope := &types.IndexerMessageEnvelope{
    Action: constants.ActionCreated,
    Headers: map[string]string{
        "authorization": "Bearer " + token,
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
        Tags:                 []string{"featured"},
    },
}

// Marshal and publish to NATS
data, _ := json.Marshal(envelope)
nc.Publish("lfx.index.project", data)
```

## Troubleshooting

### Issue: Document missing server-side fields

**Problem**: `latest`, `created_at`, or principal fields are missing in OpenSearch.

**Solution**: This was a bug fixed in recent versions. Ensure you're using the latest indexer service version. These fields are always set by the server, regardless of whether you use `indexing_config`.

### Issue: "No enricher found" error

**Problem**: Sending messages without `indexing_config` for unsupported object types.

**Solution**: Either:

1. Add `indexing_config` to your message
2. Request a server-side enricher implementation for your object type

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
- [OpenFGA Documentation](https://openfga.dev/) - Fine-Grained Authorization reference
