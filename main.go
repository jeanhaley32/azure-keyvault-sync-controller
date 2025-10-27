package main

import (
	"log"
	"path/filepath"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func main() {
	log.Println("Starting SecretProviderClass watcher")

	var kubeconfig string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	} else {
		log.Fatal("Unable to find home directory")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		log.Fatalf("Error building kubeconfig: %v", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error creating dynamic client: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error creating kubernetes clientset: %v", err)
	}

	controller := NewController(dynamicClient, clientset)

	// Start health check server
	healthAddr := ":8080"
	log.Printf("Starting health check server on %s", healthAddr)
	go func() {
		if err := StartHealthCheckServer(healthAddr, controller.healthChecker); err != nil {
			log.Fatalf("Health check server failed: %v", err)
		}
	}()

	controller.Run()
}

