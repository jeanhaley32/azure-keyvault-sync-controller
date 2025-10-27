package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func main() {
	// Load and validate configuration
	cfg, err := LoadConfig()
	if err != nil {
		// Can't use slog yet since logger isn't initialized
		println("FATAL: Configuration error:", err.Error())
		os.Exit(1)
	}

	// Initialize structured logger with configuration
	InitLogger(cfg)

	slog.Info("Starting Azure Key Vault Sync Controller")
	slog.Info("Configuration loaded",
		"syncInterval", cfg.SyncInterval,
		"workerCount", cfg.WorkerCount,
		"logLevel", cfg.LogLevel)

	var kubeconfig string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	} else {
		slog.Error("Unable to find home directory")
		os.Exit(1)
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		slog.Error("Error building kubeconfig", "error", err)
		os.Exit(1)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		slog.Error("Error creating dynamic client", "error", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		slog.Error("Error creating kubernetes clientset", "error", err)
		os.Exit(1)
	}

	// Read watch namespace from environment (empty = cluster-wide for backward compatibility)
	watchNamespace := os.Getenv("WATCH_NAMESPACE")
	if watchNamespace != "" {
		slog.Info("Namespace-scoped mode enabled", "namespace", watchNamespace)
	} else {
		slog.Info("Cluster-wide mode enabled (watching all namespaces)")
	}

	controller := NewController(dynamicClient, clientset, cfg, watchNamespace)

	// Start health check server
	healthAddr := fmt.Sprintf(":%d", cfg.HealthCheckPort)
	slog.Info("Starting health check server", "address", healthAddr)
	go func() {
		if err := StartHealthCheckServer(healthAddr, controller.healthChecker); err != nil {
			slog.Error("Health check server failed", "error", err)
			os.Exit(1)
		}
	}()

	controller.Run()
}

