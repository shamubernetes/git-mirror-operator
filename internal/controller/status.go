package controller

import (
	"fmt"
	"strings"

	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ApplyWebhookState(mirror *mirrorv1alpha1.GitMirror, deliveryID string, now metav1.Time, activeJobExists bool) bool {
	mirror.Status.ObservedGeneration = mirror.Generation
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

func ApplyCompletedJobStatus(mirror *mirrorv1alpha1.GitMirror, job *batchv1.Job, now metav1.Time) bool {
	mirror.Status.LastJobName = job.Name
	if isJobComplete(job) {
		mirror.Status.LastSuccessAt = now.DeepCopy()
		mirror.Status.LastError = ""
		setCondition(mirror, "Ready", metav1.ConditionTrue, "SyncSucceeded", "Last sync job completed successfully")
	} else if failed, reason := isJobFailed(job); failed {
		mirror.Status.LastFailureAt = now.DeepCopy()
		mirror.Status.LastError = reason
		setCondition(mirror, "Ready", metav1.ConditionFalse, "SyncFailed", reason)
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
	meta.SetStatusCondition(&mirror.Status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: mirror.Generation,
		Reason:             reason,
		Message:            message,
	})
}
