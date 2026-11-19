// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package auth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func signedActorToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	input := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"PS256","kid":"fixture-key","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, digest[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: crypto.SHA256})
	require.NoError(t, err)
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestAuthRepository_ValidateActorToken(t *testing.T) {
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
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })
	repository, err := NewAuthRepository(testIssuer, []string{testAudience}, server.URL, time.Minute, logger)
	require.NoError(t, err)
	for _, tt := range []struct {
		name     string
		change   func(map[string]any)
		wrongKey bool
		prefix   string
		valid    bool
	}{
		{name: "valid", valid: true},
		{name: "bearer", prefix: "bEaReR ", valid: true},
		{name: "within configured skew", change: func(c map[string]any) { c["exp"] = time.Now().Add(-30 * time.Second).Unix() }, valid: true},
		{name: "expired beyond skew", change: func(c map[string]any) { c["exp"] = time.Now().Add(-2 * time.Hour).Unix() }},
		{name: "not yet valid", change: func(c map[string]any) { c["nbf"] = time.Now().Add(2 * time.Hour).Unix() }},
		{name: "wrong issuer", change: func(c map[string]any) { c["iss"] = "https://other.invalid/" }},
		{name: "wrong audience", change: func(c map[string]any) { c["aud"] = "other-audience" }},
		{name: "wrong signature", wrongKey: true},
		{name: "sub cannot replace principal", change: func(c map[string]any) { delete(c, "principal"); c["sub"] = "testuser01" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			claims := map[string]any{
				"iss": testIssuer, "aud": testAudience, "exp": time.Now().Add(5 * time.Minute).Unix(),
				"principal": "testuser01", "sub": "fixture-subject", "email": "user-1@example.com",
				"name": "Private Display Marker", "picture": "https://example.com/private-avatar-marker",
			}
			if tt.change != nil {
				tt.change(claims)
			}
			signer := key
			if tt.wrongKey {
				signer = otherKey
			}
			token := signedActorToken(t, signer, claims)
			got, err := repository.ValidateActorToken(context.Background(), tt.prefix+token)
			if tt.valid {
				require.NoError(t, err)
				require.Equal(t, "testuser01", got.Principal)
				require.Equal(t, "user-1@example.com", got.Email)
			} else {
				require.EqualError(t, err, "invalid actor evidence")
				require.Nil(t, got)
			}
			for _, secret := range []string{token, "testuser01", "user-1@example.com", "Private Display Marker", "private-avatar-marker"} {
				require.NotContains(t, logs.String(), secret)
			}
		})
	}
	for _, token := range []string{"", "Bearer ", "invalid.jwt.token", strings.Repeat("x", 100)} {
		_, err := repository.ValidateActorToken(context.Background(), token)
		require.EqualError(t, err, "invalid actor evidence")
	}
}
