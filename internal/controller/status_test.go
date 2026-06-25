package controller_test

import (
	"testing"
	"time"

	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	"github.com/shamubernetes/git-mirror-operator/internal/controller"
	"github.com/shamubernetes/git-mirror-operator/internal/jobs"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestApplyWebhookStateCoalescesWhenActiveJobExists(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC))
	mirror := &mirrorv1alpha1.GitMirror{}

	shouldCreate := controller.ApplyWebhookState(mirror, "delivery-1", now, true)

	if shouldCreate {
		t.Fatal("expected active sync to coalesce instead of creating a job")
	}
	if !mirror.Status.PendingResync {
		t.Fatal("expected pending resync")
	}
	if mirror.Status.LastDeliveryID != "delivery-1" {
		t.Fatalf("expected delivery recorded, got %q", mirror.Status.LastDeliveryID)
	}
}

func TestApplySuccessfulJobStatusRecordsSuccessAndFollowup(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 6, 24, 10, 5, 0, 0, time.UTC))
	mirror := &mirrorv1alpha1.GitMirror{}
	mirror.Status.PendingResync = true
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "mirror-job",
			Annotations: map[string]string{jobs.AnnotationRevision: "abc123"},
		},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type:   batchv1.JobComplete,
			Status: corev1.ConditionTrue,
		}}},
	}

	followup := controller.ApplyCompletedJobStatus(mirror, job, now)

	if !followup {
		t.Fatal("expected pending resync to request follow-up job")
	}
	if !mirror.Status.LastSuccessAt.Equal(&now) {
		t.Fatalf("expected success timestamp, got %#v", mirror.Status.LastSuccessAt)
	}
	if mirror.Status.LastError != "" {
		t.Fatalf("expected last error cleared, got %q", mirror.Status.LastError)
	}
	if mirror.Status.LastCompletedJobName != "mirror-job" {
		t.Fatalf("expected completed job recorded, got %q", mirror.Status.LastCompletedJobName)
	}
	if mirror.Status.LastMirroredRevision != "abc123" {
		t.Fatalf("expected mirrored revision recorded after success, got %q", mirror.Status.LastMirroredRevision)
	}
	if mirror.Status.PendingResync {
		t.Fatal("expected pending resync cleared")
	}
}

func TestApplyFailedJobStatusRecordsFailure(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 6, 24, 10, 5, 0, 0, time.UTC))
	mirror := &mirrorv1alpha1.GitMirror{}
	mirror.Status.LastMirroredRevision = "previous"
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "mirror-job",
			Annotations: map[string]string{jobs.AnnotationRevision: "abc123"},
		},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type:    batchv1.JobFailed,
			Status:  corev1.ConditionTrue,
			Reason:  "BackoffLimitExceeded",
			Message: "Job has reached the specified backoff limit",
		}}},
	}

	followup := controller.ApplyCompletedJobStatus(mirror, job, now)

	if followup {
		t.Fatal("did not expect follow-up job without pending resync")
	}
	if !mirror.Status.LastFailureAt.Equal(&now) {
		t.Fatalf("expected failure timestamp, got %#v", mirror.Status.LastFailureAt)
	}
	if mirror.Status.LastError == "" {
		t.Fatal("expected failure error message")
	}
	if mirror.Status.LastCompletedJobName != "mirror-job" {
		t.Fatalf("expected completed job recorded, got %q", mirror.Status.LastCompletedJobName)
	}
	if mirror.Status.LastMirroredRevision != "previous" {
		t.Fatalf("expected failed job not to advance mirrored revision, got %q", mirror.Status.LastMirroredRevision)
	}
}
