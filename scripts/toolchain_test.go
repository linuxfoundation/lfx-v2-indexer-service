// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckGoToolchain(t *testing.T) {
	script, err := os.ReadFile("check-go-toolchain.sh")
	require.NoError(t, err)
	tests := []struct {
		name      string
		pin       string
		toolchain string
		scanner   string
		actual    string
		wantError string
	}{
		{name: "exact_build_version", actual: "go1.26.10"},
		{name: "newer_patch_rejected", actual: "go1.26.11", wantError: "does not match the scanned build toolchain"},
		{name: "newer_minor_rejected", actual: "go1.27.3", wantError: "does not match the scanned build toolchain"},
		{name: "hyphen_suffix", actual: "go1.26.10-X:nodwarf5"},
		{name: "space_suffix", actual: "go1.26.10 X:nodwarf5"},
		{name: "newer_suffix_rejected", actual: "go1.27.3 X:nodwarf5", wantError: "does not match the scanned build toolchain"},
		{name: "old_lint_compiler_rejected_for_build", actual: "go1.26.3", wantError: "does not match the scanned build toolchain"},
		{name: "older_patch_rejected", actual: "go1.26.9", wantError: "does not match the scanned build toolchain"},
		{name: "toolchain_drift", toolchain: "1.26.9", actual: "go1.26.10", wantError: "Go build/scanner version mismatch"},
		{name: "scanner_drift", scanner: "1.26.9", actual: "go1.26.10", wantError: "Go build/scanner version mismatch"},
		{name: "unversioned_compiler", actual: "devel", wantError: "Cannot verify Go compiler version"},
		{name: "invalid_pin", pin: "not-a-version", actual: "go1.26.10", wantError: "Invalid Go build version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pin, toolchain, scanner := "1.26.10", "1.26.10", "1.26.10"
			if tt.pin != "" {
				pin = tt.pin
			}
			if tt.toolchain != "" {
				toolchain = tt.toolchain
			}
			if tt.scanner != "" {
				scanner = tt.scanner
			}
			root := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(root, "scripts"), 0755))
			require.NoError(t, os.Mkdir(filepath.Join(root, "bin"), 0755))
			path := filepath.Join(root, "scripts", "check-go-toolchain.sh")
			require.NoError(t, os.WriteFile(path, script, 0600))
			require.NoError(t, os.WriteFile(filepath.Join(root, ".go-version"), []byte(pin+"\n"), 0600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("go 1.26.3\n\ntoolchain go"+toolchain+"\n"), 0600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "osv-scanner.toml"), []byte("GoVersionOverride = \""+scanner+"\"\n"), 0600))
			// Use a fake compiler so the failure cases do not download old toolchains.
			require.NoError(t, os.WriteFile(filepath.Join(root, "bin", "go"), []byte("#!/bin/sh\nprintf '%s\\n' '"+tt.actual+"'\n"), 0700))
			cmd := exec.Command("sh", path)
			cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
			output, err := cmd.CombinedOutput()
			if tt.wantError != "" {
				require.Error(t, err, string(output))
				require.Contains(t, string(output), tt.wantError)
			} else {
				require.NoError(t, err, string(output))
				require.Contains(t, string(output), "Go build/scanner versions aligned")
			}
		})
	}
}
