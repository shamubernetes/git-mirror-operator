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
	corev1 "k8s.io/api/core/v1"
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
		http.Error(w, "unsupported GitHub event", http.StatusBadRequest)
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
	if deliveryID == "" {
		http.Error(w, "missing X-GitHub-Delivery", http.StatusBadRequest)
		return
	}
	if mirror.Status.LastDeliveryID == deliveryID {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "duplicate"})
		return
	}
	now := metav1.NewTime(s.now())
	revision := gh.ExtractAfterRevision(body)
	controller.ApplyWebhookIntent(mirror, deliveryID, revision, now)
	if err := controller.UpdateGitMirrorStatus(r.Context(), s.client, mirror); err != nil {
		http.Error(w, "update status", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
