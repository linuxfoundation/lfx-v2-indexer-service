# Access Query Fields Migration Script

This script migrates existing OpenSearch documents to add the new `access_check_query` and `history_check_query` fields that were recently added as part of [PR #20](https://github.com/linuxfoundation/lfx-v2-indexer-service/pull/20).

## Background

The LFX indexer service was recently updated to include new document fields for Fine-Grained Authorization (FGA) of documents via the [query service](https://github.com/linuxfoundation/lfx-v2-query-service):

- `access_check_query`: Combination of `access_check_object` + "#" + `access_check_relation`
- `history_check_query`: Combination of `history_check_object` + "#" + `history_check_relation`

These fields are automatically populated for newly indexed documents (implemented in [PR #20](https://github.com/linuxfoundation/lfx-v2-indexer-service/pull/20)), but existing documents need to be migrated.

## Usage

### Basic Usage

```bash
# Run in dry-run mode to see what would be changed
DRY_RUN=true go run scripts/migration/001_add_access_query_fields/main.go

# Run the actual migration
go run scripts/migration/001_add_access_query_fields/main.go
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `OPENSEARCH_URL` | `http://localhost:9200` | OpenSearch cluster URL |
| `OPENSEARCH_INDEX` | `resources` | Target index name |
| `BATCH_SIZE` | `100` | Number of documents to process per batch |
| `DRY_RUN` | `false` | If true, only log what would be updated without making changes |
| `SCROLL_TIMEOUT` | `5m` | Scroll context timeout |

## Safety Features

- **Dry Run Mode**: Use `DRY_RUN=true` to preview changes without applying them
- **Idempotent**: Safe to run multiple times - skips documents that already have the new fields
- **Graceful Shutdown**: Responds to SIGINT/SIGTERM signals
- **Progress Tracking**: Shows detailed progress and statistics
- **Error Handling**: Continues processing even if individual batches fail

## Migration Logic

The script:

1. Searches for documents that have access control fields but are missing the new query fields
2. For each document, constructs the query fields only if both object and relation are non-empty
3. Updates documents in batches using the OpenSearch bulk API
4. Provides detailed statistics and progress reporting

### Query Construction Rules

- `access_check_query` is created only if both `access_check_object` and `access_check_relation` are non-empty
- `history_check_query` is created only if both `history_check_object` and `history_check_relation` are non-empty
- Format: `{object}#{relation}` (e.g., `committee:abc123#viewer`)

## Example Output

```text
Starting access query fields migration...
=== Migration Configuration ===
  OpenSearch URL: http://opensearch:9200
  Index Name: resources
  Batch Size: 100
  Dry Run: false
  Scroll Timeout: 5m0s
==============================
✓ Connected to OpenSearch successfully
Searching for documents that need migration...
Found 1250 documents that may need migration

Processing batch 1 (100 documents)...
Progress: 100/1250 documents (8.0%)

Processing batch 2 (100 documents)...
Progress: 200/1250 documents (16.0%)
...

=== Migration Statistics ===
Total Documents Found: 1250
Documents Processed: 1250
Documents Updated: 987
Documents Skipped: 263
Documents with Errors: 0
Duration: 45.6s
Processing Rate: 27.4 docs/sec
============================

✓ Migration completed successfully!
```

## Troubleshooting

### Connection Issues

- Verify OpenSearch is running and accessible
- Check the `OPENSEARCH_URL` environment variable
- Ensure network connectivity and authentication if required

### Performance Tuning

- Increase `BATCH_SIZE` for faster processing of large datasets
- Adjust `SCROLL_TIMEOUT` if processing very large result sets
- Monitor OpenSearch cluster performance during migration

### Partial Failures

- The script continues processing even if individual batches fail
- Check the error logs for specific failure reasons
- Re-run the script to retry failed documents (it's idempotent)

## Testing

Always test in a non-production environment first:

1. Run with `DRY_RUN=true` to preview changes
2. Test with a small `BATCH_SIZE` initially
3. Verify the query fields are constructed correctly
4. Check that no data is corrupted

## Technical Details

- Uses OpenSearch scroll API for efficient processing of large result sets
- Bulk updates for optimal performance
- Only fetches necessary fields to minimize network transfer
- Implements proper signal handling for graceful shutdown
- Comprehensive error handling and statistics tracking
