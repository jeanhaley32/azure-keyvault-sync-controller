package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	akvv1alpha1 "github.com/jeanhaley32/azure-keyvault-sync-controller/api/v1alpha1"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/config"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/controller"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/health"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/logger"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	spcclient "sigs.k8s.io/secrets-store-csi-driver/pkg/client/clientset/versioned"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	// Load and validate configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		// Can't use slog yet since logger isn't initialized
		println("FATAL: Configuration error:", err.Error())
		os.Exit(1)
	}

	// Initialize structured logger with configuration
	logger.InitLogger(cfg)

	slog.Info("Starting Azure Key Vault Sync Controller")
	slog.Info("Configuration loaded",
		"syncInterval", cfg.SyncInterval,
		"workerCount", cfg.WorkerCount,
		"logLevel", cfg.LogLevel)

	// Try in-cluster config first (for running in Kubernetes)
	config, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		// Fall back to kubeconfig file for local development
		slog.Info("In-cluster config not available, trying kubeconfig file")
		var kubeconfig string
		if home := homedir.HomeDir(); home != "" {
			kubeconfig = filepath.Join(home, ".kube", "config")
		} else {
			slog.Error("Unable to find home directory for kubeconfig")
			os.Exit(1)
		}

		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			slog.Error("Error building kubeconfig", "error", err)
			os.Exit(1)
		}
		slog.Info("Using kubeconfig file", "path", kubeconfig)
	} else {
		slog.Info("Using in-cluster configuration")
	}

	// Apply Kubernetes API rate limits
	config.QPS = cfg.KubernetesQPS
	config.Burst = cfg.KubernetesBurst
	slog.Info("Kubernetes API rate limits configured",
		"qps", cfg.KubernetesQPS,
		"burst", cfg.KubernetesBurst)

	spcClientset, err := spcclient.NewForConfig(config)
	if err != nil {
		slog.Error("Error creating secrets store CSI client", "error", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		slog.Error("Error creating kubernetes clientset", "error", err)
		os.Exit(1)
	}

	// Setup scheme with our CRD types
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		slog.Error("Error adding client-go scheme", "error", err)
		os.Exit(1)
	}
	if err := akvv1alpha1.AddToScheme(scheme); err != nil {
		slog.Error("Error adding AzureKeyVaultSync scheme", "error", err)
		os.Exit(1)
	}
	// Add SecretProviderClass scheme for CRD reconciliation
	if err := secretsstorev1.AddToScheme(scheme); err != nil {
		slog.Error("Error adding SecretProviderClass scheme", "error", err)
		os.Exit(1)
	}

	// Create controller-runtime manager (provides client and cache)
	// Disable manager metrics by setting bind address to "0" (we have our own metrics server)
	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress: "0",
		},
	})
	if err != nil {
		slog.Error("Error creating controller-runtime manager", "error", err)
		os.Exit(1)
	}

	// Read watch namespace from environment (empty = cluster-wide for backward compatibility)
	watchNamespace := os.Getenv("WATCH_NAMESPACE")
	if watchNamespace != "" {
		slog.Info("Namespace-scoped mode enabled", "namespace", watchNamespace)
	} else {
		slog.Info("Cluster-wide mode enabled (watching all namespaces)")
	}

	ctrl := controller.NewController(spcClientset, clientset, mgr.GetClient(), cfg, watchNamespace)

	// Register AzureKeyVaultSync CRD reconciler with manager
	akvReconciler := &controller.AzureKeyVaultSyncReconciler{
		Client:     mgr.GetClient(),
		Controller: ctrl,
	}
	if err := akvReconciler.SetupWithManager(mgr); err != nil {
		slog.Error("Failed to setup AzureKeyVaultSync reconciler", "error", err)
		os.Exit(1)
	}
	slog.Info("AzureKeyVaultSync reconciler registered successfully")

	// Start health check server
	healthAddr := fmt.Sprintf(":%d", cfg.HealthCheckPort)
	slog.Info("Starting health check server", "address", healthAddr)
	go func() {
		if err := health.StartHealthCheckServer(healthAddr, ctrl.HealthChecker); err != nil {
			slog.Error("Health check server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	// Start controller-runtime manager in goroutine
	managerDone := make(chan struct{})
	go func() {
		defer close(managerDone)
		if err := mgr.Start(ctx); err != nil {
			slog.Error("Controller-runtime manager error", "error", err)
		}
	}()

	// Start controller in goroutine
	controllerDone := make(chan struct{})
	go func() {
		defer close(controllerDone)
		ctrl.Run(ctx)
	}()

	// Wait for shutdown signal
	sig := <-sigChan
	slog.Info("Received shutdown signal", "signal", sig.String())

	// Cancel context to trigger graceful shutdown
	cancel()

	// Wait for controller to finish with timeout
	shutdownTimeout := 30 * time.Second
	slog.Info("Waiting for graceful shutdown", "timeout", shutdownTimeout)

	select {
	case <-controllerDone:
		slog.Info("Controller shutdown complete")
	case <-time.After(shutdownTimeout):
		slog.Warn("Shutdown timeout exceeded, forcing exit")
	}

	slog.Info("Shutdown complete")
}
