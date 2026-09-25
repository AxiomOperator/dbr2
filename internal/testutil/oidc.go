// SPDX-License-Identifier: Apache-2.0

//go:build integration

package testutil

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// FakeOIDC is a minimal OpenID Provider (discovery, JWKS, token endpoint with
// PKCE verification) that behaves like Entra ID for the claims DBR² uses.
type FakeOIDC struct {
	*httptest.Server
	ClientID string
	key      *rsa.PrivateKey

	mu    sync.Mutex
	codes map[string]pendingCode
}

// Identity is what the fake IdP asserts for a user.
type Identity struct {
	Subject, Name, PreferredUsername, Email string
	Groups                                  []string
}

type pendingCode struct {
	id            Identity
	nonce         string
	codeChallenge string
}

// NewFakeOIDC starts the provider.
func NewFakeOIDC(t *testing.T, clientID string) *FakeOIDC {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &FakeOIDC{ClientID: clientID, key: key, codes: map[string]pendingCode{}}
	mux := http.NewServeMux()
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": f.URL, "authorization_endpoint": f.URL + "/authorize", "token_endpoint": f.URL + "/token",
			"jwks_uri": f.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", f.token(t))
	return f
}

// Authorize simulates the user signing in at the IdP: it parses DBR²'s
// authorization URL and returns the code DBR² will receive on its callback.
func (f *FakeOIDC) Authorize(t *testing.T, authURL string, id Identity) (code, state string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("client_id") != f.ClientID || q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" {
		t.Fatalf("authorization request missing PKCE/nonce/client_id: %s", authURL)
	}
	code = base64.RawURLEncoding.EncodeToString([]byte(time.Now().String()))
	f.mu.Lock()
	f.codes[code] = pendingCode{id: id, nonce: q.Get("nonce"), codeChallenge: q.Get("code_challenge")}
	f.mu.Unlock()
	return code, q.Get("state")
}

func (f *FakeOIDC) token(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		pc, ok := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != pc.codeChallenge {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key},
			(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
		if err != nil {
			t.Error(err)
			return
		}
		now := time.Now()
		claims := map[string]any{
			"iss": f.URL, "sub": pc.id.Subject, "aud": f.ClientID, "exp": now.Add(time.Hour).Unix(), "iat": now.Unix(),
			"nonce": pc.nonce, "name": pc.id.Name, "preferred_username": pc.id.PreferredUsername,
			"email": pc.id.Email, "groups": pc.id.Groups,
		}
		raw, err := jwt.Signed(signer).Claims(claims).Serialize()
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": raw, "expires_in": 3600})
	}
}
