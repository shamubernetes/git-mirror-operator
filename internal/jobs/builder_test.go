package jobs_test

import (
	"testing"

	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	"github.com/shamubernetes/git-mirror-operator/internal/jobs"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func baseMirror() *mirrorv1alpha1.GitMirror {
	return &mirrorv1alpha1.GitMirror{
		ObjectMeta: metav1.ObjectMeta{Name: "source-repo", Namespace: "mirrors"},
		Spec: mirrorv1alpha1.GitMirrorSpec{
			GitHub: mirrorv1alpha1.GitHubSpec{Owner: "example", Repo: "source-repo"},
			Source: mirrorv1alpha1.GitEndpointSpec{
				URL:          "git@github.com:example/source-repo.git",
				SSHSecretRef: mirrorv1alpha1.SecretKeyRef{Name: "source-key", Key: "ssh-privatekey"},
			},
			Target: mirrorv1alpha1.GitEndpointSpec{
				URL:          "git@codeberg.org:example/source-repo.git",
				SSHSecretRef: mirrorv1alpha1.SecretKeyRef{Name: "target-key", Key: "ssh-privatekey"},
			},
			Mirror: mirrorv1alpha1.MirrorSpec{Mode: "exact", IncludeTags: true},
		},
	}
}

func envValue(t *testing.T, jobName string, envs []string, name string) string {
	t.Helper()
	for i := 0; i < len(envs)-1; i += 2 {
		if envs[i] == name {
			return envs[i+1]
		}
	}
	t.Fatalf("%s missing env %s in %v", jobName, name, envs)
	return ""
}

func hasEnv(job *jobs.SyncJob, name string) bool {
	for _, env := range job.Job.Spec.Template.Spec.Containers[0].Env {
		if env.Name == name {
			return true
		}
	}
	return false
}

func flattenedEnv(job *jobs.SyncJob) []string {
	var envs []string
	for _, env := range job.Job.Spec.Template.Spec.Containers[0].Env {
		envs = append(envs, env.Name, env.Value)
	}
	return envs
}

func TestBuildSyncJobForExactMode(t *testing.T) {
	mirror := baseMirror()

	syncJob, err := jobs.BuildSyncJob(mirror, jobs.Options{DefaultImage: "example/git-mirror-sync:dev"})
	if err != nil {
		t.Fatalf("expected job: %v", err)
	}

	envs := flattenedEnv(syncJob)
	if got := envValue(t, syncJob.Job.Name, envs, "MIRROR_MODE"); got != "exact" {
		t.Fatalf("expected exact mode, got %q", got)
	}
	if got := envValue(t, syncJob.Job.Name, envs, "INCLUDE_TAGS"); got != "true" {
		t.Fatalf("expected include tags true, got %q", got)
	}
	if hasEnv(syncJob, "PRUNE") {
		t.Fatal("did not expect PRUNE env; exact mode always prunes and additive mode never prunes")
	}
	if got := syncJob.Job.Labels["mirror.maude.dev/source-owner"]; got != "example" {
		t.Fatalf("expected owner label, got %q", got)
	}
	if len(syncJob.Job.Spec.Template.Spec.Volumes) < 2 {
		t.Fatalf("expected secret volumes, got %d", len(syncJob.Job.Spec.Template.Spec.Volumes))
	}
	assertSecretVolumeMode(t, syncJob, "source-ssh-key", 0444)
	assertSecretVolumeMode(t, syncJob, "target-ssh-key", 0444)
	securityContext := syncJob.Job.Spec.Template.Spec.SecurityContext
	if securityContext == nil || securityContext.SeccompProfile == nil ||
		securityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("expected pod seccomp profile RuntimeDefault, got %#v", securityContext)
	}
}

func assertSecretVolumeMode(t *testing.T, job *jobs.SyncJob, name string, want int32) {
	t.Helper()
	for _, volume := range job.Job.Spec.Template.Spec.Volumes {
		if volume.Name != name {
			continue
		}
		if volume.Secret == nil {
			t.Fatalf("expected %s to be a Secret volume", name)
		}
		if volume.Secret.DefaultMode == nil || *volume.Secret.DefaultMode != want {
			t.Fatalf("expected %s default mode %04o, got %#v", name, want, volume.Secret.DefaultMode)
		}
		if len(volume.Secret.Items) != 1 || volume.Secret.Items[0].Mode == nil || *volume.Secret.Items[0].Mode != want {
			t.Fatalf("expected %s item mode %04o, got %#v", name, want, volume.Secret.Items)
		}
		return
	}
	t.Fatalf("missing volume %s", name)
}

func TestBuildSyncJobUsesStableNameForDelivery(t *testing.T) {
	mirror := baseMirror()

	first, err := jobs.BuildSyncJob(mirror, jobs.Options{DefaultImage: "example/git-mirror-sync:dev", TriggerID: "delivery-1"})
	if err != nil {
		t.Fatalf("expected first job: %v", err)
	}
	second, err := jobs.BuildSyncJob(mirror, jobs.Options{DefaultImage: "example/git-mirror-sync:dev", TriggerID: "delivery-1"})
	if err != nil {
		t.Fatalf("expected second job: %v", err)
	}

	if first.Job.Name == "" {
		t.Fatal("expected stable job name")
	}
	if first.Job.GenerateName != "" {
		t.Fatalf("expected no generateName for stable delivery, got %q", first.Job.GenerateName)
	}
	if first.Job.Name != second.Job.Name {
		t.Fatalf("expected same delivery to produce same job name, got %q and %q", first.Job.Name, second.Job.Name)
	}
	if len(first.Job.Name) > 63 {
		t.Fatalf("job name exceeds DNS label limit: %q", first.Job.Name)
	}
}

func TestBuildSyncJobAnnotatesRequestedRevision(t *testing.T) {
	mirror := baseMirror()

	syncJob, err := jobs.BuildSyncJob(mirror, jobs.Options{DefaultImage: "example/git-mirror-sync:dev", Revision: "abc123"})
	if err != nil {
		t.Fatalf("expected job: %v", err)
	}

	if got := syncJob.Job.Annotations[jobs.AnnotationRevision]; got != "abc123" {
		t.Fatalf("expected revision annotation, got %q", got)
	}
}

func TestBuildSyncJobForAdditiveModeWithTags(t *testing.T) {
	mirror := baseMirror()
	mirror.Spec.Mirror.Mode = "additive"
	mirror.Spec.Mirror.IncludeTags = true

	syncJob, err := jobs.BuildSyncJob(mirror, jobs.Options{DefaultImage: "example/git-mirror-sync:dev"})
	if err != nil {
		t.Fatalf("expected job: %v", err)
	}

	envs := flattenedEnv(syncJob)
	if got := envValue(t, syncJob.Job.Name, envs, "MIRROR_MODE"); got != "additive" {
		t.Fatalf("expected additive mode, got %q", got)
	}
	if got := envValue(t, syncJob.Job.Name, envs, "INCLUDE_TAGS"); got != "true" {
		t.Fatalf("expected include tags true, got %q", got)
	}
}

func TestBuildSyncJobForAdditiveModeWithoutTags(t *testing.T) {
	mirror := baseMirror()
	mirror.Spec.Mirror.Mode = "additive"
	mirror.Spec.Mirror.IncludeTags = false

	syncJob, err := jobs.BuildSyncJob(mirror, jobs.Options{DefaultImage: "example/git-mirror-sync:dev"})
	if err != nil {
		t.Fatalf("expected job: %v", err)
	}

	envs := flattenedEnv(syncJob)
	if got := envValue(t, syncJob.Job.Name, envs, "INCLUDE_TAGS"); got != "false" {
		t.Fatalf("expected include tags false, got %q", got)
	}
}
