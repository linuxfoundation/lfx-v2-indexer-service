// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package auth provides authentication and authorization infrastructure for the indexer service.
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/auth0/go-jwt-middleware/v2/jwks"
	"github.com/auth0/go-jwt-middleware/v2/validator"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
)

// HeimdallClaims contains extra custom claims we want to parse from the JWT token
type HeimdallClaims struct {
	Principal string `json:"principal"`
	Email     string `json:"email,omitempty"`
}

// Validate provides additional middleware validation of any claims defined in HeimdallClaims
func (c *HeimdallClaims) Validate(ctx context.Context) error {
	// Create a basic logger for validation - this method doesn't have access to AuthRepository logger
	logger := slog.With("component", "auth_validation")
	if c.Principal == "" {
		logger.WarnContext(ctx, "Principal validation failed: missing principal")
		return errors.New(constants.ErrPrincipalMissing)
	}
	logger.DebugContext(ctx, "Principal validation passed", "principal", c.Principal)
	return nil
}

// AuthRepository implements the domain AuthRepository interface
type AuthRepository struct {
	validator *validator.Validator
	issuer    string
	audiences []string // Support multiple audiences
	logger    *slog.Logger
}

// NewAuthRepository creates a new JWT auth repository
func NewAuthRepository(issuer string, audiences []string, jwksURL string, clockSkew time.Duration, logger *slog.Logger) (*AuthRepository, error) {
	authLogger := logging.WithComponent(logger, "auth_repository")

	// Parse the issuer URL
	issuerURL, err := url.Parse(issuer)
	if err != nil {
		authLogger.Error("Failed to parse issuer URL", "issuer", issuer, "error", err.Error())
		return nil, fmt.Errorf("invalid issuer URL: %w", err)
	}

	// Parse JWKS URL
	jwksURLParsed, err := url.Parse(jwksURL)
	if err != nil {
		authLogger.Error("Failed to parse JWKS URL", "jwks_url", jwksURL, "error", err.Error())
		return nil, fmt.Errorf("invalid JWKS URL: %w", err)
	}

	// Set up JWKS provider with 5 minute cache
	provider := jwks.NewCachingProvider(issuerURL, 5*time.Minute, jwks.WithCustomJWKSURI(jwksURLParsed))

	// Factory for custom JWT claims target
	customClaims := func() validator.CustomClaims {
		return &HeimdallClaims{}
	}

	// Set up JWT validator with PS256 and configuration
	jwtValidator, err := validator.New(
		provider.KeyFunc,
		validator.PS256,
		issuer,
		audiences,
		validator.WithCustomClaims(customClaims),
		validator.WithAllowedClockSkew(clockSkew),
	)
	if err != nil {
		authLogger.Error("Failed to create JWT validator", "error", err.Error())
		return nil, fmt.Errorf("failed to create JWT validator: %w", err)
	}

	return &AuthRepository{
		validator: jwtValidator,
		issuer:    issuer,
		audiences: audiences, // Store audiences array
		logger:    authLogger,
	}, nil
}

// ValidateToken validates a JWT token
func (r *AuthRepository) ValidateToken(ctx context.Context, token string) (*contracts.Principal, error) {
	authID := r.generateAuthID()

	// Trim any leading, case-insensitive "bearer " prefix
	originalToken := token
	if len(token) > 7 && strings.ToLower(token[:7]) == constants.BearerPrefix {
		token = token[7:]
	}

	// Check for empty token after prefix removal
	if token == "" {
		r.logger.Warn("Empty token after bearer prefix removal",
			"auth_id", authID,
			"original_token_length", len(originalToken))
		return nil, fmt.Errorf("%s: empty token", constants.ErrInvalidToken)
	}

	// Validate the token
	validatedClaims, err := r.validator.ValidateToken(ctx, token)
	if err != nil {
		errorType := r.classifyAuthError(err)
		r.logger.Error("Token validation failed",
			"auth_id", authID,
			"error", err.Error(),
			"error_type", errorType)
		return nil, fmt.Errorf("%s: %w", constants.ErrInvalidToken, err)
	}

	// Extract the principal from the token
	principal, err := r.extractPrincipalFromClaims(validatedClaims)
	if err != nil {
		r.logger.Error("Failed to extract principal from validated claims",
			"auth_id", authID,
			"error", err.Error())
		return nil, fmt.Errorf("failed to extract principal: %w", err)
	}

	r.logger.Info("Token validation completed",
		"auth_id", authID,
		"principal", r.safePrincipalLog(principal.Principal),
		"is_machine_user", r.isMachineUser(principal.Principal))

	return principal, nil
}

// ParsePrincipals extracts direct and indirect LFX principals from the
// Authorization and X-On-Behalf-Of header JWT bearer tokens. As the
// X-On-Behalf-Of header is not validated or pruned by the API gateway, this
// function also provides the enforcement that "on behalf of" data is only used
// if the authorized principal is a machine user.
func (r *AuthRepository) ParsePrincipals(ctx context.Context, headers map[string]string) ([]contracts.Principal, error) {
	authID := r.generateAuthID()

	// This will be set to `true` if the authorization header contains a machine
	// user principal.
	var isMachineUser bool

	var authorizedPrincipals []contracts.Principal
	var onBehalfOfPrincipals []contracts.Principal

	for header, value := range headers {
		header = strings.ToLower(header)
		switch header {
		case constants.AuthorizationHeader:
			principal, email, err := r.parsePrincipalAndEmail(ctx, value)
			if err != nil {
				r.logger.Warn("Failed to parse principal from authorization header",
					"auth_id", authID,
					"error", err.Error())
				continue
			}

			principalEntity := contracts.Principal{
				Principal: principal,
				Email:     email,
			}
			authorizedPrincipals = append(authorizedPrincipals, principalEntity)

			if strings.HasSuffix(principal, constants.MachineUserSuffix) {
				isMachineUser = true
				r.logger.Info("Machine user detected in authorization header",
					"auth_id", authID,
					"principal", r.safePrincipalLog(principal),
					"machine_user_suffix", constants.MachineUserSuffix)
			}

			r.logger.Debug("Authorization principal parsed successfully",
				"auth_id", authID,
				"principal", r.safePrincipalLog(principal),
				"has_email", email != "",
				"is_machine_user", isMachineUser)

		case constants.OnBehalfOfHeader:
			r.logger.Debug("Processing on-behalf-of header",
				"auth_id", authID,
				"value_length", len(value))

			// Split by commas.
			forwardedJWTs := strings.Split(value, ",")
			r.logger.Debug("On-behalf-of tokens found",
				"auth_id", authID,
				"token_count", len(forwardedJWTs))

			errCount := 0
			var err, lastError error
			for i, jwt := range forwardedJWTs {
				var principal, email string

				r.logger.Debug("Processing on-behalf-of token",
					"auth_id", authID,
					"token_index", i,
					"token_preview", r.safeTokenLog(strings.TrimSpace(jwt)))

				principal, email, err = r.parsePrincipalAndEmail(ctx, strings.TrimSpace(jwt))
				if err != nil {
					errCount++
					lastError = err
					r.logger.Warn("Failed to parse on-behalf-of token",
						"auth_id", authID,
						"token_index", i,
						"error", err.Error(),
						"error_type", r.classifyAuthError(err),
						"token_preview", r.safeTokenLog(strings.TrimSpace(jwt)))
					continue
				}

				principalEntity := contracts.Principal{
					Principal: principal,
					Email:     email,
				}
				onBehalfOfPrincipals = append(onBehalfOfPrincipals, principalEntity)

				r.logger.Debug("On-behalf-of principal parsed successfully",
					"auth_id", authID,
					"token_index", i,
					"principal", r.safePrincipalLog(principal),
					"has_email", email != "")
			}

			if lastError != nil {
				r.logger.Error("On-behalf-of parsing completed with errors",
					"auth_id", authID,
					"total_tokens", len(forwardedJWTs),
					"error_count", errCount,
					"success_count", len(onBehalfOfPrincipals),
					"last_error", lastError.Error(),
					"last_error_type", r.classifyAuthError(lastError))
			}
		}
	}

	// Authorization decision logging
	if isMachineUser {
		r.logger.Info("Machine user authorization: including on-behalf-of principals",
			"auth_id", authID,
			"authorized_principals", len(authorizedPrincipals),
			"on_behalf_of_principals", len(onBehalfOfPrincipals),
			"total_principals", len(authorizedPrincipals)+len(onBehalfOfPrincipals))

		// If the authenticated principal is a machine user, we will use the "on
		// behalf of" claims as well.
		authorizedPrincipals = append(authorizedPrincipals, onBehalfOfPrincipals...)
	} else if len(onBehalfOfPrincipals) > 0 {
		r.logger.Warn("On-behalf-of principals ignored: not a machine user",
			"auth_id", authID,
			"authorized_principals", len(authorizedPrincipals),
			"ignored_on_behalf_of_principals", len(onBehalfOfPrincipals))
	}

	r.logger.Info("Multi-principal parsing completed",
		"auth_id", authID,
		"final_principals_count", len(authorizedPrincipals),
		"is_machine_user", isMachineUser)

	return authorizedPrincipals, nil
}

// HealthCheck checks the health of the auth service
func (r *AuthRepository) HealthCheck(ctx context.Context) error {
	// Check if logger is properly initialized first to avoid panic
	if r.logger == nil {
		return errors.New("auth repository logger not initialized")
	}

	r.logger.DebugContext(ctx, "Auth repository health check initiated")

	// Check if validator is properly initialized
	if r.validator == nil {
		r.logger.ErrorContext(ctx, "Auth repository health check failed: validator not initialized")
		return errors.New(constants.ErrJWTValidatorNotInit)
	}

	r.logger.DebugContext(ctx, "Auth repository health check passed")
	return nil
}

// GetMetrics returns auth repository metrics for monitoring
func (r *AuthRepository) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"issuer":                r.issuer,
		"audiences":             r.audiences,
		"audiences_count":       len(r.audiences),
		"validator_initialized": r.validator != nil,
		"logger_initialized":    r.logger != nil,
	}
}

// GetAuthStatus returns the authentication status for health monitoring
func (r *AuthRepository) GetAuthStatus() map[string]interface{} {
	return map[string]interface{}{
		"component":       "auth_repository",
		"status":          "initialized",
		"issuer":          r.issuer,
		"audiences_count": len(r.audiences),
		"validator_ready": r.validator != nil,
	}
}

// parsePrincipalAndEmail extracts the principal and email from the JWT claims
func (r *AuthRepository) parsePrincipalAndEmail(ctx context.Context, token string) (string, string, error) {
	if r.validator == nil {
		r.logger.Error("JWT validator not initialized for principal parsing")
		return "", "", errors.New(constants.ErrJWTValidatorNotSet)
	}

	r.logger.Debug("Parsing principal and email from token",
		"token_preview", r.safeTokenLog(token))

	// Trim any leading, case-insensitive "bearer " prefix
	originalToken := token
	if len(token) > 7 && strings.ToLower(token[:7]) == constants.BearerPrefix {
		token = token[7:]
	}

	// Check for empty token
	if token == "" {
		r.logger.Warn("Empty token provided for principal parsing",
			"original_token_length", len(originalToken))
		return "", "", errors.New("empty token")
	}

	parsedJWT, err := r.validator.ValidateToken(ctx, token)
	if err != nil {
		errorType := r.classifyAuthError(err)
		r.logger.Debug("Token validation failed during principal parsing",
			"error", err.Error(),
			"error_type", errorType,
			"token_preview", r.safeTokenLog(token))
		return "", "", err
	}

	claims, ok := parsedJWT.(*validator.ValidatedClaims)
	if !ok {
		r.logger.Error("Failed to cast parsed JWT to ValidatedClaims",
			"token_preview", r.safeTokenLog(token))
		return "", "", errors.New(constants.ErrValidatedClaimsFailed)
	}

	customClaims, ok := claims.CustomClaims.(*HeimdallClaims)
	if !ok {
		r.logger.Error("Failed to cast custom claims to HeimdallClaims",
			"token_preview", r.safeTokenLog(token))
		return "", "", errors.New(constants.ErrCustomClaimsFailed)
	}

	principal := customClaims.Principal
	email := customClaims.Email

	r.logger.Debug("Principal and email extracted successfully",
		"principal", r.safePrincipalLog(principal),
		"has_email", email != "",
		"is_machine_user", r.isMachineUser(principal))

	return principal, email, nil
}

// extractPrincipalFromClaims extracts principal information from JWT claims
func (r *AuthRepository) extractPrincipalFromClaims(claims interface{}) (*contracts.Principal, error) {
	r.logger.Debug("Extracting principal from validated claims")

	validatedClaims, ok := claims.(*validator.ValidatedClaims)
	if !ok {
		r.logger.Error("Invalid claims type provided for principal extraction",
			"claims_type", fmt.Sprintf("%T", claims))
		return nil, errors.New(constants.ErrInvalidClaimsType)
	}

	customClaims, ok := validatedClaims.CustomClaims.(*HeimdallClaims)
	if !ok {
		r.logger.Error("Invalid custom claims type for principal extraction",
			"custom_claims_type", fmt.Sprintf("%T", validatedClaims.CustomClaims))
		return nil, errors.New(constants.ErrInvalidCustomClaims)
	}

	principal := &contracts.Principal{
		Principal: customClaims.Principal,
		Email:     customClaims.Email,
	}

	// Fallback to subject if no principal in custom claims
	if principal.Principal == "" && validatedClaims.RegisteredClaims.Subject != "" {
		r.logger.Debug("Using subject claim as fallback for principal",
			"subject", validatedClaims.RegisteredClaims.Subject)
		principal.Principal = validatedClaims.RegisteredClaims.Subject
	}

	if principal.Principal == "" {
		r.logger.Error("No principal found in claims",
			"has_custom_principal", customClaims.Principal != "",
			"has_subject", validatedClaims.RegisteredClaims.Subject != "",
			"has_email", customClaims.Email != "")
		return nil, errors.New(constants.ErrNoPrincipalFound)
	}

	r.logger.Debug("Principal extracted successfully",
		"principal", r.safePrincipalLog(principal.Principal),
		"has_email", principal.Email != "",
		"is_machine_user", r.isMachineUser(principal.Principal),
		"used_subject_fallback", customClaims.Principal == "" && validatedClaims.RegisteredClaims.Subject != "")

	return principal, nil
}

// GetIssuer returns the JWT issuer
func (r *AuthRepository) GetIssuer() string {
	return r.issuer
}

// GetAudiences returns all JWT audiences
func (r *AuthRepository) GetAudiences() []string {
	return r.audiences
}

// Helper methods for security-safe logging

// safeTokenLog safely logs token information without exposing sensitive data
func (r *AuthRepository) safeTokenLog(token string) string {
	if token == "" {
		return "<empty>"
	}
	if len(token) < 20 {
		return "<too_short>"
	}
	// Show first 8 and last 8 characters for debugging while maintaining security
	return fmt.Sprintf("%s...%s", token[:8], token[len(token)-8:])
}

// generateAuthID generates a unique identifier for authentication requests
func (r *AuthRepository) generateAuthID() string {
	return fmt.Sprintf("auth_%d", time.Now().UnixNano())
}

// classifyAuthError categorizes authentication errors for logging
func (r *AuthRepository) classifyAuthError(err error) string {
	if err == nil {
		return "none"
	}

	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "token is expired"):
		return "expired_token"
	case strings.Contains(errStr, "token is not valid yet"):
		return "premature_token"
	case strings.Contains(errStr, "signature verification failed"):
		return "invalid_signature"
	case strings.Contains(errStr, "token is malformed"):
		return "malformed_token"
	case strings.Contains(errStr, "audience"):
		return "invalid_audience"
	case strings.Contains(errStr, "issuer"):
		return "invalid_issuer"
	case strings.Contains(errStr, "claims"):
		return "invalid_claims"
	default:
		return "unknown_error"
	}
}

// safePrincipalLog safely logs principal information
func (r *AuthRepository) safePrincipalLog(principal string) string {
	if principal == "" {
		return "<empty>"
	}
	// Machine users are not personal information and should be logged in full for debugging.
	if r.isMachineUser(principal) {
		return principal
	}
	// Don't log full email addresses for privacy.
	if strings.Contains(principal, "@") {
		parts := strings.Split(principal, "@")
		if len(parts) == 2 {
			return fmt.Sprintf("%s@***", parts[0])
		}
	}
	return principal
}

// isMachineUser checks if a principal is a machine user
func (r *AuthRepository) isMachineUser(principal string) bool {
	return strings.HasSuffix(principal, constants.MachineUserSuffix)
}
