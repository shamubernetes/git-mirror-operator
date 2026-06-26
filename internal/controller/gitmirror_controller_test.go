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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/shamubernetes/git-mirror-operator/internal/jobs"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
)

var _ = Describe("GitMirror Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		gitmirror := &mirrorv1alpha1.GitMirror{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind GitMirror")
			err := k8sClient.Get(ctx, typeNamespacedName, gitmirror)
			if err != nil && errors.IsNotFound(err) {
				resource := &mirrorv1alpha1.GitMirror{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: mirrorv1alpha1.GitMirrorSpec{
						Provider: "github",
						GitHub: mirrorv1alpha1.GitHubSpec{
							Owner: "example",
							Repo:  "source-repo",
							WebhookSecretRef: mirrorv1alpha1.SecretKeyRef{
								Name: "webhook-secret",
								Key:  "secret",
							},
						},
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
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &mirrorv1alpha1.GitMirror{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance GitMirror")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &GitMirrorReconciler{
				Client:           k8sClient,
				Scheme:           k8sClient.Scheme(),
				DefaultSyncImage: "example/git-mirror-sync:test",
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})

	Context("When the GitMirror spec generation changes", func() {
		ctx := context.Background()

		AfterEach(func() {
			var mirrors mirrorv1alpha1.GitMirrorList
			Expect(k8sClient.List(ctx, &mirrors, client.InNamespace("default"))).To(Succeed())
			for i := range mirrors.Items {
				if mirrors.Items[i].Name == "stale-failed-generation" || mirrors.Items[i].Name == "stale-active-generation" {
					Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &mirrors.Items[i]))).To(Succeed())
				}
			}
			var jobList batchv1.JobList
			Expect(k8sClient.List(ctx, &jobList, client.InNamespace("default"))).To(Succeed())
			for i := range jobList.Items {
				if jobList.Items[i].Labels[jobs.LabelGitMirror] == jobs.SanitizeLabelValue("stale-failed-generation") ||
					jobList.Items[i].Labels[jobs.LabelGitMirror] == jobs.SanitizeLabelValue("stale-active-generation") {
					Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &jobList.Items[i]))).To(Succeed())
				}
			}
		})

		It("creates a current-generation Job after an older failed Job", func() {
			mirror := envGitMirror("stale-failed-generation")
			Expect(k8sClient.Create(ctx, mirror)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mirror), mirror)).To(Succeed())
			Expect(mirror.Generation).To(Equal(int64(1)))
			failedJob := generationJob(mirror, "stale-failed-generation-gen1-failed", 1, []batchv1.JobCondition{{
				Type:    batchv1.JobFailed,
				Status:  corev1.ConditionTrue,
				Reason:  "BackoffLimitExceeded",
				Message: "old exact sync failed",
			}})
			Expect(k8sClient.Create(ctx, failedJob)).To(Succeed())

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mirror), mirror)).To(Succeed())
			mirror.Spec.Mirror.Mode = "additive"
			Expect(k8sClient.Update(ctx, mirror)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mirror), mirror)).To(Succeed())
			Expect(mirror.Generation).To(Equal(int64(2)))

			controllerReconciler := &GitMirrorReconciler{
				Client:           k8sClient,
				Scheme:           k8sClient.Scheme(),
				DefaultSyncImage: "example/git-mirror-sync:test",
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mirror)})
			Expect(err).NotTo(HaveOccurred())

			jobsForMirror := listEnvJobsForMirror(ctx, mirror)
			Expect(jobsForMirror).To(HaveLen(2))
			currentJob := findEnvJobForGeneration(jobsForMirror, "2")
			Expect(currentJob).NotTo(BeNil())
			Expect(currentJob.Annotations[jobs.AnnotationGeneration]).To(Equal("2"))
		})

		It("creates a current-generation Job while an older generation Job is still active", func() {
			mirror := envGitMirror("stale-active-generation")
			Expect(k8sClient.Create(ctx, mirror)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mirror), mirror)).To(Succeed())
			Expect(mirror.Generation).To(Equal(int64(1)))
			activeJob := generationJob(mirror, "stale-active-generation-gen1-active", 1, nil)
			Expect(k8sClient.Create(ctx, activeJob)).To(Succeed())

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mirror), mirror)).To(Succeed())
			mirror.Spec.Mirror.Mode = "additive"
			Expect(k8sClient.Update(ctx, mirror)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mirror), mirror)).To(Succeed())
			Expect(mirror.Generation).To(Equal(int64(2)))

			controllerReconciler := &GitMirrorReconciler{
				Client:           k8sClient,
				Scheme:           k8sClient.Scheme(),
				DefaultSyncImage: "example/git-mirror-sync:test",
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mirror)})
			Expect(err).NotTo(HaveOccurred())

			jobsForMirror := listEnvJobsForMirror(ctx, mirror)
			Expect(jobsForMirror).To(HaveLen(2))
			currentJob := findEnvJobForGeneration(jobsForMirror, "2")
			Expect(currentJob).NotTo(BeNil())
			Expect(currentJob.Annotations[jobs.AnnotationGeneration]).To(Equal("2"))
		})
	})
})

func envGitMirror(name string) *mirrorv1alpha1.GitMirror {
	return &mirrorv1alpha1.GitMirror{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: mirrorv1alpha1.GitMirrorSpec{
			Provider: "github",
			GitHub: mirrorv1alpha1.GitHubSpec{
				Owner: "example",
				Repo:  name,
				WebhookSecretRef: mirrorv1alpha1.SecretKeyRef{
					Name: "webhook-secret",
					Key:  "secret",
				},
			},
			Source: mirrorv1alpha1.GitEndpointSpec{
				URL:          "git@github.com:example/" + name + ".git",
				SSHSecretRef: &mirrorv1alpha1.SecretKeyRef{Name: "source-key", Key: "ssh-privatekey"},
			},
			Target: mirrorv1alpha1.GitEndpointSpec{
				URL:          "git@codeberg.org:example/" + name + ".git",
				SSHSecretRef: &mirrorv1alpha1.SecretKeyRef{Name: "target-key", Key: "ssh-privatekey"},
			},
			Mirror: mirrorv1alpha1.MirrorSpec{Mode: "exact", IncludeTags: true},
		},
	}
}

func listEnvJobsForMirror(ctx context.Context, mirror *mirrorv1alpha1.GitMirror) []batchv1.Job {
	labels := jobs.LabelsForMirror(mirror)
	var jobList batchv1.JobList
	Expect(k8sClient.List(ctx, &jobList, client.InNamespace(mirror.Namespace), client.MatchingLabels{jobs.LabelGitMirror: labels[jobs.LabelGitMirror]})).To(Succeed())
	return jobList.Items
}

func findEnvJobForGeneration(jobList []batchv1.Job, generation string) *batchv1.Job {
	for i := range jobList {
		if jobList[i].Annotations[jobs.AnnotationGeneration] == generation {
			return &jobList[i]
		}
	}
	return nil
}
