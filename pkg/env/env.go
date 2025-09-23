// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package env provides utilities for reading environment variables with type conversion and default values.
package env

import (
	"os"
	"strconv"
	"time"
)

// GetString returns the value of the environment variable or the default value if not set or empty.
func GetString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// GetInt returns the value of the environment variable as an integer or the default value if not set, empty, or invalid.
func GetInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

// GetBool returns the value of the environment variable as a boolean or the default value if not set, empty, or invalid.
func GetBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}

// GetDuration returns the value of the environment variable as a time.Duration or the default value if not set, empty, or invalid.
func GetDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}
