package webhook_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	"github.com/shamubernetes/git-mirror-operator/internal/webhook"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func signed(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func testServer(t *testing.T, objects ...runtime.Object) *webhook.Server {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return webhook.NewServer(fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build(), "example/git-mirror-sync:dev")
}

func testMirror() *mirrorv1alpha1.GitMirror {
	return &mirrorv1alpha1.GitMirror{
		ObjectMeta: metav1.ObjectMeta{Name: "source-repo", Namespace: "mirrors"},
		Spec: mirrorv1alpha1.GitMirrorSpec{
			Provider: "github",
			GitHub: mirrorv1alpha1.GitHubSpec{
				Owner: "example",
				Repo:  "source-repo",
				WebhookSecretRef: mirrorv1alpha1.SecretKeyRef{
					Name: "webhook-secret",
					Key:  "secret",
				},
			},
			Source: mirrorv1alpha1.GitEndpointSpec{URL: "git@github.com:example/source-repo.git", SSHSecretRef: mirrorv1alpha1.SecretKeyRef{Name: "source-key", Key: "ssh-privatekey"}},
			Target: mirrorv1alpha1.GitEndpointSpec{URL: "git@codeberg.org:example/source-repo.git", SSHSecretRef: mirrorv1alpha1.SecretKeyRef{Name: "target-key", Key: "ssh-privatekey"}},
			Mirror: mirrorv1alpha1.MirrorSpec{Mode: "exact", IncludeTags: true, Prune: true},
		},
	}
}

func testSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "webhook-secret", Namespace: "mirrors"},
		Data:       map[string][]byte{"secret": []byte("webhook-secret")},
	}
}

func TestPingEventIsAccepted(t *testing.T) {
	server := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-GitHub-Event", "ping")
	rec := httptest.NewRecorder()

	server.HandleGitHub(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected accepted ping, got %d body %q", rec.Code, rec.Body.String())
	}
}

func TestPushEventSchedulesKnownRepository(t *testing.T) {
	body := []byte(`{"repository":{"full_name":"example/source-repo"},"after":"abc123"}`)
	server := testServer(t, testMirror(), testSecret())
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "delivery-1")
	req.Header.Set("X-Hub-Signature-256", signed(body, "webhook-secret"))
	rec := httptest.NewRecorder()

	server.HandleGitHub(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected accepted push, got %d body %q", rec.Code, rec.Body.String())
	}
	var mirror mirrorv1alpha1.GitMirror
	if err := server.Client().Get(context.Background(), webhook.ObjectKey("mirrors", "source-repo"), &mirror); err != nil {
		t.Fatal(err)
	}
	if mirror.Status.LastDeliveryID != "delivery-1" {
		t.Fatalf("expected delivery recorded, got %q", mirror.Status.LastDeliveryID)
	}
}

func TestPushEventRejectsUnknownRepository(t *testing.T) {
	body := []byte(`{"repository":{"full_name":"unknown/repo"}}`)
	server := testServer(t, testMirror(), testSecret())
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "delivery-1")
	req.Header.Set("X-Hub-Signature-256", signed(body, "webhook-secret"))
	rec := httptest.NewRecorder()

	server.HandleGitHub(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected not found, got %d body %q", rec.Code, rec.Body.String())
	}
}

func TestPushEventRejectsInvalidSignature(t *testing.T) {
	body := []byte(`{"repository":{"full_name":"example/source-repo"}}`)
	server := testServer(t, testMirror(), testSecret())
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "delivery-1")
	req.Header.Set("X-Hub-Signature-256", "sha256=bad")
	rec := httptest.NewRecorder()

	server.HandleGitHub(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d body %q", rec.Code, rec.Body.String())
	}
}
