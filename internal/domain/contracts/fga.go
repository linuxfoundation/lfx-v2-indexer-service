// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package contracts defines interfaces and data structures for domain contracts.
package contracts

import "fmt"

// JoinFgaQuery returns a formatted FGA query string
//
// e.g. "project:123#viewer"
// project:123 is the object, viewer is the relation
func JoinFgaQuery(object string, relation string) string {
	return fmt.Sprintf("%s#%s", object, relation)
}
