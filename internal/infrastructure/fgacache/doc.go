// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package fgacache implements the FGA check cache backends (NATS KV, Valkey and none)
// behind contracts.FGACache, with a wrapper that bypasses the cache whenever
// the store is unavailable.
package fgacache
