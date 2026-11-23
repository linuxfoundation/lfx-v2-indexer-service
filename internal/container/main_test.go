// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package container

import (
	"os"
	"testing"
)

// TestMain clears the OpenFGA settings so a developer's environment cannot
// start a real writer, whose background store lookup adds HTTP spans and
// changes FGA replies. Tests that need a writer set them with t.Setenv.
func TestMain(m *testing.M) {
	for _, key := range []string{"OPENFGA_API_URL", "OPENFGA_STORE_NAME", "OPENFGA_STORE_ID"} {
		_ = os.Unsetenv(key)
	}
	os.Exit(m.Run())
}
