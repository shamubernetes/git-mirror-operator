package main

import (
	"context"
	"flag"
	"net/http"
	"os"

	mirrorv1alpha1 "github.com/shamubernetes/git-mirror-operator/api/v1alpha1"
	"github.com/shamubernetes/git-mirror-operator/internal/controller"
	"github.com/shamubernetes/git-mirror-operator/internal/webhook"
	batchv1 "k8s.io/api/batch/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	var metricsAddr string
	var probeAddr string
	var webhookAddr string
	var leaderElection bool
	var syncImage string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the health probe endpoint binds to.")
	flag.StringVar(&webhookAddr, "webhook-bind-address", ":8082", "The address the GitHub webhook endpoint binds to.")
	flag.BoolVar(&leaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.StringVar(&syncImage, "sync-image", getenv("SYNC_IMAGE", "ghcr.io/shamubernetes/git-mirror-sync:latest"), "Sync runner image used by Jobs.")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	scheme := clientgoscheme.Scheme
	must(batchv1.AddToScheme(scheme))
	must(mirrorv1alpha1.AddToScheme(scheme))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElection,
		LeaderElectionID:       "git-mirror-operator.mirror.maude.dev",
	})
	must(err)

	reconciler := &controller.GitMirrorReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		DefaultSyncImage: syncImage,
	}
	must(reconciler.SetupWithManager(mgr))
	must(mgr.AddHealthzCheck("healthz", healthz.Ping))
	must(mgr.AddReadyzCheck("readyz", healthz.Ping))

	webhookServer := webhook.NewServerWithScheme(mgr.GetClient(), syncImage, mgr.GetScheme())
	mux := http.NewServeMux()
	mux.HandleFunc("/webhooks/github", webhookServer.HandleGitHub)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	must(mgr.Add(&httpServer{addr: webhookAddr, handler: mux}))

	must(mgr.Start(ctrl.SetupSignalHandler()))
}

type httpServer struct {
	addr    string
	handler http.Handler
	server  *http.Server
}

func (s *httpServer) Start(ctx context.Context) error {
	s.server = &http.Server{Addr: s.addr, Handler: s.handler}
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		return s.server.Shutdown(context.Background())
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
