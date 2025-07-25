// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package config provides configuration structures and utilities for the LFX indexer service.
package config

// CLIConfig holds parsed command line flags and configuration overrides
type CLIConfig struct {
	Port         string
	Debug        bool
	Bind         string
	NoJanitor    bool
	SimpleHealth bool
	ConfigCheck  bool
	Help         bool
}
