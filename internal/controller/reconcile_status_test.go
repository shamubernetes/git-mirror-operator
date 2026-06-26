package controller

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	"github.com/shamubernetes/git-mirror-operator/internal/jobs"
	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
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
				SSHSecretRef: &mirrorv1alpha1.SecretKeyRef{Name: "source-key", Key: "ssh-privatekey"},
			},
			Target: mirrorv1alpha1.GitEndpointSpec{
				URL:          "git@codeberg.org:example/source-repo.git",
				SSHSecretRef: &mirrorv1alpha1.SecretKeyRef{Name: "target-key", Key: "ssh-privatekey"},
			},
			Mirror:   mirrorv1alpha1.MirrorSpec{Mode: "exact", IncludeTags: true},
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

	var jobList batchv1.JobList
	if err := c.List(context.Background(), &jobList, client.InNamespace("mirrors")); err != nil {
		t.Fatal(err)
	}
	if len(jobList.Items) != 1 {
		t.Fatalf("expected one job, got %d", len(jobList.Items))
	}
	var mirrorAfter mirrorv1alpha1.GitMirror
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(mirror), &mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if mirrorAfter.Status.LastJobName == "" {
		t.Fatal("expected persisted last job name")
	}
	if mirrorAfter.Status.LastJobName != jobList.Items[0].Name {
		t.Fatalf("expected status job %q to match created job %q", mirrorAfter.Status.LastJobName, jobList.Items[0].Name)
	}
}

func TestReconcileCompletedJobRecordsRevisionAndSchedulesFollowup(t *testing.T) {
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
				SSHSecretRef: &mirrorv1alpha1.SecretKeyRef{Name: "source-key", Key: "ssh-privatekey"},
			},
			Target: mirrorv1alpha1.GitEndpointSpec{
				URL:          "git@codeberg.org:example/source-repo.git",
				SSHSecretRef: &mirrorv1alpha1.SecretKeyRef{Name: "target-key", Key: "ssh-privatekey"},
			},
			Mirror: mirrorv1alpha1.MirrorSpec{Mode: "exact", IncludeTags: true},
		},
		Status: mirrorv1alpha1.GitMirrorStatus{
			LastJobName:           "gitmirror-source-repo-active",
			LastRequestedRevision: "def456",
			PendingResync:         true,
		},
	}
	completedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gitmirror-source-repo-active",
			Namespace: "mirrors",
			Labels: map[string]string{
				jobs.LabelGitMirror: "source-repo",
			},
			Annotations: map[string]string{
				jobs.AnnotationRevision: "abc123",
			},
		},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type:   batchv1.JobComplete,
			Status: corev1.ConditionTrue,
		}}},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mirrorv1alpha1.GitMirror{}).
		WithObjects(mirror, completedJob).
		Build()
	reconciler := &GitMirrorReconciler{
		Client:           c,
		Scheme:           scheme,
		DefaultSyncImage: "example/git-mirror-sync:dev",
		Clock:            func() time.Time { return time.Date(2026, 6, 24, 10, 10, 0, 0, time.UTC) },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mirror)}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var jobList batchv1.JobList
	if err := c.List(context.Background(), &jobList, client.InNamespace("mirrors")); err != nil {
		t.Fatal(err)
	}
	if len(jobList.Items) != 2 {
		t.Fatalf("expected completed job and follow-up job, got %d", len(jobList.Items))
	}
	var followup *batchv1.Job
	for i := range jobList.Items {
		if jobList.Items[i].Name != completedJob.Name {
			followup = &jobList.Items[i]
			break
		}
	}
	if followup == nil {
		t.Fatal("expected follow-up job")
	}
	if got := followup.Annotations[jobs.AnnotationRevision]; got != "def456" {
		t.Fatalf("expected follow-up revision annotation, got %q", got)
	}
	var mirrorAfter mirrorv1alpha1.GitMirror
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(mirror), &mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if mirrorAfter.Status.LastCompletedJobName != completedJob.Name {
		t.Fatalf("expected completed job recorded, got %q", mirrorAfter.Status.LastCompletedJobName)
	}
	if mirrorAfter.Status.LastMirroredRevision != "abc123" {
		t.Fatalf("expected completed revision recorded, got %q", mirrorAfter.Status.LastMirroredRevision)
	}
	if mirrorAfter.Status.LastJobName != followup.Name {
		t.Fatalf("expected last job to be follow-up %q, got %q", followup.Name, mirrorAfter.Status.LastJobName)
	}
	if mirrorAfter.Status.PendingResync {
		t.Fatal("expected pending resync cleared")
	}
}

func TestReconcileCompletedJobWithLongMirrorNameUsesSafeSelector(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mirror := testGitMirror()
	mirror.Name = "source-repo-" + strings.Repeat("very-long-name-", 8)
	mirror.Spec.GitHub.Owner = "owner-" + strings.Repeat("long-", 20)
	mirror.Spec.GitHub.Repo = "repo-" + strings.Repeat("long-", 20)
	mirror.Status.LastJobName = "gitmirror-source-repo-active"
	labels := jobs.LabelsForMirror(mirror)
	completedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gitmirror-source-repo-active",
			Namespace: "mirrors",
			Labels: map[string]string{
				jobs.LabelGitMirror: labels[jobs.LabelGitMirror],
			},
			Annotations: map[string]string{
				jobs.AnnotationRevision: "abc123",
			},
		},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type:   batchv1.JobComplete,
			Status: corev1.ConditionTrue,
		}}},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mirrorv1alpha1.GitMirror{}).
		WithObjects(mirror, completedJob).
		Build()
	reconciler := &GitMirrorReconciler{
		Client:           c,
		Scheme:           scheme,
		DefaultSyncImage: "example/git-mirror-sync:dev",
		Clock:            func() time.Time { return time.Date(2026, 6, 24, 10, 12, 0, 0, time.UTC) },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mirror)}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var mirrorAfter mirrorv1alpha1.GitMirror
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(mirror), &mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if mirrorAfter.Status.LastMirroredRevision != "abc123" {
		t.Fatalf("expected completed revision recorded through safe selector, got %q", mirrorAfter.Status.LastMirroredRevision)
	}
}

func TestReconcilePendingWebhookIntentCreatesLockedJob(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mirror := testGitMirror()
	mirror.Status.LastDeliveryID = "delivery-1"
	mirror.Status.LastRequestedRevision = "abc123"
	mirror.Status.PendingResync = true
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mirrorv1alpha1.GitMirror{}).
		WithObjects(mirror).
		Build()
	reconciler := &GitMirrorReconciler{
		Client:           c,
		Scheme:           scheme,
		DefaultSyncImage: "example/git-mirror-sync:dev",
		Clock:            func() time.Time { return time.Date(2026, 6, 24, 10, 15, 0, 0, time.UTC) },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mirror)}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var jobList batchv1.JobList
	if err := c.List(context.Background(), &jobList, client.InNamespace("mirrors")); err != nil {
		t.Fatal(err)
	}
	if len(jobList.Items) != 1 {
		t.Fatalf("expected one job, got %d", len(jobList.Items))
	}
	job := jobList.Items[0]
	if got := job.Annotations[jobs.AnnotationRevision]; got != "abc123" {
		t.Fatalf("expected revision annotation, got %q", got)
	}
	var lease coordinationv1.Lease
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "mirrors", Name: SyncLeaseName(mirror)}, &lease); err != nil {
		t.Fatal(err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != job.Name {
		t.Fatalf("expected lease holder %q, got %#v", job.Name, lease.Spec.HolderIdentity)
	}
	var mirrorAfter mirrorv1alpha1.GitMirror
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(mirror), &mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if mirrorAfter.Status.PendingResync {
		t.Fatal("expected pending intent cleared after scheduling")
	}
	if mirrorAfter.Status.LastJobName != job.Name {
		t.Fatalf("expected last job %q, got %q", job.Name, mirrorAfter.Status.LastJobName)
	}
	if mirrorAfter.Status.LastTriggeredAt == nil {
		t.Fatal("expected trigger timestamp")
	}
}

func TestReconcileUnobservedGenerationSchedulesPendingRevision(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mirror := testGitMirror()
	mirror.Generation = 2
	mirror.Status.ObservedGeneration = 1
	mirror.Status.LastDeliveryID = "delivery-3"
	mirror.Status.LastRequestedRevision = "fedcba"
	mirror.Status.PendingResync = true
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mirrorv1alpha1.GitMirror{}).
		WithObjects(mirror).
		Build()
	reconciler := &GitMirrorReconciler{
		Client:           c,
		Scheme:           scheme,
		DefaultSyncImage: "example/git-mirror-sync:dev",
		Clock:            func() time.Time { return time.Date(2026, 6, 24, 10, 17, 0, 0, time.UTC) },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mirror)}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var jobList batchv1.JobList
	if err := c.List(context.Background(), &jobList, client.InNamespace("mirrors")); err != nil {
		t.Fatal(err)
	}
	if len(jobList.Items) != 1 {
		t.Fatalf("expected one job, got %d", len(jobList.Items))
	}
	job := jobList.Items[0]
	if got := job.Annotations[jobs.AnnotationGeneration]; got != "2" {
		t.Fatalf("expected generation annotation 2, got %q", got)
	}
	if got := job.Annotations[jobs.AnnotationRevision]; got != "fedcba" {
		t.Fatalf("expected revision annotation, got %q", got)
	}
	var mirrorAfter mirrorv1alpha1.GitMirror
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(mirror), &mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if mirrorAfter.Status.ObservedGeneration != 2 {
		t.Fatalf("expected observed generation 2, got %d", mirrorAfter.Status.ObservedGeneration)
	}
	if mirrorAfter.Status.LastJobName != job.Name {
		t.Fatalf("expected last job %q, got %q", job.Name, mirrorAfter.Status.LastJobName)
	}
	if mirrorAfter.Status.PendingResync {
		t.Fatal("expected pending intent cleared after scheduling unobserved generation")
	}
}

func TestReconcilePendingWebhookIntentWaitsForHeldLease(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mirror := testGitMirror()
	mirror.Status.LastDeliveryID = "delivery-2"
	mirror.Status.LastRequestedRevision = "def456"
	mirror.Status.PendingResync = true
	holder := "gitmirror-source-repo-existing"
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: SyncLeaseName(mirror), Namespace: "mirrors"},
		Spec:       coordinationv1.LeaseSpec{HolderIdentity: &holder},
	}
	activeJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: holder, Namespace: "mirrors"},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mirrorv1alpha1.GitMirror{}).
		WithObjects(mirror, lease, activeJob).
		Build()
	reconciler := &GitMirrorReconciler{
		Client:           c,
		Scheme:           scheme,
		DefaultSyncImage: "example/git-mirror-sync:dev",
		Clock:            func() time.Time { return time.Date(2026, 6, 24, 10, 20, 0, 0, time.UTC) },
	}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mirror)})
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if result.RequeueAfter == 0 {
		t.Fatal("expected requeue while lease is held")
	}
	var jobList batchv1.JobList
	if err := c.List(context.Background(), &jobList, client.InNamespace("mirrors")); err != nil {
		t.Fatal(err)
	}
	if len(jobList.Items) != 1 {
		t.Fatalf("expected only lease-holder job, got %d", len(jobList.Items))
	}
	var mirrorAfter mirrorv1alpha1.GitMirror
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(mirror), &mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if !mirrorAfter.Status.PendingResync {
		t.Fatal("expected pending intent to remain queued")
	}
}

func TestReconcilePendingWebhookIntentWaitsForCurrentGenerationLeaseHolder(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mirror := testGitMirror()
	mirror.Generation = 2
	mirror.Status.ObservedGeneration = 2
	mirror.Status.LastDeliveryID = "delivery-current-holder"
	mirror.Status.LastRequestedRevision = "def456"
	mirror.Status.PendingResync = true
	holder := "gitmirror-source-repo-current-holder"
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: SyncLeaseName(mirror), Namespace: "mirrors"},
		Spec:       coordinationv1.LeaseSpec{HolderIdentity: &holder},
	}
	activeJob := generationJob(mirror, holder, 2, nil)
	activeJob.Labels = nil
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mirrorv1alpha1.GitMirror{}).
		WithObjects(mirror, lease, activeJob).
		Build()
	reconciler := &GitMirrorReconciler{
		Client:           c,
		Scheme:           scheme,
		DefaultSyncImage: "example/git-mirror-sync:dev",
		Clock:            func() time.Time { return time.Date(2026, 6, 24, 10, 21, 0, 0, time.UTC) },
	}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mirror)})
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if result.RequeueAfter == 0 {
		t.Fatal("expected requeue while current-generation lease holder is active")
	}
	var jobList batchv1.JobList
	if err := c.List(context.Background(), &jobList, client.InNamespace("mirrors")); err != nil {
		t.Fatal(err)
	}
	if len(jobList.Items) != 1 {
		t.Fatalf("expected only lease-holder job, got %d", len(jobList.Items))
	}
	var mirrorAfter mirrorv1alpha1.GitMirror
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(mirror), &mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if !mirrorAfter.Status.PendingResync {
		t.Fatal("expected pending intent to remain queued")
	}
}

func TestReconcilePendingWebhookIntentReplacesOlderGenerationLeaseHolder(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mirror := testGitMirror()
	mirror.Generation = 2
	mirror.Status.ObservedGeneration = 2
	mirror.Status.LastDeliveryID = "delivery-old-holder"
	mirror.Status.LastRequestedRevision = "def456"
	mirror.Status.PendingResync = true
	holder := "gitmirror-source-repo-old-holder"
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: SyncLeaseName(mirror), Namespace: "mirrors"},
		Spec:       coordinationv1.LeaseSpec{HolderIdentity: &holder},
	}
	activeJob := generationJob(mirror, holder, 1, nil)
	activeJob.Labels = nil
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mirrorv1alpha1.GitMirror{}).
		WithObjects(mirror, lease, activeJob).
		Build()
	reconciler := &GitMirrorReconciler{
		Client:           c,
		Scheme:           scheme,
		DefaultSyncImage: "example/git-mirror-sync:dev",
		Clock:            func() time.Time { return time.Date(2026, 6, 24, 10, 22, 0, 0, time.UTC) },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mirror)}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var jobList batchv1.JobList
	if err := c.List(context.Background(), &jobList, client.InNamespace("mirrors")); err != nil {
		t.Fatal(err)
	}
	if len(jobList.Items) != 2 {
		t.Fatalf("expected old holder plus replacement job, got %d", len(jobList.Items))
	}
	var replacement *batchv1.Job
	for i := range jobList.Items {
		if jobList.Items[i].Name != holder {
			replacement = &jobList.Items[i]
			break
		}
	}
	if replacement == nil {
		t.Fatal("expected replacement job")
	}
	if got := replacement.Annotations[jobs.AnnotationGeneration]; got != "2" {
		t.Fatalf("expected replacement generation 2, got %q", got)
	}
	var updatedLease coordinationv1.Lease
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "mirrors", Name: SyncLeaseName(mirror)}, &updatedLease); err != nil {
		t.Fatal(err)
	}
	if updatedLease.Spec.HolderIdentity == nil || *updatedLease.Spec.HolderIdentity != replacement.Name {
		t.Fatalf("expected lease to move to replacement job %q, got %#v", replacement.Name, updatedLease.Spec.HolderIdentity)
	}
}

func TestReconcilePendingWebhookIntentReplacesStaleLease(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mirror := testGitMirror()
	mirror.Status.LastDeliveryID = "delivery-2"
	mirror.Status.LastRequestedRevision = "def456"
	mirror.Status.PendingResync = true
	holder := "missing-job"
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: SyncLeaseName(mirror), Namespace: "mirrors"},
		Spec:       coordinationv1.LeaseSpec{HolderIdentity: &holder},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mirrorv1alpha1.GitMirror{}).
		WithObjects(mirror, lease).
		Build()
	reconciler := &GitMirrorReconciler{
		Client:           c,
		Scheme:           scheme,
		DefaultSyncImage: "example/git-mirror-sync:dev",
		Clock:            func() time.Time { return time.Date(2026, 6, 24, 10, 25, 0, 0, time.UTC) },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mirror)}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var jobList batchv1.JobList
	if err := c.List(context.Background(), &jobList, client.InNamespace("mirrors")); err != nil {
		t.Fatal(err)
	}
	if len(jobList.Items) != 1 {
		t.Fatalf("expected one replacement job, got %d", len(jobList.Items))
	}
	var updatedLease coordinationv1.Lease
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "mirrors", Name: SyncLeaseName(mirror)}, &updatedLease); err != nil {
		t.Fatal(err)
	}
	if updatedLease.Spec.HolderIdentity == nil || *updatedLease.Spec.HolderIdentity != jobList.Items[0].Name {
		t.Fatalf("expected lease to move to new job %q, got %#v", jobList.Items[0].Name, updatedLease.Spec.HolderIdentity)
	}
}

func TestReconcileLegacyActiveJobBlocksDuplicateAfterUpgrade(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mirror := testGitMirror()
	mirror.Generation = 2
	mirror.Status.ObservedGeneration = 1
	legacyJob := legacyJob(mirror, "gitmirror-source-repo-legacy-active", nil)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mirrorv1alpha1.GitMirror{}).
		WithObjects(mirror, legacyJob).
		Build()
	reconciler := &GitMirrorReconciler{
		Client:           c,
		Scheme:           scheme,
		DefaultSyncImage: "example/git-mirror-sync:dev",
		Clock:            func() time.Time { return time.Date(2026, 6, 24, 10, 27, 0, 0, time.UTC) },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mirror)}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var jobList batchv1.JobList
	if err := c.List(context.Background(), &jobList, client.InNamespace("mirrors")); err != nil {
		t.Fatal(err)
	}
	if len(jobList.Items) != 1 {
		t.Fatalf("expected legacy active job to block duplicate, got %d jobs", len(jobList.Items))
	}
	var mirrorAfter mirrorv1alpha1.GitMirror
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(mirror), &mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if mirrorAfter.Status.ObservedGeneration != 1 {
		t.Fatalf("expected observed generation to remain 1, got %d", mirrorAfter.Status.ObservedGeneration)
	}
}

func TestReconcileLegacyCompletedJobRecordsCompletionBeforeSchedulingNewGeneration(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mirror := testGitMirror()
	mirror.Generation = 2
	mirror.Status.ObservedGeneration = 1
	mirror.Status.LastJobName = "gitmirror-source-repo-legacy-complete"
	legacyJob := legacyJob(mirror, "gitmirror-source-repo-legacy-complete", []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}})
	legacyJob.Annotations[jobs.AnnotationRevision] = "legacy-revision"
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mirrorv1alpha1.GitMirror{}).
		WithObjects(mirror, legacyJob).
		Build()
	reconciler := &GitMirrorReconciler{
		Client:           c,
		Scheme:           scheme,
		DefaultSyncImage: "example/git-mirror-sync:dev",
		Clock:            func() time.Time { return time.Date(2026, 6, 24, 10, 28, 0, 0, time.UTC) },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mirror)}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var mirrorAfter mirrorv1alpha1.GitMirror
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(mirror), &mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if mirrorAfter.Status.LastCompletedJobName != legacyJob.Name {
		t.Fatalf("expected legacy completion recorded, got %q", mirrorAfter.Status.LastCompletedJobName)
	}
	if mirrorAfter.Status.LastMirroredRevision != "legacy-revision" {
		t.Fatalf("expected legacy revision recorded, got %q", mirrorAfter.Status.LastMirroredRevision)
	}
	if mirrorAfter.Status.ObservedGeneration != 2 {
		t.Fatalf("expected current generation scheduled after legacy completion, got observed generation %d", mirrorAfter.Status.ObservedGeneration)
	}
	if mirrorAfter.Status.LastJobName == legacyJob.Name {
		t.Fatal("expected current generation job to become last job after legacy completion")
	}
	var jobList batchv1.JobList
	if err := c.List(context.Background(), &jobList, client.InNamespace("mirrors")); err != nil {
		t.Fatal(err)
	}
	if len(jobList.Items) != 2 {
		t.Fatalf("expected legacy completed job plus current-generation job, got %d", len(jobList.Items))
	}
	var currentJob *batchv1.Job
	for i := range jobList.Items {
		if jobList.Items[i].Name != legacyJob.Name {
			currentJob = &jobList.Items[i]
			break
		}
	}
	if currentJob == nil {
		t.Fatal("expected current-generation job")
	}
	if got := currentJob.Annotations[jobs.AnnotationGeneration]; got != "2" {
		t.Fatalf("expected current-generation annotation 2, got %q", got)
	}
}

func TestReconcileIgnoresStaleGenerationJobsForStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	successAt := metav1.NewTime(time.Date(2026, 6, 24, 10, 30, 0, 0, time.UTC))
	mirror := testGitMirror()
	mirror.Generation = 2
	mirror.Status.ObservedGeneration = 2
	mirror.Status.LastJobName = "gitmirror-source-repo-generation-2"
	mirror.Status.LastCompletedJobName = "gitmirror-source-repo-generation-2"
	mirror.Status.LastSuccessAt = successAt.DeepCopy()
	mirror.Status.LastMirroredRevision = "new-revision"
	staleFailedJob := generationJob(mirror, "gitmirror-source-repo-generation-1-failed", 1, []batchv1.JobCondition{{
		Type:    batchv1.JobFailed,
		Status:  corev1.ConditionTrue,
		Reason:  "BackoffLimitExceeded",
		Message: "old generation failed",
	}})
	staleCompletedJob := generationJob(mirror, "gitmirror-source-repo-generation-1-complete", 1, []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}})
	staleCompletedJob.Annotations[jobs.AnnotationRevision] = "old-revision"
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mirrorv1alpha1.GitMirror{}).
		WithObjects(mirror, staleFailedJob, staleCompletedJob).
		Build()
	reconciler := &GitMirrorReconciler{
		Client:           c,
		Scheme:           scheme,
		DefaultSyncImage: "example/git-mirror-sync:dev",
		Clock:            func() time.Time { return time.Date(2026, 6, 24, 10, 31, 0, 0, time.UTC) },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mirror)}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var mirrorAfter mirrorv1alpha1.GitMirror
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(mirror), &mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if mirrorAfter.Status.LastError != "" {
		t.Fatalf("expected stale failure not to set last error, got %q", mirrorAfter.Status.LastError)
	}
	if mirrorAfter.Status.LastMirroredRevision != "new-revision" {
		t.Fatalf("expected stale completion not to overwrite mirrored revision, got %q", mirrorAfter.Status.LastMirroredRevision)
	}
	if !mirrorAfter.Status.LastSuccessAt.Equal(&successAt) {
		t.Fatalf("expected current success timestamp preserved, got %#v", mirrorAfter.Status.LastSuccessAt)
	}
}

func TestApplySyncScheduledForGenerationSkipsMismatchedGeneration(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 6, 24, 10, 36, 0, 0, time.UTC))
	mirror := testGitMirror()
	mirror.Generation = 2
	mirror.Status.ObservedGeneration = 1
	mirror.Status.PendingResync = true

	if ApplySyncScheduledForGeneration(mirror, "gitmirror-source-repo-generation-1", 1, now) {
		t.Fatal("expected mismatched generation schedule to be skipped")
	}
	if mirror.Status.ObservedGeneration != 1 {
		t.Fatalf("expected observed generation to remain 1, got %d", mirror.Status.ObservedGeneration)
	}
	if mirror.Status.LastJobName != "" {
		t.Fatalf("expected last job to remain empty, got %q", mirror.Status.LastJobName)
	}
	if !mirror.Status.PendingResync {
		t.Fatal("expected pending resync to remain queued")
	}
}

func TestApplyCompletedJobStatusForGenerationSkipsMismatchedGeneration(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 6, 24, 10, 37, 0, 0, time.UTC))
	mirror := testGitMirror()
	mirror.Generation = 2
	mirror.Status.ObservedGeneration = 1
	mirror.Status.PendingResync = true
	job := generationJob(mirror, "gitmirror-source-repo-generation-1-complete", 1, []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}})

	if ApplyCompletedJobStatusForGeneration(mirror, job, 1, now) {
		t.Fatal("expected mismatched generation completion not to schedule follow-up")
	}
	if mirror.Status.ObservedGeneration != 1 {
		t.Fatalf("expected observed generation to remain 1, got %d", mirror.Status.ObservedGeneration)
	}
	if mirror.Status.LastCompletedJobName != "" {
		t.Fatalf("expected completed job to remain empty, got %q", mirror.Status.LastCompletedJobName)
	}
	if !mirror.Status.PendingResync {
		t.Fatal("expected pending resync to remain queued")
	}
}

func TestReconcileCurrentGenerationCompletionIgnoresStaleLastJobName(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mirror := testGitMirror()
	mirror.Generation = 2
	mirror.Status.ObservedGeneration = 2
	mirror.Status.LastJobName = "gitmirror-source-repo-generation-1-failed"
	currentCompletedJob := generationJob(mirror, "gitmirror-source-repo-generation-2-complete", 2, []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}})
	currentCompletedJob.Annotations[jobs.AnnotationRevision] = "new-revision"
	staleFailedJob := generationJob(mirror, "gitmirror-source-repo-generation-1-failed", 1, []batchv1.JobCondition{{
		Type:    batchv1.JobFailed,
		Status:  corev1.ConditionTrue,
		Reason:  "BackoffLimitExceeded",
		Message: "old generation failed",
	}})
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mirrorv1alpha1.GitMirror{}).
		WithObjects(mirror, currentCompletedJob, staleFailedJob).
		Build()
	reconciler := &GitMirrorReconciler{
		Client:           c,
		Scheme:           scheme,
		DefaultSyncImage: "example/git-mirror-sync:dev",
		Clock:            func() time.Time { return time.Date(2026, 6, 24, 10, 33, 0, 0, time.UTC) },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mirror)}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var mirrorAfter mirrorv1alpha1.GitMirror
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(mirror), &mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if mirrorAfter.Status.LastCompletedJobName != currentCompletedJob.Name {
		t.Fatalf("expected current-generation completion recorded, got %q", mirrorAfter.Status.LastCompletedJobName)
	}
	if mirrorAfter.Status.LastMirroredRevision != "new-revision" {
		t.Fatalf("expected current-generation revision recorded, got %q", mirrorAfter.Status.LastMirroredRevision)
	}
	if mirrorAfter.Status.LastError != "" {
		t.Fatalf("expected stale failure not to set last error, got %q", mirrorAfter.Status.LastError)
	}
}

func TestReconcileCurrentGenerationActiveJobBlocksDuplicate(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mirrorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mirror := testGitMirror()
	mirror.Generation = 2
	mirror.Status.ObservedGeneration = 2
	mirror.Status.LastDeliveryID = "delivery-2"
	mirror.Status.LastRequestedRevision = "def456"
	mirror.Status.PendingResync = true
	activeJob := generationJob(mirror, "gitmirror-source-repo-generation-2-active", 2, nil)
	holder := activeJob.Name
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: SyncLeaseName(mirror), Namespace: "mirrors"},
		Spec:       coordinationv1.LeaseSpec{HolderIdentity: &holder},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mirrorv1alpha1.GitMirror{}).
		WithObjects(mirror, activeJob, lease).
		Build()
	reconciler := &GitMirrorReconciler{
		Client:           c,
		Scheme:           scheme,
		DefaultSyncImage: "example/git-mirror-sync:dev",
		Clock:            func() time.Time { return time.Date(2026, 6, 24, 10, 35, 0, 0, time.UTC) },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mirror)}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var jobList batchv1.JobList
	if err := c.List(context.Background(), &jobList, client.InNamespace("mirrors")); err != nil {
		t.Fatal(err)
	}
	if len(jobList.Items) != 1 {
		t.Fatalf("expected active current-generation job to block duplicates, got %d jobs", len(jobList.Items))
	}
	var mirrorAfter mirrorv1alpha1.GitMirror
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(mirror), &mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if !mirrorAfter.Status.PendingResync {
		t.Fatal("expected pending resync to remain queued behind active current-generation job")
	}
}

func generationJob(mirror *mirrorv1alpha1.GitMirror, name string, generation int64, conditions []batchv1.JobCondition) *batchv1.Job {
	labels := jobs.LabelsForMirror(mirror)
	job := legacyJob(mirror, name, conditions)
	job.Labels = map[string]string{
		jobs.LabelGitMirror: labels[jobs.LabelGitMirror],
	}
	job.Annotations[jobs.AnnotationGeneration] = strconv.FormatInt(generation, 10)
	return job
}

func legacyJob(mirror *mirrorv1alpha1.GitMirror, name string, conditions []batchv1.JobCondition) *batchv1.Job {
	labels := jobs.LabelsForMirror(mirror)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: mirror.Namespace,
			Labels: map[string]string{
				jobs.LabelGitMirror: labels[jobs.LabelGitMirror],
			},
			Annotations: map[string]string{},
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:  "sync",
						Image: "example/git-mirror-sync:test",
					}},
				},
			},
		},
		Status: batchv1.JobStatus{Conditions: conditions},
	}
}

func testGitMirror() *mirrorv1alpha1.GitMirror {
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
			Source: mirrorv1alpha1.GitEndpointSpec{
				URL:          "git@github.com:example/source-repo.git",
				SSHSecretRef: &mirrorv1alpha1.SecretKeyRef{Name: "source-key", Key: "ssh-privatekey"},
			},
			Target: mirrorv1alpha1.GitEndpointSpec{
				URL:          "git@codeberg.org:example/source-repo.git",
				SSHSecretRef: &mirrorv1alpha1.SecretKeyRef{Name: "target-key", Key: "ssh-privatekey"},
			},
			Mirror: mirrorv1alpha1.MirrorSpec{Mode: "exact", IncludeTags: true},
		},
	}
}
