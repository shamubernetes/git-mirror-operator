package controller

import (
	"context"
	"testing"
	"time"

	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileFallbackPersistsCreatedJobName(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mirror := &mirrorv1alpha1.GitMirror{
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
			Source: mirrorv1alpha1.GitEndpointSpec{
				URL:          "git@github.com:example/source-repo.git",
				SSHSecretRef: mirrorv1alpha1.SecretKeyRef{Name: "source-key", Key: "ssh-privatekey"},
			},
			Target: mirrorv1alpha1.GitEndpointSpec{
				URL:          "git@codeberg.org:example/source-repo.git",
				SSHSecretRef: mirrorv1alpha1.SecretKeyRef{Name: "target-key", Key: "ssh-privatekey"},
			},
			Mirror:   mirrorv1alpha1.MirrorSpec{Mode: "exact", IncludeTags: true, Prune: true},
			Fallback: mirrorv1alpha1.FallbackSpec{Schedule: "0 * * * *"},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mirrorv1alpha1.GitMirror{}).
		WithObjects(mirror).
		Build()
	reconciler := &GitMirrorReconciler{
		Client:           c,
		Scheme:           scheme,
		DefaultSyncImage: "example/git-mirror-sync:dev",
		Clock:            func() time.Time { return time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC) },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mirror)}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var jobs batchv1.JobList
	if err := c.List(context.Background(), &jobs, client.InNamespace("mirrors")); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("expected one job, got %d", len(jobs.Items))
	}
	var mirrorAfter mirrorv1alpha1.GitMirror
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(mirror), &mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if mirrorAfter.Status.LastJobName == "" {
		t.Fatal("expected persisted last job name")
	}
	if mirrorAfter.Status.LastJobName != jobs.Items[0].Name {
		t.Fatalf("expected status job %q to match created job %q", mirrorAfter.Status.LastJobName, jobs.Items[0].Name)
	}
}
