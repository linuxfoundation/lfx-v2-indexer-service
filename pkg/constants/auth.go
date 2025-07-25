// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package constants provides shared constants used throughout the LFX indexer service.
package constants

// Authentication constants
const (
	// Header names
	AuthorizationHeader = "authorization"
	OnBehalfOfHeader    = "x-on-behalf-of"

	// Token prefixes
	BearerPrefix = "bearer "

	// Machine user identifier
	MachineUserPrefix = "clients@"

	// Error messages for authentication
	ErrInvalidToken          = "invalid token"
	ErrPrincipalMissing      = "principal must be provided"
	ErrJWTValidatorNotSet    = "JWT validator is not set up"
	ErrJWTValidatorNotInit   = "JWT validator is not initialized"
	ErrAuthRepoNotConfigured = "auth repository not configured"
	ErrInvalidClaimsType     = "invalid claims type"
	ErrInvalidCustomClaims   = "invalid custom claims type"
	ErrNoPrincipalFound      = "no principal identifier found in token"
	ErrValidatedClaimsFailed = "failed to get validated authorization claims"
	ErrCustomClaimsFailed    = "failed to get custom authorization claims"
)
