package jobs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	AppName             = "git-mirror-operator"
	LabelName           = "app.kubernetes.io/name"
	LabelGitMirror      = "mirror.maude.dev/gitmirror"
	LabelDeliveryID     = "mirror.maude.dev/delivery-id"
	LabelSourceOwner    = "mirror.maude.dev/source-owner"
	LabelSourceRepo     = "mirror.maude.dev/source-repo"
	AnnotationRevision  = "mirror.maude.dev/revision"
	AnnotationGitMirror = "mirror.maude.dev/gitmirror-name"
	AnnotationOwner     = "mirror.maude.dev/full-source-owner"
	AnnotationRepo      = "mirror.maude.dev/full-source-repo"
	DefaultKnownHostsCM = "git-mirror-known-hosts"
)

const (
	authTypeSSH       = "ssh"
	authTypeBasic     = "basic"
	authTypeGitHubApp = "githubApp"
)

type Options struct {
	DefaultImage string
	Scheme       *runtime.Scheme
	TriggerID    string
	Revision     string
}

type SyncJob struct {
	Job *batchv1.Job
}

func BuildSyncJob(mirror *mirrorv1alpha1.GitMirror, opts Options) (*SyncJob, error) {
	if mirror == nil {
		return nil, fmt.Errorf("mirror is required")
	}
	image := mirror.Spec.Job.Image
	if image == "" {
		image = opts.DefaultImage
	}
	if image == "" {
		return nil, fmt.Errorf("sync job image is required")
	}
	mode := mirror.Spec.Mirror.Mode
	if mode == "" {
		mode = "exact"
	}
	if mode != "exact" && mode != "additive" {
		return nil, fmt.Errorf("unsupported mirror mode %q", mode)
	}
	backoffLimit := int32(1)
	if mirror.Spec.Job.BackoffLimit != nil {
		backoffLimit = *mirror.Spec.Job.BackoffLimit
	}
	ttl := int32(3600)
	if mirror.Spec.Job.TTLSecondsAfterFinished != nil {
		ttl = *mirror.Spec.Job.TTLSecondsAfterFinished
	}
	resources := mirror.Spec.Job.Resources
	if resources.Requests == nil && resources.Limits == nil {
		resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
		}
	}
	labels := LabelsForMirror(mirror)
	if opts.TriggerID != "" {
		labels[LabelDeliveryID] = SanitizeLabelValue(opts.TriggerID)
	}
	env := []corev1.EnvVar{
		{Name: "SOURCE_URL", Value: mirror.Spec.Source.URL},
		{Name: "TARGET_URL", Value: mirror.Spec.Target.URL},
		{Name: "MIRROR_MODE", Value: mode},
	}
	if mode == "additive" {
		env = append(env, corev1.EnvVar{Name: "INCLUDE_TAGS", Value: strconv.FormatBool(mirror.Spec.Mirror.IncludeTags)})
	}
	env = append(env, corev1.EnvVar{Name: "KNOWN_HOSTS_PATH", Value: "/var/run/git-mirror/known-hosts/known_hosts"}, corev1.EnvVar{Name: "HOME", Value: "/tmp"})
	authMounts := []corev1.VolumeMount{}
	authVolumes := []corev1.Volume{}
	sourceEnv, sourceMounts, sourceVolumes, err := endpointAuth("SOURCE", "source", mirror.Spec.Source)
	if err != nil {
		return nil, err
	}
	targetEnv, targetMounts, targetVolumes, err := endpointAuth("TARGET", "target", mirror.Spec.Target)
	if err != nil {
		return nil, err
	}
	env = append(env, sourceEnv...)
	env = append(env, targetEnv...)
	authMounts = append(authMounts, sourceMounts...)
	authMounts = append(authMounts, targetMounts...)
	authVolumes = append(authVolumes, sourceVolumes...)
	authVolumes = append(authVolumes, targetVolumes...)
	annotations := map[string]string{}
	for key, value := range AnnotationsForMirror(mirror) {
		annotations[key] = value
	}
	if opts.Revision != "" {
		annotations[AnnotationRevision] = opts.Revision
	}
	knownHostsName := mirror.Spec.Job.KnownHostsConfigMapName
	if knownHostsName == "" {
		knownHostsName = DefaultKnownHostsCM
	}
	knownHostsKey := mirror.Spec.Job.KnownHostsConfigMapKey
	if knownHostsKey == "" {
		knownHostsKey = "known_hosts"
	}
	serviceAccount := mirror.Spec.Job.ServiceAccountName
	if serviceAccount == "" {
		serviceAccount = "git-mirror-sync"
	}
	readOnly := true
	allowPrivilegeEscalation := false
	runAsNonRoot := true
	runAsUser := int64(65532)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   mirror.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: serviceAccount,
					RestartPolicy:      corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &runAsNonRoot,
						RunAsUser:    &runAsUser,
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{{
						Name:            "sync",
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Env:             env,
						Resources:       resources,
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: &allowPrivilegeEscalation,
							ReadOnlyRootFilesystem:   &readOnly,
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
						VolumeMounts: append(authMounts, []corev1.VolumeMount{
							{Name: "known-hosts", MountPath: "/var/run/git-mirror/known-hosts/known_hosts", SubPath: "known_hosts", ReadOnly: true},
							{Name: "tmp", MountPath: "/tmp"},
						}...),
					}},
					Volumes: append(authVolumes, []corev1.Volume{
						{
							Name: "known-hosts",
							VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: knownHostsName},
								Items: []corev1.KeyToPath{{
									Key:  knownHostsKey,
									Path: "known_hosts",
									Mode: int32Ptr(0444),
								}},
								Optional: boolPtr(true),
							}},
						},
						{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					}...),
				},
			},
		},
	}
	if opts.TriggerID != "" {
		job.Name = NameForMirrorTrigger(mirror, opts.TriggerID)
	} else {
		job.GenerateName = dnsLabel("gitmirror-" + mirror.Name + "-")
	}
	if mirror.Spec.Job.ActiveDeadlineSeconds != nil {
		job.Spec.ActiveDeadlineSeconds = mirror.Spec.Job.ActiveDeadlineSeconds
	}
	if opts.Scheme != nil {
		if err := controllerutil.SetControllerReference(mirror, job, opts.Scheme); err != nil {
			return nil, err
		}
	}
	return &SyncJob{Job: job}, nil
}

func endpointAuth(envPrefix, pathPrefix string, endpoint mirrorv1alpha1.GitEndpointSpec) ([]corev1.EnvVar, []corev1.VolumeMount, []corev1.Volume, error) {
	authType := ""
	if endpoint.Auth != nil {
		authType = endpoint.Auth.Type
	}
	if authType == "" && endpoint.SSHSecretRef != nil {
		authType = authTypeSSH
	}
	env := []corev1.EnvVar{{Name: envPrefix + "_AUTH_TYPE", Value: authType}}
	switch authType {
	case authTypeSSH:
		ref, err := sshAuthRef(endpoint)
		if err != nil {
			return nil, nil, nil, err
		}
		volumeName := pathPrefix + "-ssh-key"
		mountPath := "/var/run/git-mirror/" + pathPrefix + "/ssh_key"
		env = append(env, corev1.EnvVar{Name: envPrefix + "_SSH_KEY_PATH", Value: mountPath})
		return env,
			[]corev1.VolumeMount{{Name: volumeName, MountPath: mountPath, SubPath: ref.Key, ReadOnly: true}},
			[]corev1.Volume{secretVolume(volumeName, *ref)},
			nil
	case authTypeBasic:
		if endpoint.Auth == nil || endpoint.Auth.Basic == nil {
			return nil, nil, nil, fmt.Errorf("%s auth.type=basic requires auth.basic", strings.ToLower(envPrefix))
		}
		env = append(env,
			secretEnvVar(envPrefix+"_AUTH_USERNAME", endpoint.Auth.Basic.UsernameSecretRef),
			secretEnvVar(envPrefix+"_AUTH_PASSWORD", endpoint.Auth.Basic.PasswordSecretRef),
		)
		return env, nil, nil, nil
	case authTypeGitHubApp:
		if endpoint.Auth == nil || endpoint.Auth.GitHubApp == nil {
			return nil, nil, nil, fmt.Errorf("%s auth.type=githubApp requires auth.githubApp", strings.ToLower(envPrefix))
		}
		privateKeyPath := "/var/run/git-mirror/" + pathPrefix + "-github-app/private_key"
		volumeName := pathPrefix + "-github-app-private-key"
		env = append(env,
			secretEnvVar(envPrefix+"_GITHUB_APP_ID", endpoint.Auth.GitHubApp.AppIDSecretRef),
			secretEnvVar(envPrefix+"_GITHUB_APP_INSTALLATION_ID", endpoint.Auth.GitHubApp.InstallationIDSecretRef),
			corev1.EnvVar{Name: envPrefix + "_GITHUB_APP_PRIVATE_KEY_PATH", Value: privateKeyPath},
		)
		if endpoint.Auth.GitHubApp.APIURL != "" {
			env = append(env, corev1.EnvVar{Name: envPrefix + "_GITHUB_APP_API_URL", Value: endpoint.Auth.GitHubApp.APIURL})
		}
		return env,
			[]corev1.VolumeMount{{Name: volumeName, MountPath: privateKeyPath, SubPath: endpoint.Auth.GitHubApp.PrivateKeySecretRef.Key, ReadOnly: true}},
			[]corev1.Volume{secretVolume(volumeName, endpoint.Auth.GitHubApp.PrivateKeySecretRef)},
			nil
	case "":
		return nil, nil, nil, fmt.Errorf("%s auth is required", strings.ToLower(envPrefix))
	default:
		return nil, nil, nil, fmt.Errorf("unsupported %s auth type %q", strings.ToLower(envPrefix), authType)
	}
}

func sshAuthRef(endpoint mirrorv1alpha1.GitEndpointSpec) (*mirrorv1alpha1.SecretKeyRef, error) {
	if endpoint.Auth != nil && endpoint.Auth.SSH != nil {
		return &endpoint.Auth.SSH.PrivateKeyRef, nil
	}
	if endpoint.SSHSecretRef != nil {
		return endpoint.SSHSecretRef, nil
	}
	return nil, fmt.Errorf("auth.type=ssh requires auth.ssh.privateKeyRef or sshSecretRef")
}

func secretEnvVar(name string, ref mirrorv1alpha1.SecretKeyRef) corev1.EnvVar {
	return corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: ref.Name},
			Key:                  ref.Key,
		}},
	}
}

func NameForMirrorTrigger(mirror *mirrorv1alpha1.GitMirror, triggerID string) string {
	sum := sha256.Sum256([]byte(mirror.Namespace + "/" + mirror.Name + "/" + triggerID))
	hash := hex.EncodeToString(sum[:])[:12]
	base := dnsLabel("gitmirror-" + mirror.Name)
	maxBase := 63 - len(hash) - 1
	if len(base) > maxBase {
		base = strings.TrimRight(base[:maxBase], "-")
	}
	if base == "" {
		base = "gitmirror"
	}
	return base + "-" + hash
}

func LabelsForMirror(mirror *mirrorv1alpha1.GitMirror) map[string]string {
	return map[string]string{
		LabelName:        AppName,
		LabelGitMirror:   SanitizeLabelValue(mirror.Name),
		LabelSourceOwner: SanitizeLabelValue(mirror.Spec.GitHub.Owner),
		LabelSourceRepo:  SanitizeLabelValue(mirror.Spec.GitHub.Repo),
	}
}

func AnnotationsForMirror(mirror *mirrorv1alpha1.GitMirror) map[string]string {
	return map[string]string{
		AnnotationGitMirror: mirror.Name,
		AnnotationOwner:     mirror.Spec.GitHub.Owner,
		AnnotationRepo:      mirror.Spec.GitHub.Repo,
	}
}

func secretVolume(name string, ref mirrorv1alpha1.SecretKeyRef) corev1.Volume {
	mode := int32(0444)
	return corev1.Volume{
		Name: name,
		VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
			SecretName:  ref.Name,
			DefaultMode: &mode,
			Items:       []corev1.KeyToPath{{Key: ref.Key, Path: ref.Key, Mode: &mode}},
		}},
	}
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
	return strings.TrimLeft(b.String(), "-")
}

func SanitizeLabelValue(s string) string {
	clean := strings.Trim(dnsLabel(s), "-")
	if clean == "" {
		clean = "value"
	}
	if len(clean) <= 63 {
		return clean
	}
	sum := sha256.Sum256([]byte(s))
	hash := hex.EncodeToString(sum[:])[:12]
	maxBase := 63 - len(hash) - 1
	clean = strings.Trim(clean[:maxBase], "-")
	if clean == "" {
		clean = "value"
	}
	return clean + "-" + hash
}

func int32Ptr(v int32) *int32 { return &v }
func boolPtr(v bool) *bool    { return &v }
