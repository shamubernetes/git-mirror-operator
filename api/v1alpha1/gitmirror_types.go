package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type GitMirror struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GitMirrorSpec   `json:"spec,omitempty"`
	Status GitMirrorStatus `json:"status,omitempty"`
}

type GitMirrorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []GitMirror `json:"items"`
}

type GitMirrorSpec struct {
	Provider string          `json:"provider,omitempty"`
	GitHub   GitHubSpec      `json:"github,omitempty"`
	Source   GitEndpointSpec `json:"source,omitempty"`
	Target   GitEndpointSpec `json:"target,omitempty"`
	Mirror   MirrorSpec      `json:"mirror,omitempty"`
	Fallback FallbackSpec    `json:"fallback,omitempty"`
	Job      JobSpec         `json:"job,omitempty"`
}

type GitHubSpec struct {
	Owner            string       `json:"owner,omitempty"`
	Repo             string       `json:"repo,omitempty"`
	WebhookSecretRef SecretKeyRef `json:"webhookSecretRef,omitempty"`
}

type GitEndpointSpec struct {
	URL          string       `json:"url,omitempty"`
	SSHSecretRef SecretKeyRef `json:"sshSecretRef,omitempty"`
}

type SecretKeyRef struct {
	Name string `json:"name,omitempty"`
	Key  string `json:"key,omitempty"`
}

type MirrorSpec struct {
	Mode        string `json:"mode,omitempty"`
	IncludeTags bool   `json:"includeTags,omitempty"`
	Prune       bool   `json:"prune,omitempty"`
}

type FallbackSpec struct {
	Schedule string `json:"schedule,omitempty"`
}

type JobSpec struct {
	Image                   string                      `json:"image,omitempty"`
	BackoffLimit            *int32                      `json:"backoffLimit,omitempty"`
	ActiveDeadlineSeconds   *int64                      `json:"activeDeadlineSeconds,omitempty"`
	TTLSecondsAfterFinished *int32                      `json:"ttlSecondsAfterFinished,omitempty"`
	Resources               corev1.ResourceRequirements `json:"resources,omitempty"`
	KnownHostsConfigMapName string                      `json:"knownHostsConfigMapName,omitempty"`
	KnownHostsConfigMapKey  string                      `json:"knownHostsConfigMapKey,omitempty"`
	ServiceAccountName      string                      `json:"serviceAccountName,omitempty"`
}

type GitMirrorStatus struct {
	ObservedGeneration   int64              `json:"observedGeneration,omitempty"`
	LastWebhookAt        *metav1.Time       `json:"lastWebhookAt,omitempty"`
	LastDeliveryID       string             `json:"lastDeliveryID,omitempty"`
	LastTriggeredAt      *metav1.Time       `json:"lastTriggeredAt,omitempty"`
	LastJobName          string             `json:"lastJobName,omitempty"`
	LastSuccessAt        *metav1.Time       `json:"lastSuccessAt,omitempty"`
	LastFailureAt        *metav1.Time       `json:"lastFailureAt,omitempty"`
	LastError            string             `json:"lastError,omitempty"`
	LastMirroredRevision string             `json:"lastMirroredRevision,omitempty"`
	PendingResync        bool               `json:"pendingResync,omitempty"`
	Conditions           []metav1.Condition `json:"conditions,omitempty"`
}

func (in *GitMirror) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(GitMirror)
	*out = *in
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()
	out.Spec = *in.Spec.DeepCopy()
	out.Status = *in.Status.DeepCopy()
	return out
}

func (in *GitMirrorList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(GitMirrorList)
	*out = *in
	out.ListMeta = in.ListMeta
	if in.Items != nil {
		out.Items = make([]GitMirror, len(in.Items))
		for i := range in.Items {
			out.Items[i] = *in.Items[i].DeepCopy()
		}
	}
	return out
}

func (in *GitMirror) DeepCopy() *GitMirror {
	if in == nil {
		return nil
	}
	return in.DeepCopyObject().(*GitMirror)
}

func (in *GitMirrorSpec) DeepCopy() *GitMirrorSpec {
	if in == nil {
		return nil
	}
	out := new(GitMirrorSpec)
	*out = *in
	out.Job.Resources = *in.Job.Resources.DeepCopy()
	if in.Job.BackoffLimit != nil {
		out.Job.BackoffLimit = new(int32)
		*out.Job.BackoffLimit = *in.Job.BackoffLimit
	}
	if in.Job.ActiveDeadlineSeconds != nil {
		out.Job.ActiveDeadlineSeconds = new(int64)
		*out.Job.ActiveDeadlineSeconds = *in.Job.ActiveDeadlineSeconds
	}
	if in.Job.TTLSecondsAfterFinished != nil {
		out.Job.TTLSecondsAfterFinished = new(int32)
		*out.Job.TTLSecondsAfterFinished = *in.Job.TTLSecondsAfterFinished
	}
	return out
}

func (in *GitMirrorStatus) DeepCopy() *GitMirrorStatus {
	if in == nil {
		return nil
	}
	out := new(GitMirrorStatus)
	*out = *in
	if in.LastWebhookAt != nil {
		out.LastWebhookAt = in.LastWebhookAt.DeepCopy()
	}
	if in.LastTriggeredAt != nil {
		out.LastTriggeredAt = in.LastTriggeredAt.DeepCopy()
	}
	if in.LastSuccessAt != nil {
		out.LastSuccessAt = in.LastSuccessAt.DeepCopy()
	}
	if in.LastFailureAt != nil {
		out.LastFailureAt = in.LastFailureAt.DeepCopy()
	}
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		copy(out.Conditions, in.Conditions)
	}
	return out
}
