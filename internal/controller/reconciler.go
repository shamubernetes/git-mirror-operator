package controller

import (
	"context"
	"time"

	"github.com/robfig/cron/v3"
	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	"github.com/shamubernetes/git-mirror-operator/internal/jobs"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type GitMirrorReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	DefaultSyncImage string
	Clock            func() time.Time
}

func (r *GitMirrorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	var mirror mirrorv1alpha1.GitMirror
	if err := r.Get(ctx, req.NamespacedName, &mirror); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var jobList batchv1.JobList
	if err := r.List(ctx, &jobList, client.InNamespace(mirror.Namespace), client.MatchingLabels{jobs.LabelGitMirror: mirror.Name}); err != nil {
		return ctrl.Result{}, err
	}
	now := metav1.NewTime(r.now())
	for i := range jobList.Items {
		job := &jobList.Items[i]
		if !JobFinished(job) || mirror.Status.LastJobName == job.Name {
			continue
		}
		followup := ApplyCompletedJobStatus(&mirror, job, now)
		if err := updateMirrorStatus(ctx, r.Client, &mirror); err != nil {
			return ctrl.Result{}, err
		}
		if followup {
			if err := r.createSyncJob(ctx, &mirror); err != nil && !apierrors.IsAlreadyExists(err) {
				return ctrl.Result{}, err
			}
		}
	}

	if mirror.Spec.Fallback.Schedule != "" {
		next, err := NextFallbackTime(mirror.Spec.Fallback.Schedule, mirror.Status.LastTriggeredAt, r.now())
		if err != nil {
			logger.Error(err, "invalid fallback schedule", "schedule", mirror.Spec.Fallback.Schedule)
			return ctrl.Result{}, nil
		}
		if !next.After(r.now()) {
			active := false
			for i := range jobList.Items {
				if JobActive(&jobList.Items[i]) {
					active = true
					break
				}
			}
			create := ApplyWebhookState(&mirror, "scheduled", now, active)
			if err := updateMirrorStatus(ctx, r.Client, &mirror); err != nil {
				return ctrl.Result{}, err
			}
			if create {
				if err := r.createSyncJob(ctx, &mirror); err != nil {
					return ctrl.Result{}, err
				}
			}
			next, _ = NextFallbackTime(mirror.Spec.Fallback.Schedule, mirror.Status.LastTriggeredAt, r.now())
		}
		return ctrl.Result{RequeueAfter: time.Until(next)}, nil
	}

	return ctrl.Result{}, nil
}

func (r *GitMirrorReconciler) createSyncJob(ctx context.Context, mirror *mirrorv1alpha1.GitMirror) error {
	syncJob, err := jobs.BuildSyncJob(mirror, jobs.Options{DefaultImage: r.DefaultSyncImage, Scheme: r.Scheme})
	if err != nil {
		return err
	}
	mirror.Status.LastJobName = syncJob.Job.GenerateName
	return r.Create(ctx, syncJob.Job)
}

func (r *GitMirrorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mirrorv1alpha1.GitMirror{}).
		Owns(&batchv1.Job{}).
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

func updateMirrorStatus(ctx context.Context, c client.Client, mirror *mirrorv1alpha1.GitMirror) error {
	if err := c.Status().Update(ctx, mirror); err != nil {
		return c.Update(ctx, mirror)
	}
	return nil
}
