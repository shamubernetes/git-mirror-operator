package jobs_test

import (
	"testing"

	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	"github.com/shamubernetes/git-mirror-operator/internal/jobs"
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
			Mirror: mirrorv1alpha1.MirrorSpec{Mode: "exact", IncludeTags: true, Prune: true},
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
	if got := syncJob.Job.Labels["mirror.maude.dev/source-owner"]; got != "example" {
		t.Fatalf("expected owner label, got %q", got)
	}
	if len(syncJob.Job.Spec.Template.Spec.Volumes) < 2 {
		t.Fatalf("expected secret volumes, got %d", len(syncJob.Job.Spec.Template.Spec.Volumes))
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
