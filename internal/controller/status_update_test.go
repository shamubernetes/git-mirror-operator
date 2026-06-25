package controller

import (
	"context"
	"testing"
	"time"

	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPatchGitMirrorStatusPreservesUnrelatedCurrentFields(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	successAt := metav1.NewTime(time.Date(2026, 6, 24, 10, 5, 0, 0, time.UTC))
	mirror := &mirrorv1alpha1.GitMirror{
		ObjectMeta: metav1.ObjectMeta{Name: "source-repo", Namespace: "mirrors"},
		Status: mirrorv1alpha1.GitMirrorStatus{
			LastJobName:           "gitmirror-source-repo-abc123",
			LastCompletedJobName:  "gitmirror-source-repo-abc123",
			LastSuccessAt:         successAt.DeepCopy(),
			LastRequestedRevision: "abc123",
			LastMirroredRevision:  "abc123",
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mirrorv1alpha1.GitMirror{}).
		WithObjects(mirror).
		Build()

	webhookAt := metav1.NewTime(time.Date(2026, 6, 24, 10, 6, 0, 0, time.UTC))
	if err := PatchGitMirrorStatus(context.Background(), c, client.ObjectKeyFromObject(mirror), func(current *mirrorv1alpha1.GitMirror) {
		ApplyWebhookIntent(current, "delivery-2", "def456", webhookAt)
	}); err != nil {
		t.Fatalf("patch status: %v", err)
	}

	var updated mirrorv1alpha1.GitMirror
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(mirror), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.LastCompletedJobName != "gitmirror-source-repo-abc123" {
		t.Fatalf("expected completed job to be preserved, got %q", updated.Status.LastCompletedJobName)
	}
	if updated.Status.LastSuccessAt == nil || !updated.Status.LastSuccessAt.Equal(&successAt) {
		t.Fatalf("expected success timestamp to be preserved, got %#v", updated.Status.LastSuccessAt)
	}
	if updated.Status.LastMirroredRevision != "abc123" {
		t.Fatalf("expected mirrored revision to be preserved, got %q", updated.Status.LastMirroredRevision)
	}
	if updated.Status.LastDeliveryID != "delivery-2" {
		t.Fatalf("expected webhook delivery recorded, got %q", updated.Status.LastDeliveryID)
	}
	if updated.Status.LastRequestedRevision != "def456" {
		t.Fatalf("expected requested revision advanced, got %q", updated.Status.LastRequestedRevision)
	}
}
