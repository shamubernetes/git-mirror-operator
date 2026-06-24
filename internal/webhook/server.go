package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	"github.com/shamubernetes/git-mirror-operator/internal/controller"
	gh "github.com/shamubernetes/git-mirror-operator/internal/github"
	"github.com/shamubernetes/git-mirror-operator/internal/jobs"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Server struct {
	client           client.Client
	secretReader     client.Reader
	defaultSyncImage string
	scheme           *runtime.Scheme
	now              func() time.Time
}

func NewServer(c client.Client, defaultSyncImage string) *Server {
	return NewServerWithReaders(c, c, defaultSyncImage, nil)
}

func NewServerWithScheme(c client.Client, defaultSyncImage string, scheme *runtime.Scheme) *Server {
	return NewServerWithReaders(c, c, defaultSyncImage, scheme)
}

func NewServerWithReaders(c client.Client, secretReader client.Reader, defaultSyncImage string, scheme *runtime.Scheme) *Server {
	if secretReader == nil {
		secretReader = c
	}
	return &Server{
		client:           c,
		secretReader:     secretReader,
		defaultSyncImage: defaultSyncImage,
		scheme:           scheme,
		now:              func() time.Time { return time.Now().UTC() },
	}
}

func (s *Server) Client() client.Client {
	return s.client
}

func ObjectKey(namespace, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: namespace, Name: name}
}

func (s *Server) HandleGitHub(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	event := r.Header.Get("X-GitHub-Event")
	if event == "" {
		http.Error(w, "missing X-GitHub-Event", http.StatusBadRequest)
		return
	}
	if event == "ping" {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pong"})
		return
	}
	if event != "push" {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	fullName, err := gh.ExtractRepositoryFullName(body)
	if err != nil {
		http.Error(w, "missing repository", http.StatusBadRequest)
		return
	}
	mirror, err := s.findMirror(r.Context(), fullName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	secret, err := s.webhookSecret(r.Context(), mirror)
	if err != nil {
		http.Error(w, "webhook secret unavailable", http.StatusInternalServerError)
		return
	}
	if !gh.VerifySignature256(body, r.Header.Get("X-Hub-Signature-256"), secret) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	if deliveryID != "" && mirror.Status.LastDeliveryID == deliveryID {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "duplicate"})
		return
	}
	now := metav1.NewTime(s.now())
	if deliveryID != "" {
		existingJob, err := s.jobForDelivery(r.Context(), mirror, deliveryID)
		if err != nil {
			http.Error(w, "list jobs", http.StatusInternalServerError)
			return
		}
		if existingJob != nil {
			mirror.Status.ObservedGeneration = mirror.Generation
			mirror.Status.LastWebhookAt = now.DeepCopy()
			mirror.Status.LastDeliveryID = deliveryID
			mirror.Status.LastTriggeredAt = now.DeepCopy()
			mirror.Status.LastJobName = existingJob.Name
			mirror.Status.PendingResync = false
			if revision := gh.ExtractAfterRevision(body); revision != "" {
				mirror.Status.LastMirroredRevision = revision
			}
			if err := controller.UpdateGitMirrorStatus(r.Context(), s.client, mirror); err != nil {
				http.Error(w, "update status", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "scheduled"})
			return
		}
	}
	active, err := s.activeJobExists(r.Context(), mirror)
	if err != nil {
		http.Error(w, "list jobs", http.StatusInternalServerError)
		return
	}
	createJob := controller.ApplyWebhookState(mirror, deliveryID, now, active)
	if revision := gh.ExtractAfterRevision(body); revision != "" {
		mirror.Status.LastMirroredRevision = revision
	}
	if createJob {
		syncJob, err := jobs.BuildSyncJob(mirror, jobs.Options{DefaultImage: s.defaultSyncImage, Scheme: s.scheme, TriggerID: deliveryID})
		if err != nil {
			http.Error(w, "build job", http.StatusInternalServerError)
			return
		}
		mirror.Status.LastJobName = syncJob.Job.Name
		if syncJob.Job.Name == "" {
			mirror.Status.LastJobName = syncJob.Job.GenerateName
		}
		if err := s.client.Create(r.Context(), syncJob.Job); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				http.Error(w, "create job", http.StatusInternalServerError)
				return
			}
		}
	}
	if err := controller.UpdateGitMirrorStatus(r.Context(), s.client, mirror); err != nil {
		http.Error(w, "update status", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "scheduled"})
}

func (s *Server) jobForDelivery(ctx context.Context, mirror *mirrorv1alpha1.GitMirror, deliveryID string) (*batchv1.Job, error) {
	var list batchv1.JobList
	if err := s.client.List(ctx, &list, client.InNamespace(mirror.Namespace), client.MatchingLabels{
		jobs.LabelGitMirror:  mirror.Name,
		jobs.LabelDeliveryID: jobs.SanitizeLabelValue(deliveryID),
	}); err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, nil
	}
	return &list.Items[0], nil
}

func (s *Server) findMirror(ctx context.Context, fullName string) (*mirrorv1alpha1.GitMirror, error) {
	var list mirrorv1alpha1.GitMirrorList
	if err := s.client.List(ctx, &list); err != nil {
		return nil, err
	}
	for i := range list.Items {
		mirror := &list.Items[i]
		if strings.EqualFold(mirror.Spec.Provider, "github") || mirror.Spec.Provider == "" {
			if fullName == mirror.Spec.GitHub.Owner+"/"+mirror.Spec.GitHub.Repo {
				return mirror.DeepCopy(), nil
			}
		}
	}
	return nil, fmt.Errorf("no GitMirror configured for %s", fullName)
}

func (s *Server) webhookSecret(ctx context.Context, mirror *mirrorv1alpha1.GitMirror) ([]byte, error) {
	var secret corev1.Secret
	ref := mirror.Spec.GitHub.WebhookSecretRef
	if err := s.secretReader.Get(ctx, types.NamespacedName{Namespace: mirror.Namespace, Name: ref.Name}, &secret); err != nil {
		return nil, err
	}
	value, ok := secret.Data[ref.Key]
	if !ok {
		return nil, fmt.Errorf("secret %s missing key %s", ref.Name, ref.Key)
	}
	return value, nil
}

func (s *Server) activeJobExists(ctx context.Context, mirror *mirrorv1alpha1.GitMirror) (bool, error) {
	var list batchv1.JobList
	if err := s.client.List(ctx, &list, client.InNamespace(mirror.Namespace), client.MatchingLabels{jobs.LabelGitMirror: mirror.Name}); err != nil {
		return false, err
	}
	for i := range list.Items {
		if controller.JobActive(&list.Items[i]) {
			return true, nil
		}
	}
	return false, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
