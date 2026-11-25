// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"strings"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// ObjectTypeFromSubject returns the object type of an indexing subject on
// either the JetStream prefix (lfx.index.) or the request/reply prefix
// (lfx.indexer.index.). Like the strings.CutPrefix calls it replaces, it does
// not validate the remainder, so the lfx.index. path behaves as before.
func ObjectTypeFromSubject(subject string) (string, bool) {
	for _, prefix := range []string{constants.IndexPrefix, constants.IndexerIndexPrefix} {
		if objectType, ok := strings.CutPrefix(subject, prefix); ok {
			return objectType, true
		}
	}
	return "", false
}
