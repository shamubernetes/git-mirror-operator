package jobs_test

import (
	"strings"
	"testing"

	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	"github.com/shamubernetes/git-mirror-operator/internal/jobs"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

func baseMirror() *mirrorv1alpha1.GitMirror {
	return &mirrorv1alpha1.GitMirror{
		ObjectMeta: metav1.ObjectMeta{Name: "source-repo", Namespace: "mirrors"},
		Spec: mirrorv1alpha1.GitMirrorSpec{
			GitHub: mirrorv1alpha1.GitHubSpec{Owner: "example", Repo: "source-repo"},
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

func envVar(t *testing.T, job *jobs.SyncJob, name string) corev1.EnvVar {
	t.Helper()
	for _, env := range job.Job.Spec.Template.Spec.Containers[0].Env {
		if env.Name == name {
			return env
		}
	}
	t.Fatalf("missing env %s", name)
	return corev1.EnvVar{}
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
	if hasEnv(syncJob, "INCLUDE_TAGS") {
		t.Fatal("did not expect INCLUDE_TAGS env in exact mode; exact mode always mirrors tags")
	}
	if hasEnv(syncJob, "PRUNE") {
		t.Fatal("did not expect PRUNE env; exact mode always prunes and additive mode never prunes")
	}
	if got := syncJob.Job.Labels["mirror.shamubernetes.com/source-owner"]; got != "example" {
		t.Fatalf("expected owner label, got %q", got)
	}
	if len(syncJob.Job.Spec.Template.Spec.Volumes) < 2 {
		t.Fatalf("expected secret volumes, got %d", len(syncJob.Job.Spec.Template.Spec.Volumes))
	}
	assertSecretVolumeMode(t, syncJob, "source-ssh-key", 0444)
	assertSecretVolumeMode(t, syncJob, "target-ssh-key", 0444)
	if got := envValue(t, syncJob.Job.Name, envs, "SOURCE_AUTH_TYPE"); got != "ssh" {
		t.Fatalf("expected source SSH auth, got %q", got)
	}
	if got := envValue(t, syncJob.Job.Name, envs, "TARGET_AUTH_TYPE"); got != "ssh" {
		t.Fatalf("expected target SSH auth, got %q", got)
	}
	securityContext := syncJob.Job.Spec.Template.Spec.SecurityContext
	if securityContext == nil || securityContext.SeccompProfile == nil ||
		securityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("expected pod seccomp profile RuntimeDefault, got %#v", securityContext)
	}
}

func TestBuildSyncJobSanitizesLongLabelValues(t *testing.T) {
	mirror := baseMirror()
	mirror.Name = "source-repo-" + strings.Repeat("very-long-name-", 8)
	mirror.Spec.GitHub.Owner = "owner-" + strings.Repeat("long-", 20)
	mirror.Spec.GitHub.Repo = "repo-" + strings.Repeat("long-", 20)

	syncJob, err := jobs.BuildSyncJob(mirror, jobs.Options{DefaultImage: "example/git-mirror-sync:dev", TriggerID: "delivery-" + strings.Repeat("long-", 20)})
	if err != nil {
		t.Fatalf("expected job: %v", err)
	}

	for _, key := range []string{jobs.LabelGitMirror, jobs.LabelSourceOwner, jobs.LabelSourceRepo, jobs.LabelDeliveryID} {
		value := syncJob.Job.Labels[key]
		assertValidLabelValue(t, key, value)
		if strings.Contains(value, strings.Repeat("long-", 10)) {
			t.Fatalf("expected %s to be hash-truncated, got %q", key, value)
		}
	}
	if got := syncJob.Job.Annotations[jobs.AnnotationGitMirror]; got != mirror.Name {
		t.Fatalf("expected full mirror name annotation, got %q", got)
	}
	if got := syncJob.Job.Annotations[jobs.AnnotationOwner]; got != mirror.Spec.GitHub.Owner {
		t.Fatalf("expected full owner annotation, got %q", got)
	}
	if got := syncJob.Job.Annotations[jobs.AnnotationRepo]; got != mirror.Spec.GitHub.Repo {
		t.Fatalf("expected full repo annotation, got %q", got)
	}
}

func TestLabelsForMirrorPreservesDistinctValidLabelValues(t *testing.T) {
	withDot := baseMirror()
	withDot.Name = "source.repo"
	withHyphen := baseMirror()
	withHyphen.Name = "source-repo"

	dotLabel := jobs.LabelsForMirror(withDot)[jobs.LabelGitMirror]
	hyphenLabel := jobs.LabelsForMirror(withHyphen)[jobs.LabelGitMirror]

	assertValidLabelValue(t, jobs.LabelGitMirror, dotLabel)
	assertValidLabelValue(t, jobs.LabelGitMirror, hyphenLabel)
	if dotLabel != "source.repo" {
		t.Fatalf("expected valid label value to be preserved, got %q", dotLabel)
	}
	if hyphenLabel != "source-repo" {
		t.Fatalf("expected valid label value to be preserved, got %q", hyphenLabel)
	}
	if dotLabel == hyphenLabel {
		t.Fatalf("expected distinct mirror names to produce distinct labels, got %q", dotLabel)
	}
}

func TestLabelsForMirrorHashSuffixesLongLabelValues(t *testing.T) {
	first := baseMirror()
	first.Name = strings.Repeat("a", 63) + "x"
	second := baseMirror()
	second.Name = strings.Repeat("a", 63) + "y"

	firstLabel := jobs.LabelsForMirror(first)[jobs.LabelGitMirror]
	secondLabel := jobs.LabelsForMirror(second)[jobs.LabelGitMirror]

	assertValidLabelValue(t, jobs.LabelGitMirror, firstLabel)
	assertValidLabelValue(t, jobs.LabelGitMirror, secondLabel)
	if firstLabel == secondLabel {
		t.Fatalf("expected long mirror names with shared prefixes to produce distinct labels, got %q", firstLabel)
	}
	if len(firstLabel) > 63 || len(secondLabel) > 63 {
		t.Fatalf("expected labels <= 63 chars, got %d and %d", len(firstLabel), len(secondLabel))
	}
}

func TestBuildSyncJobForBasicAuth(t *testing.T) {
	mirror := baseMirror()
	mirror.Spec.Source.URL = "https://github.com/example/source-repo.git"
	mirror.Spec.Source.SSHSecretRef = nil
	mirror.Spec.Source.Auth = &mirrorv1alpha1.GitAuthSpec{
		Type: "basic",
		Basic: &mirrorv1alpha1.BasicAuthSpec{
			UsernameSecretRef: mirrorv1alpha1.SecretKeyRef{Name: "source-basic", Key: "username"},
			PasswordSecretRef: mirrorv1alpha1.SecretKeyRef{Name: "source-basic", Key: "password"},
		},
	}

	syncJob, err := jobs.BuildSyncJob(mirror, jobs.Options{DefaultImage: "example/git-mirror-sync:dev"})
	if err != nil {
		t.Fatalf("expected job: %v", err)
	}

	if got := envVar(t, syncJob, "SOURCE_AUTH_TYPE").Value; got != "basic" {
		t.Fatalf("expected source basic auth, got %q", got)
	}
	assertSecretEnvVar(t, syncJob, "SOURCE_AUTH_USERNAME", "source-basic", "username")
	assertSecretEnvVar(t, syncJob, "SOURCE_AUTH_PASSWORD", "source-basic", "password")
	if hasVolume(syncJob, "source-ssh-key") {
		t.Fatal("did not expect a source SSH volume for basic auth")
	}
}

func TestBuildSyncJobForGitHubAppAuth(t *testing.T) {
	mirror := baseMirror()
	mirror.Spec.Source.URL = "https://github.com/example/source-repo.git"
	mirror.Spec.Source.SSHSecretRef = nil
	mirror.Spec.Source.Auth = &mirrorv1alpha1.GitAuthSpec{
		Type: "githubApp",
		GitHubApp: &mirrorv1alpha1.GitHubAppAuthSpec{
			AppIDSecretRef:          mirrorv1alpha1.SecretKeyRef{Name: "github-app", Key: "app-id"},
			InstallationIDSecretRef: mirrorv1alpha1.SecretKeyRef{Name: "github-app", Key: "installation-id"},
			PrivateKeySecretRef:     mirrorv1alpha1.SecretKeyRef{Name: "github-app", Key: "private-key.pem"},
			APIURL:                  "https://github.example.com/api/v3",
		},
	}

	syncJob, err := jobs.BuildSyncJob(mirror, jobs.Options{DefaultImage: "example/git-mirror-sync:dev"})
	if err != nil {
		t.Fatalf("expected job: %v", err)
	}

	if got := envVar(t, syncJob, "SOURCE_AUTH_TYPE").Value; got != "githubApp" {
		t.Fatalf("expected source GitHub App auth, got %q", got)
	}
	assertSecretEnvVar(t, syncJob, "SOURCE_GITHUB_APP_ID", "github-app", "app-id")
	assertSecretEnvVar(t, syncJob, "SOURCE_GITHUB_APP_INSTALLATION_ID", "github-app", "installation-id")
	if got := envVar(t, syncJob, "SOURCE_GITHUB_APP_API_URL").Value; got != "https://github.example.com/api/v3" {
		t.Fatalf("expected GitHub Enterprise API URL, got %q", got)
	}
	assertSecretVolumeMode(t, syncJob, "source-github-app-private-key", 0444)
}

func assertSecretEnvVar(t *testing.T, job *jobs.SyncJob, name, secretName, key string) {
	t.Helper()
	env := envVar(t, job, name)
	if env.Value != "" {
		t.Fatalf("expected %s to use valueFrom, got literal value", name)
	}
	if env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("expected %s to use secretKeyRef, got %#v", name, env.ValueFrom)
	}
	if env.ValueFrom.SecretKeyRef.Name != secretName || env.ValueFrom.SecretKeyRef.Key != key {
		t.Fatalf("expected %s secret %s/%s, got %s/%s", name, secretName, key, env.ValueFrom.SecretKeyRef.Name, env.ValueFrom.SecretKeyRef.Key)
	}
}

func hasVolume(job *jobs.SyncJob, name string) bool {
	for _, volume := range job.Job.Spec.Template.Spec.Volumes {
		if volume.Name == name {
			return true
		}
	}
	return false
}

func assertValidLabelValue(t *testing.T, key, value string) {
	t.Helper()
	if len(value) > 63 {
		t.Fatalf("expected %s label value <= 63 chars, got %d: %q", key, len(value), value)
	}
	if errs := validation.IsValidLabelValue(value); len(errs) > 0 {
		t.Fatalf("invalid %s label value %q: %v", key, value, errs)
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
