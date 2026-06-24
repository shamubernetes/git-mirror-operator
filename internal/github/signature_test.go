package github_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/shamubernetes/git-mirror-operator/internal/github"
)

func TestVerifySignatureAcceptsValidSHA256Signature(t *testing.T) {
	body := []byte(`{"repository":{"full_name":"example/source-repo"}}`)
	mac := hmac.New(sha256.New, []byte("webhook-secret"))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !github.VerifySignature256(body, signature, []byte("webhook-secret")) {
		t.Fatal("expected valid signature to be accepted")
	}
}

func TestVerifySignatureRejectsInvalidSHA256Signature(t *testing.T) {
	body := []byte(`{"repository":{"full_name":"example/source-repo"}}`)

	if github.VerifySignature256(body, "sha256=bad", []byte("webhook-secret")) {
		t.Fatal("expected invalid signature to be rejected")
	}
}

func TestExtractRepositoryFullNameReadsMinimalPayload(t *testing.T) {
	body := []byte(`{"repository":{"full_name":"example/source-repo"},"ignored":{"large":true}}`)

	fullName, err := github.ExtractRepositoryFullName(body)
	if err != nil {
		t.Fatalf("expected repository name: %v", err)
	}
	if fullName != "example/source-repo" {
		t.Fatalf("expected repository full_name, got %q", fullName)
	}
}
