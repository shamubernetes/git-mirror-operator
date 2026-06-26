/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	"github.com/shamubernetes/git-mirror-operator/internal/jobs"
	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// GitMirrorReconciler reconciles a GitMirror object.
type GitMirrorReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	DefaultSyncImage string
	Clock            func() time.Time
}

// +kubebuilder:rbac:groups=mirror.shamubernetes.com,resources=gitmirrors,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=mirror.shamubernetes.com,resources=gitmirrors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=create;get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=create;get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *GitMirrorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var mirror mirrorv1alpha1.GitMirror
	if err := r.Get(ctx, req.NamespacedName, &mirror); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var jobList batchv1.JobList
	labels := jobs.LabelsForMirror(&mirror)
	if err := r.List(ctx, &jobList, client.InNamespace(mirror.Namespace), client.MatchingLabels{jobs.LabelGitMirror: labels[jobs.LabelGitMirror]}); err != nil {
		return ctrl.Result{}, err
	}
	now := metav1.NewTime(r.now())
	currentGenerationJobs := jobsForGeneration(jobList.Items, mirror.Generation)

	if result, handled, err := r.reconcileCompletedJobs(ctx, req, &mirror, currentGenerationJobs, now); err != nil || handled {
		return result, err
	}

	active := activeJobExists(currentGenerationJobs)
	if result, handled, err := r.reconcileUnobservedGeneration(ctx, req, &mirror, active, now); err != nil || handled {
		return result, err
	}

	if result, handled, err := r.reconcilePendingResync(ctx, req, &mirror, active, now); err != nil || handled {
		return result, err
	}

	return r.reconcileFallback(ctx, req, &mirror, active, now)
}

func (r *GitMirrorReconciler) reconcileUnobservedGeneration(ctx context.Context, req ctrl.Request, mirror *mirrorv1alpha1.GitMirror, active bool, now metav1.Time) (ctrl.Result, bool, error) {
	if mirror.Status.ObservedGeneration == mirror.Generation {
		return ctrl.Result{}, false, nil
	}
	if active {
		return ctrl.Result{}, false, nil
	}

	var current mirrorv1alpha1.GitMirror
	if err := r.Get(ctx, req.NamespacedName, &current); err != nil {
		return ctrl.Result{}, false, err
	}
	if current.Status.ObservedGeneration == current.Generation {
		return ctrl.Result{}, false, nil
	}

	scheduledGeneration := current.Generation
	triggerID := fmt.Sprintf("generation-%d", scheduledGeneration)
	acquired, jobName, err := r.createLockedSyncJob(ctx, &current, triggerID, current.Status.LastRequestedRevision)
	if err != nil {
		return ctrl.Result{}, false, err
	}
	if !acquired {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, true, nil
	}

	if err := PatchGitMirrorStatus(ctx, r.Client, req.NamespacedName, func(current *mirrorv1alpha1.GitMirror) {
		if current.Generation == scheduledGeneration {
			ApplySyncScheduled(current, jobName, now)
		}
	}); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{}, true, nil
}

func (r *GitMirrorReconciler) reconcileCompletedJobs(ctx context.Context, req ctrl.Request, mirror *mirrorv1alpha1.GitMirror, jobItems []batchv1.Job, now metav1.Time) (ctrl.Result, bool, error) {
	for i := range jobItems {
		job := &jobItems[i]
		if !JobFinished(job) || mirror.Status.LastCompletedJobName == job.Name {
			continue
		}
		if mirror.Status.LastJobName != "" && mirror.Status.LastJobName != job.Name && jobNameExists(jobItems, mirror.Status.LastJobName) {
			continue
		}
		var followup bool
		var followupRevision string
		if err := PatchGitMirrorStatus(ctx, r.Client, req.NamespacedName, func(current *mirrorv1alpha1.GitMirror) {
			if current.Status.LastCompletedJobName == job.Name {
				return
			}
			if generation, annotated, valid := jobGenerationAnnotation(job); annotated {
				if !valid {
					return
				}
				followup = ApplyCompletedJobStatusForGeneration(current, job, generation, now)
			} else {
				followup = ApplyCompletedLegacyJobStatus(current, job, now)
			}
			followupRevision = current.Status.LastRequestedRevision
		}); err != nil {
			return ctrl.Result{}, false, err
		}
		if err := r.releaseSyncLease(ctx, mirror, job.Name); err != nil {
			return ctrl.Result{}, false, err
		}
		if followup {
			acquired, jobName, err := r.createLockedSyncJob(ctx, mirror, "resync-"+now.Format(time.RFC3339Nano), followupRevision)
			if err != nil && !apierrors.IsAlreadyExists(err) {
				return ctrl.Result{}, false, err
			}
			if !acquired {
				return ctrl.Result{RequeueAfter: 5 * time.Second}, true, nil
			}
			if err := PatchGitMirrorStatus(ctx, r.Client, req.NamespacedName, func(current *mirrorv1alpha1.GitMirror) {
				ApplySyncScheduledForGeneration(current, jobName, mirror.Generation, now)
			}); err != nil {
				return ctrl.Result{}, false, err
			}
		}
	}
	return ctrl.Result{}, false, nil
}

func (r *GitMirrorReconciler) reconcilePendingResync(ctx context.Context, req ctrl.Request, mirror *mirrorv1alpha1.GitMirror, active bool, now metav1.Time) (ctrl.Result, bool, error) {
	if !mirror.Status.PendingResync || active {
		return ctrl.Result{}, false, nil
	}

	var current mirrorv1alpha1.GitMirror
	if err := r.Get(ctx, req.NamespacedName, &current); err != nil {
		return ctrl.Result{}, false, err
	}
	if !current.Status.PendingResync {
		return ctrl.Result{}, false, nil
	}
	scheduledGeneration := current.Generation
	triggerID := current.Status.LastDeliveryID
	if triggerID == "" {
		triggerID = "resync-" + now.Format(time.RFC3339Nano)
	}
	acquired, jobName, err := r.createLockedSyncJob(ctx, &current, triggerID, current.Status.LastRequestedRevision)
	if err != nil {
		return ctrl.Result{}, false, err
	}
	if !acquired {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, true, nil
	}
	if err := PatchGitMirrorStatus(ctx, r.Client, req.NamespacedName, func(current *mirrorv1alpha1.GitMirror) {
		ApplySyncScheduledForGeneration(current, jobName, scheduledGeneration, now)
	}); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{}, false, nil
}

func (r *GitMirrorReconciler) reconcileFallback(ctx context.Context, req ctrl.Request, mirror *mirrorv1alpha1.GitMirror, active bool, now metav1.Time) (ctrl.Result, error) {
	if mirror.Spec.Fallback.Schedule == "" {
		return ctrl.Result{}, nil
	}
	next, err := NextFallbackTime(mirror.Spec.Fallback.Schedule, mirror.Status.LastTriggeredAt, r.now())
	if err != nil {
		log.FromContext(ctx).Error(err, "invalid fallback schedule", "schedule", mirror.Spec.Fallback.Schedule)
		return ctrl.Result{}, nil
	}
	if next.After(r.now()) {
		return ctrl.Result{RequeueAfter: time.Until(next)}, nil
	}

	var scheduledJobName string
	if !active {
		acquired, jobName, err := r.createLockedSyncJob(ctx, mirror, "scheduled-"+now.Format(time.RFC3339Nano), "")
		if err != nil {
			return ctrl.Result{}, err
		}
		if !acquired {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		scheduledJobName = jobName
	}

	if err := PatchGitMirrorStatus(ctx, r.Client, req.NamespacedName, func(current *mirrorv1alpha1.GitMirror) {
		create := ApplyWebhookState(current, "scheduled", now, active)
		if create && scheduledJobName != "" {
			ApplySyncScheduledForGeneration(current, scheduledJobName, mirror.Generation, now)
		}
	}); err != nil {
		return ctrl.Result{}, err
	}
	if scheduledJobName != "" {
		next, _ = NextFallbackTime(mirror.Spec.Fallback.Schedule, now.DeepCopy(), r.now())
	} else {
		next, _ = NextFallbackTime(mirror.Spec.Fallback.Schedule, mirror.Status.LastTriggeredAt, r.now())
	}
	return ctrl.Result{RequeueAfter: time.Until(next)}, nil
}

func (r *GitMirrorReconciler) createLockedSyncJob(ctx context.Context, mirror *mirrorv1alpha1.GitMirror, triggerID, revision string) (bool, string, error) {
	syncJob, err := jobs.BuildSyncJob(mirror, jobs.Options{
		DefaultImage: r.DefaultSyncImage,
		Scheme:       r.Scheme,
		TriggerID:    triggerID,
		Revision:     revision,
	})
	if err != nil {
		return false, "", err
	}
	jobName := syncJob.Job.Name
	if jobName == "" {
		return false, "", fmt.Errorf("sync job for %s/%s must have a deterministic name", mirror.Namespace, mirror.Name)
	}
	acquired, err := r.acquireSyncLease(ctx, mirror, jobName)
	if err != nil || !acquired {
		return acquired, jobName, err
	}
	if err := r.Create(ctx, syncJob.Job); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			_ = r.releaseSyncLease(ctx, mirror, jobName)
			return false, "", err
		}
	}
	return true, jobName, nil
}

func (r *GitMirrorReconciler) acquireSyncLease(ctx context.Context, mirror *mirrorv1alpha1.GitMirror, holder string) (bool, error) {
	now := metav1.MicroTime{Time: r.now()}
	labels := jobs.LabelsForMirror(mirror)
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SyncLeaseName(mirror),
			Namespace: mirror.Namespace,
			Labels: map[string]string{
				jobs.LabelName:      labels[jobs.LabelName],
				jobs.LabelGitMirror: labels[jobs.LabelGitMirror],
			},
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity: &holder,
			AcquireTime:    &now,
			RenewTime:      &now,
		},
	}
	if err := r.Create(ctx, lease); err == nil {
		return true, nil
	} else if !apierrors.IsAlreadyExists(err) {
		return false, err
	}

	var existing coordinationv1.Lease
	key := client.ObjectKey{Namespace: mirror.Namespace, Name: SyncLeaseName(mirror)}
	if err := r.Get(ctx, key, &existing); err != nil {
		return false, err
	}
	currentHolder := ""
	if existing.Spec.HolderIdentity != nil {
		currentHolder = *existing.Spec.HolderIdentity
	}
	if currentHolder != "" && currentHolder != holder {
		active, err := r.jobNameActiveForGeneration(ctx, mirror.Namespace, currentHolder, mirror.Generation)
		if err != nil {
			return false, err
		}
		if active {
			return false, nil
		}
	}
	existing.Spec.HolderIdentity = &holder
	if existing.Spec.AcquireTime == nil || currentHolder != holder {
		existing.Spec.AcquireTime = &now
	}
	existing.Spec.RenewTime = &now
	if err := r.Update(ctx, &existing); err != nil {
		if apierrors.IsConflict(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *GitMirrorReconciler) releaseSyncLease(ctx context.Context, mirror *mirrorv1alpha1.GitMirror, holder string) error {
	var lease coordinationv1.Lease
	key := client.ObjectKey{Namespace: mirror.Namespace, Name: SyncLeaseName(mirror)}
	if err := r.Get(ctx, key, &lease); err != nil {
		return client.IgnoreNotFound(err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != holder {
		return nil
	}
	if err := r.Delete(ctx, &lease); err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}

func (r *GitMirrorReconciler) jobNameActiveForGeneration(ctx context.Context, namespace, name string, generation int64) (bool, error) {
	var job batchv1.Job
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &job); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return JobActive(&job) && jobGenerationMatches(&job, generation), nil
}

func activeJobExists(items []batchv1.Job) bool {
	for i := range items {
		if JobActive(&items[i]) {
			return true
		}
	}
	return false
}

func jobsForGeneration(items []batchv1.Job, generation int64) []batchv1.Job {
	current := make([]batchv1.Job, 0, len(items))
	for i := range items {
		if jobGenerationMatches(&items[i], generation) {
			current = append(current, items[i])
		}
	}
	return current
}

func jobNameExists(items []batchv1.Job, name string) bool {
	for i := range items {
		if items[i].Name == name {
			return true
		}
	}
	return false
}

func jobGenerationMatches(job *batchv1.Job, generation int64) bool {
	if job == nil {
		return false
	}
	parsed, annotated, valid := jobGenerationAnnotation(job)
	if !annotated {
		return true
	}
	return valid && parsed == generation
}

func jobGenerationAnnotation(job *batchv1.Job) (int64, bool, bool) {
	if job == nil {
		return 0, false, false
	}
	value := job.Annotations[jobs.AnnotationGeneration]
	if value == "" {
		return 0, false, false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return parsed, true, err == nil
}

func SyncLeaseName(mirror *mirrorv1alpha1.GitMirror) string {
	base := dnsLabel("gitmirror-" + mirror.Spec.GitHub.Owner + "-" + mirror.Spec.GitHub.Repo)
	sum := sha256.Sum256([]byte(mirror.Namespace + "/" + mirror.Spec.Provider + "/" + mirror.Spec.GitHub.Owner + "/" + mirror.Spec.GitHub.Repo))
	hash := hex.EncodeToString(sum[:])[:12]
	maxBase := 63 - len(hash) - 1
	if len(base) > maxBase {
		base = strings.TrimRight(base[:maxBase], "-")
	}
	if base == "" {
		base = "gitmirror"
	}
	return base + "-" + hash
}

func dnsLabel(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(strings.TrimLeft(b.String(), "-"), "-")
}

// SetupWithManager sets up the controller with the Manager.
func (r *GitMirrorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mirrorv1alpha1.GitMirror{}).
		Owns(&batchv1.Job{}).
		Named("gitmirror").
		Complete(r)
}

func (r *GitMirrorReconciler) now() time.Time {
	if r.Clock != nil {
		return r.Clock()
	}
	return time.Now().UTC()
}

func NextFallbackTime(spec string, lastTriggered *metav1.Time, now time.Time) (time.Time, error) {
	schedule, err := cron.ParseStandard(spec)
	if err != nil {
		return time.Time{}, err
	}
	if lastTriggered == nil {
		return now, nil
	}
	return schedule.Next(lastTriggered.Time), nil
}
