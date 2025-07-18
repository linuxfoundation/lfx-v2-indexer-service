package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants
const (
	testIssuer   = "https://test.auth0.com/"
	testAudience = "test-audience"
	testJWKSURL  = "https://test.auth0.com/.well-known/jwks.json"
	testToken    = "eyJhbGciOiJQUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.invalid-signature"
)

// Helper function to create a test logger
func setupTestLogger() *slog.Logger {
	return logging.NewLogger(true) // Enable debug mode
}

// Test NewAuthRepository constructor
func TestNewAuthRepository(t *testing.T) {
	logger := setupTestLogger()

	t.Run("successful_creation", func(t *testing.T) {
		repo, err := NewAuthRepository(
			testIssuer,
			[]string{testAudience},
			testJWKSURL,
			time.Minute,
			logger,
		)

		assert.NoError(t, err)
		assert.NotNil(t, repo)
		assert.Equal(t, testIssuer, repo.issuer)
		assert.Equal(t, []string{testAudience}, repo.audiences)
		assert.NotNil(t, repo.validator)
		assert.NotNil(t, repo.logger)
	})

	t.Run("invalid_issuer_url", func(t *testing.T) {
		repo, err := NewAuthRepository(
			"://invalid-url",
			[]string{testAudience},
			testJWKSURL,
			time.Minute,
			logger,
		)

		assert.Error(t, err)
		assert.Nil(t, repo)
		assert.Contains(t, err.Error(), "invalid issuer URL")
	})

	t.Run("invalid_jwks_url", func(t *testing.T) {
		repo, err := NewAuthRepository(
			testIssuer,
			[]string{testAudience},
			"://invalid-url",
			time.Minute,
			logger,
		)

		assert.Error(t, err)
		assert.Nil(t, repo)
		assert.Contains(t, err.Error(), "invalid JWKS URL")
	})

	t.Run("empty_audiences", func(t *testing.T) {
		repo, err := NewAuthRepository(
			testIssuer,
			[]string{},
			testJWKSURL,
			time.Minute,
			logger,
		)

		// Should fail with empty audiences as JWT validator requires at least one audience
		assert.Error(t, err)
		assert.Nil(t, repo)
		assert.Contains(t, err.Error(), "audience is required but was empty")
	})

	t.Run("multiple_audiences", func(t *testing.T) {
		audiences := []string{"audience1", "audience2", "audience3"}
		repo, err := NewAuthRepository(
			testIssuer,
			audiences,
			testJWKSURL,
			time.Minute,
			logger,
		)

		assert.NoError(t, err)
		assert.NotNil(t, repo)
		assert.Equal(t, audiences, repo.audiences)
	})
}

// Test ValidateToken method
func TestAuthRepository_ValidateToken(t *testing.T) {
	logger := setupTestLogger()
	repo, err := NewAuthRepository(
		testIssuer,
		[]string{testAudience},
		testJWKSURL,
		time.Minute,
		logger,
	)
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("empty_token", func(t *testing.T) {
		principal, err := repo.ValidateToken(ctx, "")

		assert.Error(t, err)
		assert.Nil(t, principal)
		assert.Contains(t, err.Error(), constants.ErrInvalidToken)
	})

	t.Run("bearer_prefix_only", func(t *testing.T) {
		principal, err := repo.ValidateToken(ctx, "Bearer ")

		assert.Error(t, err)
		assert.Nil(t, principal)
		assert.Contains(t, err.Error(), constants.ErrInvalidToken)
	})

	t.Run("invalid_token", func(t *testing.T) {
		principal, err := repo.ValidateToken(ctx, "invalid-token")

		assert.Error(t, err)
		assert.Nil(t, principal)
		assert.Contains(t, err.Error(), constants.ErrInvalidToken)
	})

	t.Run("malformed_jwt", func(t *testing.T) {
		principal, err := repo.ValidateToken(ctx, "Bearer invalid.jwt.token")

		assert.Error(t, err)
		assert.Nil(t, principal)
		assert.Contains(t, err.Error(), constants.ErrInvalidToken)
	})

	t.Run("token_with_bearer_prefix", func(t *testing.T) {
		principal, err := repo.ValidateToken(ctx, "Bearer "+testToken)

		// Should fail due to invalid signature but should process bearer prefix
		assert.Error(t, err)
		assert.Nil(t, principal)
		assert.Contains(t, err.Error(), constants.ErrInvalidToken)
	})

	t.Run("case_insensitive_bearer", func(t *testing.T) {
		principal, err := repo.ValidateToken(ctx, "bearer "+testToken)

		// Should fail due to invalid signature but should process bearer prefix
		assert.Error(t, err)
		assert.Nil(t, principal)
		assert.Contains(t, err.Error(), constants.ErrInvalidToken)
	})
}

// Test ParsePrincipals method
func TestAuthRepository_ParsePrincipals(t *testing.T) {
	logger := setupTestLogger()
	repo, err := NewAuthRepository(
		testIssuer,
		[]string{testAudience},
		testJWKSURL,
		time.Minute,
		logger,
	)
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("empty_headers", func(t *testing.T) {
		principals, err := repo.ParsePrincipals(ctx, map[string]string{})

		assert.NoError(t, err)
		assert.Empty(t, principals)
		assert.Len(t, principals, 0)
	})

	t.Run("invalid_authorization_header", func(t *testing.T) {
		headers := map[string]string{
			constants.AuthorizationHeader: "invalid-token",
		}

		principals, err := repo.ParsePrincipals(ctx, headers)

		// Should not return error but empty principals due to invalid token
		assert.NoError(t, err)
		assert.Empty(t, principals)
		assert.Len(t, principals, 0)
	})

	t.Run("invalid_on_behalf_of_header", func(t *testing.T) {
		headers := map[string]string{
			constants.OnBehalfOfHeader: "invalid-token1,invalid-token2",
		}

		principals, err := repo.ParsePrincipals(ctx, headers)

		// Should not return error but empty principals due to invalid tokens
		assert.NoError(t, err)
		assert.Empty(t, principals)
		assert.Len(t, principals, 0)
	})

	t.Run("mixed_valid_invalid_tokens", func(t *testing.T) {
		headers := map[string]string{
			constants.AuthorizationHeader: "Bearer invalid-token",
			constants.OnBehalfOfHeader:    "invalid-token1,invalid-token2",
		}

		principals, err := repo.ParsePrincipals(ctx, headers)

		assert.NoError(t, err)
		assert.Empty(t, principals)
		assert.Len(t, principals, 0)
	})

	t.Run("case_insensitive_headers", func(t *testing.T) {
		headers := map[string]string{
			"AUTHORIZATION":  "Bearer invalid-token",
			"X-ON-BEHALF-OF": "invalid-token1,invalid-token2",
		}

		principals, err := repo.ParsePrincipals(ctx, headers)

		assert.NoError(t, err)
		assert.Empty(t, principals)
		assert.Len(t, principals, 0)
	})
}

// Test HealthCheck method
func TestAuthRepository_HealthCheck(t *testing.T) {
	logger := setupTestLogger()

	t.Run("healthy_repository", func(t *testing.T) {
		repo, err := NewAuthRepository(
			testIssuer,
			[]string{testAudience},
			testJWKSURL,
			time.Minute,
			logger,
		)
		require.NoError(t, err)

		ctx := context.Background()
		err = repo.HealthCheck(ctx)

		assert.NoError(t, err)
	})

	t.Run("nil_validator", func(t *testing.T) {
		repo := &AuthRepository{
			validator: nil,
			logger:    logger,
		}

		ctx := context.Background()
		err := repo.HealthCheck(ctx)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), constants.ErrJWTValidatorNotInit)
	})

	t.Run("nil_logger", func(t *testing.T) {
		repo := &AuthRepository{
			validator: nil,
			logger:    nil,
		}

		ctx := context.Background()
		err := repo.HealthCheck(ctx)

		// Should fail on logger check first
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "logger not initialized")
	})
}

// Test GetMetrics method
func TestAuthRepository_GetMetrics(t *testing.T) {
	logger := setupTestLogger()
	audiences := []string{"audience1", "audience2"}

	repo, err := NewAuthRepository(
		testIssuer,
		audiences,
		testJWKSURL,
		time.Minute,
		logger,
	)
	require.NoError(t, err)

	metrics := repo.GetMetrics()

	assert.NotNil(t, metrics)
	assert.Equal(t, testIssuer, metrics["issuer"])
	assert.Equal(t, audiences, metrics["audiences"])
	assert.Equal(t, len(audiences), metrics["audiences_count"])
	assert.Equal(t, true, metrics["validator_initialized"])
	assert.Equal(t, true, metrics["logger_initialized"])
}

// Test GetAuthStatus method
func TestAuthRepository_GetAuthStatus(t *testing.T) {
	logger := setupTestLogger()
	audiences := []string{"audience1", "audience2"}

	repo, err := NewAuthRepository(
		testIssuer,
		audiences,
		testJWKSURL,
		time.Minute,
		logger,
	)
	require.NoError(t, err)

	status := repo.GetAuthStatus()

	assert.NotNil(t, status)
	assert.Equal(t, "auth_repository", status["component"])
	assert.Equal(t, "initialized", status["status"])
	assert.Equal(t, testIssuer, status["issuer"])
	assert.Equal(t, len(audiences), status["audiences_count"])
	assert.Equal(t, true, status["validator_ready"])
}

// Test GetIssuer method
func TestAuthRepository_GetIssuer(t *testing.T) {
	logger := setupTestLogger()

	repo, err := NewAuthRepository(
		testIssuer,
		[]string{testAudience},
		testJWKSURL,
		time.Minute,
		logger,
	)
	require.NoError(t, err)

	issuer := repo.GetIssuer()
	assert.Equal(t, testIssuer, issuer)
}

// Test GetAudiences method
func TestAuthRepository_GetAudiences(t *testing.T) {
	logger := setupTestLogger()
	audiences := []string{"audience1", "audience2", "audience3"}

	repo, err := NewAuthRepository(
		testIssuer,
		audiences,
		testJWKSURL,
		time.Minute,
		logger,
	)
	require.NoError(t, err)

	returnedAudiences := repo.GetAudiences()
	assert.Equal(t, audiences, returnedAudiences)
}

// Test helper methods
func TestAuthRepository_HelperMethods(t *testing.T) {
	logger := setupTestLogger()

	repo, err := NewAuthRepository(
		testIssuer,
		[]string{testAudience},
		testJWKSURL,
		time.Minute,
		logger,
	)
	require.NoError(t, err)

	t.Run("safeTokenLog", func(t *testing.T) {
		// Test empty token
		result := repo.safeTokenLog("")
		assert.Equal(t, "<empty>", result)

		// Test short token
		result = repo.safeTokenLog("short")
		assert.Equal(t, "<too_short>", result)

		// Test long token
		longToken := "eyJhbGciOiJQUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.invalid-signature"
		result = repo.safeTokenLog(longToken)
		assert.Contains(t, result, "...")
		assert.True(t, len(result) < len(longToken))
	})

	t.Run("generateAuthID", func(t *testing.T) {
		id1 := repo.generateAuthID()
		id2 := repo.generateAuthID()

		assert.NotEqual(t, id1, id2)
		assert.True(t, strings.HasPrefix(id1, "auth_"))
		assert.True(t, strings.HasPrefix(id2, "auth_"))
	})

	t.Run("classifyAuthError", func(t *testing.T) {
		testCases := []struct {
			err      error
			expected string
		}{
			{nil, "none"},
			{errors.New("token is expired"), "expired_token"},
			{errors.New("token is not valid yet"), "premature_token"},
			{errors.New("signature verification failed"), "invalid_signature"},
			{errors.New("token is malformed"), "malformed_token"},
			{errors.New("invalid audience"), "invalid_audience"},
			{errors.New("invalid issuer"), "invalid_issuer"},
			{errors.New("invalid claims"), "invalid_claims"},
			{errors.New("unknown error"), "unknown_error"},
		}

		for _, tc := range testCases {
			result := repo.classifyAuthError(tc.err)
			assert.Equal(t, tc.expected, result, "Error: %v", tc.err)
		}
	})

	t.Run("safePrincipalLog", func(t *testing.T) {
		// Test empty principal
		result := repo.safePrincipalLog("")
		assert.Equal(t, "<empty>", result)

		// Test email principal
		result = repo.safePrincipalLog("user@example.com")
		assert.Equal(t, "user@***", result)

		// Test non-email principal
		result = repo.safePrincipalLog("machine-user-123")
		assert.Equal(t, "machine-user-123", result)
	})

	t.Run("isMachineUser", func(t *testing.T) {
		// Test machine user
		result := repo.isMachineUser(constants.MachineUserPrefix + "test-machine")
		assert.True(t, result)

		// Test regular user
		result = repo.isMachineUser("regular-user")
		assert.False(t, result)

		// Test email user
		result = repo.isMachineUser("user@example.com")
		assert.False(t, result)

		// Test empty principal
		result = repo.isMachineUser("")
		assert.False(t, result)
	})
}

// Test HeimdallClaims validation
func TestHeimdallClaims_Validate(t *testing.T) {
	ctx := context.Background()

	t.Run("valid_claims", func(t *testing.T) {
		claims := &HeimdallClaims{
			Principal: "test-user",
			Email:     "test@example.com",
		}

		err := claims.Validate(ctx)
		assert.NoError(t, err)
	})

	t.Run("missing_principal", func(t *testing.T) {
		claims := &HeimdallClaims{
			Principal: "",
			Email:     "test@example.com",
		}

		err := claims.Validate(ctx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), constants.ErrPrincipalMissing)
	})

	t.Run("optional_email", func(t *testing.T) {
		claims := &HeimdallClaims{
			Principal: "test-user",
			Email:     "",
		}

		err := claims.Validate(ctx)
		assert.NoError(t, err)
	})
}

// Test edge cases and error conditions
func TestAuthRepository_EdgeCases(t *testing.T) {
	logger := setupTestLogger()

	t.Run("parsePrincipalAndEmail_nil_validator", func(t *testing.T) {
		repo := &AuthRepository{
			validator: nil,
			logger:    logger,
		}

		ctx := context.Background()
		principal, email, err := repo.parsePrincipalAndEmail(ctx, "Bearer token")

		assert.Error(t, err)
		assert.Empty(t, principal)
		assert.Empty(t, email)
		assert.Contains(t, err.Error(), constants.ErrJWTValidatorNotSet)
	})

	t.Run("parsePrincipalAndEmail_empty_token", func(t *testing.T) {
		repo, err := NewAuthRepository(
			testIssuer,
			[]string{testAudience},
			testJWKSURL,
			time.Minute,
			logger,
		)
		require.NoError(t, err)

		ctx := context.Background()
		principal, email, err := repo.parsePrincipalAndEmail(ctx, "")

		assert.Error(t, err)
		assert.Empty(t, principal)
		assert.Empty(t, email)
		assert.Contains(t, err.Error(), "empty token")
	})

	t.Run("parsePrincipalAndEmail_bearer_only", func(t *testing.T) {
		repo, err := NewAuthRepository(
			testIssuer,
			[]string{testAudience},
			testJWKSURL,
			time.Minute,
			logger,
		)
		require.NoError(t, err)

		ctx := context.Background()
		principal, email, err := repo.parsePrincipalAndEmail(ctx, "Bearer ")

		assert.Error(t, err)
		assert.Empty(t, principal)
		assert.Empty(t, email)
		// It will fail with token parsing error, not "empty token"
		assert.Contains(t, err.Error(), "could not parse the token")
	})
}

// Benchmark tests
func BenchmarkAuthRepository_GenerateAuthID(b *testing.B) {
	logger := setupTestLogger()
	repo, err := NewAuthRepository(
		testIssuer,
		[]string{testAudience},
		testJWKSURL,
		time.Minute,
		logger,
	)
	require.NoError(b, err)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = repo.generateAuthID()
	}
}

func BenchmarkAuthRepository_ClassifyAuthError(b *testing.B) {
	logger := setupTestLogger()
	repo, err := NewAuthRepository(
		testIssuer,
		[]string{testAudience},
		testJWKSURL,
		time.Minute,
		logger,
	)
	require.NoError(b, err)

	testErr := errors.New("token is expired")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = repo.classifyAuthError(testErr)
	}
}

func BenchmarkAuthRepository_SafeTokenLog(b *testing.B) {
	logger := setupTestLogger()
	repo, err := NewAuthRepository(
		testIssuer,
		[]string{testAudience},
		testJWKSURL,
		time.Minute,
		logger,
	)
	require.NoError(b, err)

	longToken := "eyJhbGciOiJQUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.invalid-signature"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = repo.safeTokenLog(longToken)
	}
}

func BenchmarkAuthRepository_GetMetrics(b *testing.B) {
	logger := setupTestLogger()
	repo, err := NewAuthRepository(
		testIssuer,
		[]string{testAudience},
		testJWKSURL,
		time.Minute,
		logger,
	)
	require.NoError(b, err)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = repo.GetMetrics()
	}
}

// TestMain for setup and teardown
func TestMain(m *testing.M) {
	// Run tests
	m.Run()
}
