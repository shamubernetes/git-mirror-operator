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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GitMirrorSpec defines the desired state of GitMirror.
type GitMirrorSpec struct {
	// +kubebuilder:validation:Enum=github
	Provider string `json:"provider"`

	GitHub   GitHubSpec      `json:"github"`
	Source   GitEndpointSpec `json:"source"`
	Target   GitEndpointSpec `json:"target"`
	Mirror   MirrorSpec      `json:"mirror"`
	Fallback FallbackSpec    `json:"fallback,omitempty"`
	Job      JobSpec         `json:"job,omitempty"`
}

type GitHubSpec struct {
	// +kubebuilder:validation:MinLength=1
	Owner string `json:"owner"`
	// +kubebuilder:validation:MinLength=1
	Repo string `json:"repo"`

	WebhookSecretRef SecretKeyRef `json:"webhookSecretRef"`
}

// +kubebuilder:validation:XValidation:rule="has(self.auth) || has(self.sshSecretRef)",message="auth or sshSecretRef is required"
type GitEndpointSpec struct {
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// SSHSecretRef is the legacy SSH private key reference. Prefer auth.type=ssh for new manifests.
	SSHSecretRef *SecretKeyRef `json:"sshSecretRef,omitempty"`

	Auth *GitAuthSpec `json:"auth,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self.type != 'ssh' || has(self.ssh)",message="auth.type ssh requires auth.ssh"
// +kubebuilder:validation:XValidation:rule="self.type != 'basic' || has(self.basic)",message="auth.type basic requires auth.basic"
// +kubebuilder:validation:XValidation:rule="self.type != 'githubApp' || has(self.githubApp)",message="auth.type githubApp requires auth.githubApp"
// +kubebuilder:validation:XValidation:rule="self.type == 'ssh' || !has(self.ssh)",message="auth.ssh is only valid with auth.type ssh"
// +kubebuilder:validation:XValidation:rule="self.type == 'basic' || !has(self.basic)",message="auth.basic is only valid with auth.type basic"
// +kubebuilder:validation:XValidation:rule="self.type == 'githubApp' || !has(self.githubApp)",message="auth.githubApp is only valid with auth.type githubApp"
type GitAuthSpec struct {
	// +kubebuilder:validation:Enum=ssh;basic;githubApp
	Type string `json:"type"`

	SSH       *SSHAuthSpec       `json:"ssh,omitempty"`
	Basic     *BasicAuthSpec     `json:"basic,omitempty"`
	GitHubApp *GitHubAppAuthSpec `json:"githubApp,omitempty"`
}

type SSHAuthSpec struct {
	PrivateKeyRef SecretKeyRef `json:"privateKeyRef"`
}

type BasicAuthSpec struct {
	UsernameSecretRef SecretKeyRef `json:"usernameSecretRef"`
	PasswordSecretRef SecretKeyRef `json:"passwordSecretRef"`
}

type GitHubAppAuthSpec struct {
	// AppIDSecretRef references the GitHub App client ID or app ID used as the JWT issuer.
	AppIDSecretRef          SecretKeyRef `json:"appIDSecretRef"`
	InstallationIDSecretRef SecretKeyRef `json:"installationIDSecretRef"`
	PrivateKeySecretRef     SecretKeyRef `json:"privateKeySecretRef"`
	// APIURL defaults to https://api.github.com. Set this for GitHub Enterprise Server.
	// +kubebuilder:validation:Pattern=`^https://.*`
	APIURL string `json:"apiURL,omitempty"`
}

type SecretKeyRef struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

type MirrorSpec struct {
	// Mode controls target ref behavior. exact mirrors all refs and prunes target refs that are absent
	// from the source. additive pushes source heads and optionally tags without pruning target refs.
	// +kubebuilder:validation:Enum=exact;additive
	// +kubebuilder:default=exact
	Mode string `json:"mode,omitempty"`
	// IncludeTags applies only when mode is additive. Exact mode mirrors all refs, including tags.
	// +kubebuilder:default=true
	IncludeTags bool `json:"includeTags,omitempty"`
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

// GitMirrorStatus defines the observed state of GitMirror.
type GitMirrorStatus struct {
	ObservedGeneration int64        `json:"observedGeneration,omitempty"`
	LastWebhookAt      *metav1.Time `json:"lastWebhookAt,omitempty"`
	LastDeliveryID     string       `json:"lastDeliveryID,omitempty"`
	LastTriggeredAt    *metav1.Time `json:"lastTriggeredAt,omitempty"`
	LastJobName        string       `json:"lastJobName,omitempty"`
	// LastCompletedJobName is the latest finished Job that has been reflected into status.
	LastCompletedJobName string       `json:"lastCompletedJobName,omitempty"`
	LastSuccessAt        *metav1.Time `json:"lastSuccessAt,omitempty"`
	LastFailureAt        *metav1.Time `json:"lastFailureAt,omitempty"`
	LastError            string       `json:"lastError,omitempty"`
	// LastRequestedRevision is the latest GitHub push revision accepted by the webhook.
	LastRequestedRevision string `json:"lastRequestedRevision,omitempty"`
	// LastMirroredRevision is the latest requested revision whose sync Job completed successfully.
	LastMirroredRevision string `json:"lastMirroredRevision,omitempty"`
	PendingResync        bool   `json:"pendingResync,omitempty"`
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Repo",type=string,JSONPath=`.spec.github.repo`
// +kubebuilder:printcolumn:name="Last Success",type=date,JSONPath=`.status.lastSuccessAt`
// +kubebuilder:printcolumn:name="Pending",type=boolean,JSONPath=`.status.pendingResync`

// GitMirror is the Schema for the gitmirrors API.
type GitMirror struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GitMirrorSpec   `json:"spec,omitempty"`
	Status GitMirrorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GitMirrorList contains a list of GitMirror.
type GitMirrorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GitMirror `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GitMirror{}, &GitMirrorList{})
}
