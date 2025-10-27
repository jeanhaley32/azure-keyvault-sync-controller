package main

import (
	"log/slog"
	"os"
	"path/filepath"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func main() {
	// Initialize structured logger
	InitLogger()

	slog.Info("Starting Azure Key Vault Sync Controller")

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

	controller := NewController(dynamicClient, clientset)

	// Start health check server
	healthAddr := ":8080"
	slog.Info("Starting health check server", "address", healthAddr)
	go func() {
		if err := StartHealthCheckServer(healthAddr, controller.healthChecker); err != nil {
			slog.Error("Health check server failed", "error", err)
			os.Exit(1)
		}
	}()

	controller.Run()
}

