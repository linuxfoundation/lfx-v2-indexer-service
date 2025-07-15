#!/usr/bin/env sh
#
# Build and run the application.

set -e

go build -o bin/lfx-indexer .

export NATS_URL="nats://nats.lfx.svc.cluster.local:4222"
export OPENSEARCH_URL="http://opensearch-cluster-master.lfx.svc.cluster.local:9200"
export JWKS_URL="http://heimdall.lfx.svc.cluster.local:4457/.well-known/jwks"

./bin/lfx-indexer 