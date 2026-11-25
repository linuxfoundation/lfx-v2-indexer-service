// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Current writes authenticate the authorization credential without ParsePrincipals.
func TestAuthRepository_ValidateToken_SignedCurrentCredential(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{
			"kty": "RSA", "alg": "PS256", "use": "sig", "kid": "fixture-key",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB",
		}}})
	}))
	defer server.Close()
	repository, err := NewAuthRepository(testIssuer, []string{testAudience}, server.URL, time.Minute, setupTestLogger())
	require.NoError(t, err)
	for _, tt := range []struct {
		name     string
		change   func(map[string]any)
		wrongKey bool
		valid    bool
	}{
		{name: "valid", valid: true},
		{name: "expired", change: func(c map[string]any) { c["exp"] = time.Now().Add(-2 * time.Hour).Unix() }},
		{name: "future", change: func(c map[string]any) { c["nbf"] = time.Now().Add(2 * time.Hour).Unix() }},
		{name: "wrong issuer", change: func(c map[string]any) { c["iss"] = "https://other.invalid/" }},
		{name: "wrong audience", change: func(c map[string]any) { c["aud"] = "other-audience" }},
		{name: "wrong signature", wrongKey: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			claims := map[string]any{
				"iss": testIssuer, "aud": testAudience, "exp": time.Now().Add(5 * time.Minute).Unix(),
				"principal": "fixture-publisher@clients", "sub": "fixture-subject",
			}
			if tt.change != nil {
				tt.change(claims)
			}
			signer := key
			if tt.wrongKey {
				signer = otherKey
			}
			principal, err := repository.ValidateToken(context.Background(), "Bearer "+signedActorToken(t, signer, claims))
			if tt.valid {
				require.NoError(t, err)
				require.Equal(t, "fixture-publisher@clients", principal.Principal)
			} else {
				require.Error(t, err)
				require.Nil(t, principal)
			}
		})
	}
}
