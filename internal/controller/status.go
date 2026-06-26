package controller

import (
	"fmt"
	"strings"

	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	"github.com/shamubernetes/git-mirror-operator/internal/jobs"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ApplyWebhookState(mirror *mirrorv1alpha1.GitMirror, deliveryID string, now metav1.Time, activeJobExists bool) bool {
	mirror.Status.LastWebhookAt = now.DeepCopy()
	mirror.Status.LastDeliveryID = deliveryID
	if activeJobExists {
		mirror.Status.PendingResync = true
		setCondition(mirror, "SyncPending", metav1.ConditionTrue, "ActiveJobExists", "Webhook coalesced while a sync job is active")
		return false
	}
	mirror.Status.LastTriggeredAt = now.DeepCopy()
	mirror.Status.PendingResync = false
	setCondition(mirror, "SyncPending", metav1.ConditionFalse, "JobScheduled", "Sync job scheduled")
	return true
}

func ApplyWebhookIntent(mirror *mirrorv1alpha1.GitMirror, deliveryID, revision string, now metav1.Time) {
	mirror.Status.LastWebhookAt = now.DeepCopy()
	mirror.Status.LastDeliveryID = deliveryID
	if revision != "" {
		mirror.Status.LastRequestedRevision = revision
	}
	mirror.Status.PendingResync = true
	setCondition(mirror, "SyncPending", metav1.ConditionTrue, "WebhookReceived", "Webhook recorded and waiting for the reconciler to schedule a sync job")
}

func ApplySyncScheduled(mirror *mirrorv1alpha1.GitMirror, jobName string, now metav1.Time) {
	mirror.Status.ObservedGeneration = mirror.Generation
	mirror.Status.LastTriggeredAt = now.DeepCopy()
	mirror.Status.LastJobName = jobName
	mirror.Status.PendingResync = false
	setCondition(mirror, "SyncPending", metav1.ConditionFalse, "JobScheduled", "Sync job scheduled")
}

func ApplySyncScheduledForGeneration(mirror *mirrorv1alpha1.GitMirror, jobName string, generation int64, now metav1.Time) bool {
	if mirror.Generation != generation {
		return false
	}
	ApplySyncScheduled(mirror, jobName, now)
	return true
}

func ApplyCompletedJobStatus(mirror *mirrorv1alpha1.GitMirror, job *batchv1.Job, now metav1.Time) bool {
	mirror.Status.ObservedGeneration = mirror.Generation
	return applyCompletedJobStatus(mirror, job, now, true, mirror.Generation)
}

func ApplyCompletedJobStatusForGeneration(mirror *mirrorv1alpha1.GitMirror, job *batchv1.Job, generation int64, now metav1.Time) bool {
	if mirror.Generation != generation {
		return false
	}
	return ApplyCompletedJobStatus(mirror, job, now)
}

func ApplyCompletedLegacyJobStatus(mirror *mirrorv1alpha1.GitMirror, job *batchv1.Job, now metav1.Time) bool {
	return applyCompletedJobStatus(mirror, job, now, false, mirror.Status.ObservedGeneration)
}

func applyCompletedJobStatus(mirror *mirrorv1alpha1.GitMirror, job *batchv1.Job, now metav1.Time, updatePending bool, conditionGeneration int64) bool {
	mirror.Status.LastJobName = job.Name
	mirror.Status.LastCompletedJobName = job.Name
	if isJobComplete(job) {
		mirror.Status.LastSuccessAt = now.DeepCopy()
		mirror.Status.LastError = ""
		if revision := job.Annotations[jobs.AnnotationRevision]; revision != "" {
			mirror.Status.LastMirroredRevision = revision
		}
		setConditionForGeneration(mirror, "Ready", conditionGeneration, metav1.ConditionTrue, "SyncSucceeded", "Last sync job completed successfully")
	} else if failed, reason := isJobFailed(job); failed {
		mirror.Status.LastFailureAt = now.DeepCopy()
		mirror.Status.LastError = reason
		setConditionForGeneration(mirror, "Ready", conditionGeneration, metav1.ConditionFalse, "SyncFailed", reason)
	}
	if !updatePending {
		return false
	}
	followup := mirror.Status.PendingResync
	if followup {
		mirror.Status.PendingResync = false
		mirror.Status.LastTriggeredAt = now.DeepCopy()
		setCondition(mirror, "SyncPending", metav1.ConditionFalse, "FollowupScheduled", "Follow-up sync requested for coalesced webhook")
	}
	return followup
}

func JobFinished(job *batchv1.Job) bool {
	if job == nil {
		return false
	}
	if isJobComplete(job) {
		return true
	}
	failed, _ := isJobFailed(job)
	return failed
}

func JobActive(job *batchv1.Job) bool {
	return job != nil && !JobFinished(job) && job.DeletionTimestamp == nil
}

func isJobComplete(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func isJobFailed(job *batchv1.Job) (bool, string) {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			parts := []string{}
			if condition.Reason != "" {
				parts = append(parts, condition.Reason)
			}
			if condition.Message != "" {
				parts = append(parts, condition.Message)
			}
			if len(parts) == 0 {
				return true, fmt.Sprintf("job %s failed", job.Name)
			}
			return true, strings.Join(parts, ": ")
		}
	}
	return false, ""
}

func setCondition(mirror *mirrorv1alpha1.GitMirror, conditionType string, status metav1.ConditionStatus, reason, message string) {
	setConditionForGeneration(mirror, conditionType, mirror.Generation, status, reason, message)
}

func setConditionForGeneration(mirror *mirrorv1alpha1.GitMirror, conditionType string, observedGeneration int64, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&mirror.Status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: observedGeneration,
		Reason:             reason,
		Message:            message,
	})
}
