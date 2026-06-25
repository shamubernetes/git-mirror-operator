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
	"github.com/shamubernetes/git-mirror-operator/internal/jobs"
	"github.com/shamubernetes/git-mirror-operator/internal/webhook"
	batchv1 "k8s.io/api/batch/v1"
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

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
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
			Mirror: mirrorv1alpha1.MirrorSpec{Mode: "exact", IncludeTags: true},
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

func TestUnsupportedGitHubEventIsRejected(t *testing.T) {
	server := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-GitHub-Event", "issues")
	rec := httptest.NewRecorder()

	server.HandleGitHub(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for unsupported event, got %d body %q", rec.Code, rec.Body.String())
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
	if mirror.Status.LastJobName == "" {
		t.Fatal("expected last job name recorded")
	}
	if mirror.Status.LastRequestedRevision != "abc123" {
		t.Fatalf("expected requested revision recorded, got %q", mirror.Status.LastRequestedRevision)
	}
	if mirror.Status.LastMirroredRevision != "" {
		t.Fatalf("did not expect mirrored revision before job success, got %q", mirror.Status.LastMirroredRevision)
	}
	var jobList batchv1.JobList
	if err := server.Client().List(context.Background(), &jobList); err != nil {
		t.Fatal(err)
	}
	if len(jobList.Items) != 1 {
		t.Fatalf("expected one sync job, got %d", len(jobList.Items))
	}
	if jobList.Items[0].Name != mirror.Status.LastJobName {
		t.Fatalf("expected status job %q to match created job %q", mirror.Status.LastJobName, jobList.Items[0].Name)
	}
	if got := jobList.Items[0].Annotations[jobs.AnnotationRevision]; got != "abc123" {
		t.Fatalf("expected job revision annotation, got %q", got)
	}
}

func TestPushEventReadsWebhookSecretFromConfiguredReader(t *testing.T) {
	body := []byte(`{"repository":{"full_name":"example/source-repo"},"after":"abc123"}`)
	scheme := testScheme(t)
	mainClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(testMirror()).Build()
	secretReader := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(testSecret()).Build()
	server := webhook.NewServerWithReaders(mainClient, secretReader, "example/git-mirror-sync:dev", scheme)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "delivery-1")
	req.Header.Set("X-Hub-Signature-256", signed(body, "webhook-secret"))
	rec := httptest.NewRecorder()

	server.HandleGitHub(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected accepted push, got %d body %q", rec.Code, rec.Body.String())
	}
}

func TestPushEventIgnoresDuplicateDelivery(t *testing.T) {
	body := []byte(`{"repository":{"full_name":"example/source-repo"},"after":"abc123"}`)
	mirror := testMirror()
	mirror.Status.LastDeliveryID = "delivery-1"
	server := testServer(t, mirror, testSecret())
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "delivery-1")
	req.Header.Set("X-Hub-Signature-256", signed(body, "webhook-secret"))
	rec := httptest.NewRecorder()

	server.HandleGitHub(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected accepted duplicate, got %d body %q", rec.Code, rec.Body.String())
	}
	var jobList batchv1.JobList
	if err := server.Client().List(context.Background(), &jobList); err != nil {
		t.Fatal(err)
	}
	if len(jobList.Items) != 0 {
		t.Fatalf("expected duplicate delivery to create no jobs, got %d", len(jobList.Items))
	}
}

func TestPushEventCoalescesWhenActiveJobExists(t *testing.T) {
	body := []byte(`{"repository":{"full_name":"example/source-repo"},"after":"def456"}`)
	mirror := testMirror()
	activeJob, err := jobs.BuildSyncJob(mirror, jobs.Options{DefaultImage: "example/git-mirror-sync:dev", TriggerID: "delivery-1"})
	if err != nil {
		t.Fatal(err)
	}
	server := testServer(t, mirror, testSecret(), activeJob.Job)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "delivery-2")
	req.Header.Set("X-Hub-Signature-256", signed(body, "webhook-secret"))
	rec := httptest.NewRecorder()

	server.HandleGitHub(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected accepted coalesced push, got %d body %q", rec.Code, rec.Body.String())
	}
	var mirrorAfter mirrorv1alpha1.GitMirror
	if err := server.Client().Get(context.Background(), webhook.ObjectKey("mirrors", "source-repo"), &mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if !mirrorAfter.Status.PendingResync {
		t.Fatal("expected pending resync")
	}
	if mirrorAfter.Status.LastRequestedRevision != "def456" {
		t.Fatalf("expected requested revision recorded, got %q", mirrorAfter.Status.LastRequestedRevision)
	}
	if mirrorAfter.Status.LastMirroredRevision != "" {
		t.Fatalf("did not expect mirrored revision before job success, got %q", mirrorAfter.Status.LastMirroredRevision)
	}
	var jobList batchv1.JobList
	if err := server.Client().List(context.Background(), &jobList); err != nil {
		t.Fatal(err)
	}
	if len(jobList.Items) != 1 {
		t.Fatalf("expected no additional job while active job exists, got %d", len(jobList.Items))
	}
}

func TestPushEventAdoptsExistingJobForSameDelivery(t *testing.T) {
	body := []byte(`{"repository":{"full_name":"example/source-repo"},"after":"abc123"}`)
	mirror := testMirror()
	existingJob, err := jobs.BuildSyncJob(mirror, jobs.Options{DefaultImage: "example/git-mirror-sync:dev", TriggerID: "delivery-1"})
	if err != nil {
		t.Fatal(err)
	}
	server := testServer(t, mirror, testSecret(), existingJob.Job)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "delivery-1")
	req.Header.Set("X-Hub-Signature-256", signed(body, "webhook-secret"))
	rec := httptest.NewRecorder()

	server.HandleGitHub(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected accepted existing delivery, got %d body %q", rec.Code, rec.Body.String())
	}
	var mirrorAfter mirrorv1alpha1.GitMirror
	if err := server.Client().Get(context.Background(), webhook.ObjectKey("mirrors", "source-repo"), &mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if mirrorAfter.Status.PendingResync {
		t.Fatal("did not expect pending resync for the same delivery's existing job")
	}
	if mirrorAfter.Status.LastJobName != existingJob.Job.Name {
		t.Fatalf("expected status to adopt existing job %q, got %q", existingJob.Job.Name, mirrorAfter.Status.LastJobName)
	}
	if mirrorAfter.Status.LastRequestedRevision != "abc123" {
		t.Fatalf("expected requested revision recorded, got %q", mirrorAfter.Status.LastRequestedRevision)
	}
	if mirrorAfter.Status.LastMirroredRevision != "" {
		t.Fatalf("did not expect mirrored revision before job success, got %q", mirrorAfter.Status.LastMirroredRevision)
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

func TestPushEventRejectsMissingDeliveryID(t *testing.T) {
	body := []byte(`{"repository":{"full_name":"example/source-repo"},"after":"abc123"}`)
	server := testServer(t, testMirror(), testSecret())
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", signed(body, "webhook-secret"))
	rec := httptest.NewRecorder()

	server.HandleGitHub(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d body %q", rec.Code, rec.Body.String())
	}
}
