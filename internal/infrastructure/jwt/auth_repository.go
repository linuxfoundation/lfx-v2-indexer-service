package jwt

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/auth0/go-jwt-middleware/v2/jwks"
	"github.com/auth0/go-jwt-middleware/v2/validator"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
)

const machineUserPrefix = "clients@"

// HeimdallClaims contains extra custom claims we want to parse from the JWT token
type HeimdallClaims struct {
	Principal string `json:"principal"`
	Email     string `json:"email,omitempty"`
}

// Validate provides additional middleware validation of any claims defined in HeimdallClaims
func (c *HeimdallClaims) Validate(ctx context.Context) error {
	if c.Principal == "" {
		return errors.New("principal must be provided")
	}
	return nil
}

// AuthRepository implements the domain AuthRepository interface
type AuthRepository struct {
	validator *validator.Validator
	issuer    string
	audiences []string // Support multiple audiences
}

// NewAuthRepository creates a new JWT auth repository
func NewAuthRepository(issuer string, audiences []string, jwksURL string, clockSkew time.Duration) (*AuthRepository, error) {
	// Parse the issuer URL
	issuerURL, err := url.Parse(issuer)
	if err != nil {
		return nil, fmt.Errorf("invalid issuer URL: %w", err)
	}

	// Parse JWKS URL
	jwksURLParsed, err := url.Parse(jwksURL)
	if err != nil {
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
		return nil, fmt.Errorf("failed to create JWT validator: %w", err)
	}

	return &AuthRepository{
		validator: jwtValidator,
		issuer:    issuer,
		audiences: audiences, // Store audiences array
	}, nil
}

// ValidateToken validates a JWT token
func (r *AuthRepository) ValidateToken(ctx context.Context, token string) (*entities.Principal, error) {
	// Trim any leading, case-insensitive "bearer " prefix
	// Not using strings.TrimPrefix() because that function is case-sensitive
	if len(token) > 7 && strings.ToLower(token[:7]) == "bearer " {
		token = token[7:]
	}

	// Validate the token
	validatedClaims, err := r.validator.ValidateToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	// Extract the principal from the token
	principal, err := r.extractPrincipalFromClaims(validatedClaims)
	if err != nil {
		return nil, fmt.Errorf("failed to extract principal: %w", err)
	}

	return principal, nil
}

// ParsePrincipals extracts direct and indirect LFX principals from the
// Authorization and X-On-Behalf-Of header JWT bearer tokens. As the
// X-On-Behalf-Of header is not validated or pruned by the API gateway, this
// function also provides the enforcement that "on behalf of" data is only used
// if the authorized principal is a machine user.
func (r *AuthRepository) ParsePrincipals(ctx context.Context, headers map[string]string) ([]entities.Principal, error) {
	// This will be set to `true` if the authorization header contains a machine
	// user principal.
	var isMachineUser bool

	var authorizedPrincipals []entities.Principal
	var onBehalfOfPrincipals []entities.Principal

	for header, value := range headers {
		header = strings.ToLower(header)
		switch header {
		case "authorization":
			principal, email, err := r.parsePrincipalAndEmail(ctx, value)
			if err != nil {
				// Log warning but continue processing
				// TODO: Add structured logging
				continue
			}
			authorizedPrincipals = append(authorizedPrincipals, entities.Principal{
				Principal: principal,
				Email:     email,
			})
			if strings.HasPrefix(principal, machineUserPrefix) {
				isMachineUser = true
			}
		case "x-on-behalf-of":
			// Split by commas.
			forwardedJWTs := strings.Split(value, ",")
			errCount := 0
			var err, lastError error
			for _, jwt := range forwardedJWTs {
				var principal, email string
				// Trim spaces when parsing individual comma-separated values. This
				// allows for a multi-header value, as ...
				//
				//  X-On-Behalf-Of: jwt1
				//  X-On-Behalf-Of: jwt2
				//
				// ... is equivalent to:
				//
				//  X-On-Behalf-Of: jwt1, jwt2
				//
				principal, email, err = r.parsePrincipalAndEmail(ctx, strings.TrimSpace(jwt))
				if err != nil {
					// Increment the error counter. Only the final error seen will be
					// logged, after the `forwardedJWTs` loop completes.
					errCount++
					lastError = err
					// This continues to the next comma-separated on-behalf-of value, not
					// the next request header.
					continue
				}
				onBehalfOfPrincipals = append(onBehalfOfPrincipals, entities.Principal{
					Principal: principal,
					Email:     email,
				})
			}
			// TODO: Add structured logging for errors
			if lastError != nil {
				// Log the number of errors encountered, as well as the
				// final/last error seen
				_ = errCount  // Placeholder for logging
				_ = lastError // Placeholder for logging
			}
		}
	}

	if isMachineUser {
		// If the authenticated principal is a machine user, we will use the "on
		// behalf of" claims as well.
		authorizedPrincipals = append(authorizedPrincipals, onBehalfOfPrincipals...)
	}

	return authorizedPrincipals, nil
}

// HealthCheck checks the health of the auth service
func (r *AuthRepository) HealthCheck(ctx context.Context) error {
	if r.validator == nil {
		return errors.New("JWT validator is not initialized")
	}

	// For now, just check if the validator is initialized
	// In a more complete implementation, we might try to validate a test token
	// or check connectivity to the JWKS endpoint
	return nil
}

// parsePrincipalAndEmail extracts the principal and email from the JWT claims
func (r *AuthRepository) parsePrincipalAndEmail(ctx context.Context, token string) (string, string, error) {
	if r.validator == nil {
		return "", "", errors.New("JWT validator is not set up")
	}

	// Trim any leading, case-insensitive "bearer " prefix
	// Not using strings.TrimPrefix() because that function is case-sensitive
	if len(token) > 7 && strings.ToLower(token[:7]) == "bearer " {
		token = token[7:]
	}

	parsedJWT, err := r.validator.ValidateToken(ctx, token)
	if err != nil {
		return "", "", err
	}

	claims, ok := parsedJWT.(*validator.ValidatedClaims)
	if !ok {
		// This should never happen
		return "", "", errors.New("failed to get validated authorization claims")
	}

	customClaims, ok := claims.CustomClaims.(*HeimdallClaims)
	if !ok {
		// This should never happen
		return "", "", errors.New("failed to get custom authorization claims")
	}

	return customClaims.Principal, customClaims.Email, nil
}

// extractPrincipalFromClaims extracts principal information from JWT claims
func (r *AuthRepository) extractPrincipalFromClaims(claims interface{}) (*entities.Principal, error) {
	validatedClaims, ok := claims.(*validator.ValidatedClaims)
	if !ok {
		return nil, errors.New("invalid claims type")
	}

	customClaims, ok := validatedClaims.CustomClaims.(*HeimdallClaims)
	if !ok {
		return nil, errors.New("invalid custom claims type")
	}

	principal := &entities.Principal{
		Principal: customClaims.Principal,
		Email:     customClaims.Email,
	}

	// Fallback to subject if no principal in custom claims
	if principal.Principal == "" && validatedClaims.RegisteredClaims.Subject != "" {
		principal.Principal = validatedClaims.RegisteredClaims.Subject
	}

	if principal.Principal == "" {
		return nil, errors.New("no principal identifier found in token")
	}

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
