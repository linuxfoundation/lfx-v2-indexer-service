package jwt

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

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
	audience  string
}

// NewAuthRepository creates a new JWT auth repository
func NewAuthRepository(issuer, audience string) (*AuthRepository, error) {
	// Parse the issuer URL
	issuerURL, err := url.Parse(issuer)
	if err != nil {
		return nil, fmt.Errorf("invalid issuer URL: %w", err)
	}

	// Set up the JWT validator
	provider := jwks.NewCachingProvider(issuerURL, 5*60) // 5 minute cache

	// Factory for custom JWT claims target
	customClaims := func() validator.CustomClaims {
		return &HeimdallClaims{}
	}

	jwtValidator, err := validator.New(
		provider.KeyFunc,
		validator.PS256,
		issuer,
		[]string{audience},
		validator.WithCustomClaims(customClaims),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWT validator: %w", err)
	}

	return &AuthRepository{
		validator: jwtValidator,
		issuer:    issuer,
		audience:  audience,
	}, nil
}

// ValidateToken validates a JWT token
func (r *AuthRepository) ValidateToken(ctx context.Context, token string) (*entities.Principal, error) {
	// Remove "Bearer " prefix if present
	token = strings.TrimPrefix(token, "Bearer ")

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

// ParsePrincipals parses principals from HTTP headers
func (r *AuthRepository) ParsePrincipals(ctx context.Context, headers map[string]string) ([]entities.Principal, error) {
	var principals []entities.Principal

	// Look for authenticated principal headers
	if principalHeader, ok := headers["authenticated-principal"]; ok {
		principal, err := r.parsePrincipal(principalHeader)
		if err != nil {
			return nil, fmt.Errorf("failed to parse authenticated principal: %w", err)
		}
		principals = append(principals, *principal)
	}

	// Look for other principal headers
	for key, value := range headers {
		if strings.HasPrefix(key, "principal-") {
			principal, err := r.parsePrincipal(value)
			if err != nil {
				// Log the error but don't fail the entire parsing
				continue
			}
			principals = append(principals, *principal)
		}
	}

	// If no principals found, return empty list (not an error)
	if len(principals) == 0 {
		return []entities.Principal{}, nil
	}

	return principals, nil
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

// parsePrincipal parses a principal from a header value
func (r *AuthRepository) parsePrincipal(value string) (*entities.Principal, error) {
	if value == "" {
		return nil, errors.New("empty principal value")
	}

	// For now, assume the value is just the principal ID
	// In a more complete implementation, this might parse a more complex format
	principal := &entities.Principal{
		Principal: value,
	}

	// Check if it's a machine user (starts with "clients@")
	if strings.HasPrefix(value, machineUserPrefix) {
		// For machine users, the principal is the full value
		principal.Principal = value
	}

	return principal, nil
}

// GetIssuer returns the JWT issuer
func (r *AuthRepository) GetIssuer() string {
	return r.issuer
}

// GetAudience returns the JWT audience
func (r *AuthRepository) GetAudience() string {
	return r.audience
}
