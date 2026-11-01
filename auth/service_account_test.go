package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func generateTestRSAKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	b := x509.MarshalPKCS1PrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: b}))
}

func testServiceAccountJSON(t *testing.T) []byte {
	t.Helper()
	type saKey struct {
		Type         string `json:"type"`
		ProjectID    string `json:"project_id"`
		PrivateKeyID string `json:"private_key_id"`
		PrivateKey   string `json:"private_key"`
		ClientEmail  string `json:"client_email"`
		ClientID     string `json:"client_id"`
		AuthURI      string `json:"auth_uri"`
		TokenURI     string `json:"token_uri"`
	}
	raw, err := json.Marshal(saKey{
		Type:         "service_account",
		ProjectID:    "test-project",
		PrivateKeyID: "key-id",
		PrivateKey:   generateTestRSAKey(t),
		ClientEmail:  "gtm-bot@test-project.iam.gserviceaccount.com",
		ClientID:     "123456789",
		AuthURI:      "https://accounts.google.com/o/oauth2/auth",
		TokenURI:     "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatalf("marshal SA JSON: %v", err)
	}
	return raw
}

func TestNewServiceAccountTokenSource_InvalidJSON(t *testing.T) {
	_, err := NewServiceAccountTokenSource(context.Background(), "not-valid-json")
	if err == nil {
		t.Fatal("expected error for invalid JSON key")
	}
}

func TestNewServiceAccountTokenSource_ValidKey(t *testing.T) {
	keyJSON := testServiceAccountJSON(t)
	ts, err := NewServiceAccountTokenSource(context.Background(), string(keyJSON))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts == nil {
		t.Fatal("expected non-nil token source")
	}
}

func TestGetSATokenSource_FromContext(t *testing.T) {
	mockTS := oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: "sa-token",
		Expiry:      time.Now().Add(time.Hour),
	})

	ctx := context.WithValue(context.Background(), SATokenSourceKey, mockTS)
	got := GetSATokenSource(ctx)
	if got == nil {
		t.Fatal("expected token source from context")
	}
	tok, err := got.Token()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok.AccessToken != "sa-token" {
		t.Errorf("expected 'sa-token', got %q", tok.AccessToken)
	}
}

func TestGetSATokenSource_MissingFromContext(t *testing.T) {
	got := GetSATokenSource(context.Background())
	if got != nil {
		t.Fatal("expected nil when not in context")
	}
}
