#!/usr/bin/env sh
# Copyright The Linux Foundation and each contributor to LFX.
# SPDX-License-Identifier: MIT
#
# Build and run the application.

set -e

go build -o bin/lfx-indexer ./cmd/lfx-indexer

export NATS_URL="nats://lfx-platform-nats.lfx.svc.cluster.local:4222"
export OPENSEARCH_URL="http://opensearch-cluster-master.lfx.svc.cluster.local:9200"
export JWKS_URL="http://lfx-platform-heimdall.lfx.svc.cluster.local:4457/.well-known/jwks"

./bin/lfx-indexer 