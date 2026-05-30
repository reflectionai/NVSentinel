// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/nvidia/nvsentinel/commons/pkg/logger"
	preflightv1alpha1 "github.com/nvidia/nvsentinel/preflight/pkg/apis/preflight/v1alpha1"
	"github.com/nvidia/nvsentinel/preflight/pkg/config"
	"github.com/nvidia/nvsentinel/preflight/pkg/controller"
	"github.com/nvidia/nvsentinel/preflight/pkg/gang"
	"github.com/nvidia/nvsentinel/preflight/pkg/registry"
	"github.com/nvidia/nvsentinel/preflight/pkg/webhook"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"

	scheme = runtime.NewScheme()

	discoverer     gang.GangDiscoverer
	onGangRegister webhook.GangRegistrationFunc
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(preflightv1alpha1.AddToScheme(scheme))
}

func main() {
	logger.SetDefaultStructuredLogger("preflight", version)

	ctrllog.SetLogger(logr.FromSlogHandler(slog.Default().Handler()))

	slog.Info("Starting preflight", "version", version, "commit", commit, "date", date)

	if err := run(); err != nil {
		slog.Error("Fatal error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		port       int
		certDir    string
		configFile string
	)

	flag.IntVar(&port, "port", 8443, "Webhook server port")
	flag.StringVar(&certDir, "cert-dir", "/certs", "Directory containing TLS certificates")
	flag.StringVar(&configFile, "config", "/etc/preflight/config.yaml", "Path to config file")
	flag.Parse()

	cfg, err := config.Load(configFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	cfg.Port = port
	cfg.CertDir = certDir

	slog.Info("Configuration loaded",
		"initContainers", len(cfg.InitContainers),
		"gpuResourceNames", cfg.GPUResourceNames,
		"gangCoordinationEnabled", cfg.GangCoordination.Enabled,
		"dynamicChecksEnabled", cfg.DynamicChecks)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The registry is the injector's view of available checks: the static
	// chart config, optionally augmented at runtime by PreflightCheck CRs.
	reg := registry.New(cfg.InitContainers)

	// A controller manager is needed for gang coordination and/or for watching
	// PreflightCheck CRs. Start one only if either feature is enabled.
	if cfg.GangCoordination.Enabled || cfg.DynamicChecks {
		if err := setupManager(ctx, cfg, reg, stop); err != nil {
			return err
		}
	}

	handler := webhook.NewHandlerWithRegistry(cfg, discoverer, onGangRegister, reg)

	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", handler.HandleMutate)
	mux.HandleFunc("/healthz", handleHealth)

	return runHTTPServer(ctx, mux, certDir, port)
}

// setupManager creates the controller manager and wires up whichever
// controllers are enabled: the gang controller (gang coordination) and/or the
// PreflightCheck controller (dynamic check registration). The manager is
// started in the background; if it fails, the process is asked to shut down.
func setupManager(ctx context.Context, cfg *config.Config, reg *registry.Registry, stop context.CancelFunc) error {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to get in-cluster config: %w", err)
	}

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("failed to create controller manager: %w", err)
	}

	if cfg.GangCoordination.Enabled {
		if err := setupGangController(cfg, mgr); err != nil {
			return err
		}
	}

	if cfg.DynamicChecks {
		pfcController := controller.NewPreflightCheckController(mgr.GetClient(), reg)
		if err := pfcController.SetupWithManager(mgr); err != nil {
			return fmt.Errorf("failed to setup PreflightCheck controller: %w", err)
		}

		slog.Info("Dynamic preflight-check registration enabled (watching PreflightCheck CRs)")
	}

	go func() {
		if err := mgr.Start(ctx); err != nil {
			slog.Error("Controller manager failed, initiating shutdown", "error", err)
			stop()
		}
	}()

	return nil
}

// setupGangController builds the gang discoverer, coordinator, and controller,
// and registers the controller with the manager. It also publishes the
// discoverer and gang-registration hook used by the admission handler.
func setupGangController(cfg *config.Config, mgr ctrl.Manager) error {
	var err error

	discoverer, err = gang.NewDiscovererFromConfig(
		cfg.GangDiscovery,
		mgr.GetClient(),
		mgr.GetRESTMapper(),
		gang.HasGangConfigVolume,
	)
	if err != nil {
		return fmt.Errorf("failed to create gang discoverer: %w", err)
	}

	coordinatorConfig := gang.CoordinatorConfig{
		MasterPort: cfg.GangCoordination.MasterPort,
	}
	coordinator := gang.NewCoordinator(mgr.GetClient(), coordinatorConfig)

	gangController := controller.NewGangController(
		cfg,
		mgr.GetClient(),
		coordinator,
		discoverer,
	)

	if err := gangController.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup gang controller: %w", err)
	}

	onGangRegister = gangController.RegisterPod

	discovererName := "kubernetes"
	if cfg.GangDiscovery.Name != "" {
		discovererName = cfg.GangDiscovery.Name
	}

	slog.Info("Gang coordination enabled",
		"discoverer", discovererName,
		"timeout", cfg.GangCoordination.Timeout,
		"masterPort", cfg.GangCoordination.MasterPort)

	return nil
}

func runHTTPServer(ctx context.Context, handler http.Handler, certDir string, port int) error {
	certPath := filepath.Join(certDir, "tls.crt")
	keyPath := filepath.Join(certDir, "tls.key")

	certWatcher, err := certwatcher.New(certPath, keyPath)
	if err != nil {
		return fmt.Errorf("failed to create certificate watcher: %w", err)
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			GetCertificate: certWatcher.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		},
	}

	go func() {
		if err := certWatcher.Start(ctx); err != nil {
			slog.Error("Certificate watcher failed", "error", err)
		}
	}()

	go func() {
		slog.Info("Starting HTTPS server", "port", port)

		if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			slog.Error("Server failed", "error", err)
		}
	}()

	<-ctx.Done()
	slog.Info("Shutting down server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return server.Shutdown(shutdownCtx)
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
