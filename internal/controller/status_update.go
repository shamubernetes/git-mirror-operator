package controller

import (
	"context"

	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func PatchGitMirrorStatus(ctx context.Context, c client.Client, key types.NamespacedName, mutate func(*mirrorv1alpha1.GitMirror)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var current mirrorv1alpha1.GitMirror
		if err := c.Get(ctx, key, &current); err != nil {
			return err
		}
		base := current.DeepCopy()
		mutate(&current)
		if err := c.Status().Patch(ctx, &current, client.MergeFrom(base)); err != nil {
			return c.Patch(ctx, &current, client.MergeFrom(base))
		}
		return nil
	})
}
