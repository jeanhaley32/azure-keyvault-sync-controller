package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type SecretProviderClassCache struct {
	mu      sync.RWMutex
	objects map[string]*unstructured.Unstructured
}

func NewCache() *SecretProviderClassCache {
	return &SecretProviderClassCache{
		objects: make(map[string]*unstructured.Unstructured),
	}
}

func (c *SecretProviderClassCache) Set(namespace, name string, obj *unstructured.Unstructured) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := fmt.Sprintf("%s/%s", namespace, name)
	c.objects[key] = obj
}

func (c *SecretProviderClassCache) Delete(namespace, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := fmt.Sprintf("%s/%s", namespace, name)
	delete(c.objects, key)
}

func (c *SecretProviderClassCache) List() []*unstructured.Unstructured {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]*unstructured.Unstructured, 0, len(c.objects))
	for _, obj := range c.objects {
		result = append(result, obj)
	}
	return result
}

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

	gvr := schema.GroupVersionResource{
		Group:    "secrets-store.csi.x-k8s.io",
		Version:  "v1",
		Resource: "secretproviderclasses",
	}

	cache := NewCache()
	ctx := context.Background()

	printCache := func() {
		objects := cache.List()
		fmt.Printf("\n--- Current SecretProviderClass objects: %d ---\n", len(objects))
		for _, obj := range objects {
			fmt.Printf("  %s/%s\n", obj.GetNamespace(), obj.GetName())
		}
		fmt.Println("---")
	}

	listAndSync := func() {
		log.Println("Performing full resync")
		result, err := dynamicClient.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{})
		if err != nil {
			log.Printf("Error listing SecretProviderClasses: %v", err)
			return
		}

		for _, item := range result.Items {
			cache.Set(item.GetNamespace(), item.GetName(), item.DeepCopy())
		}

		log.Printf("Resync complete: %d objects in cache", len(result.Items))
	}

	listAndSync()
	printCache()

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			listAndSync()
		}
	}()

	log.Println("Watching for events...")

	for {
		watcher, err := dynamicClient.Resource(gvr).Namespace("").Watch(ctx, metav1.ListOptions{})
		if err != nil {
			log.Printf("Error creating watcher: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		for event := range watcher.ResultChan() {
			obj, ok := event.Object.(*unstructured.Unstructured)
			if !ok {
				log.Printf("Unexpected object type: %T", event.Object)
				continue
			}

			namespace := obj.GetNamespace()
			name := obj.GetName()

			switch event.Type {
			case watch.Added:
				log.Printf("Event: ADDED %s/%s", namespace, name)
				cache.Set(namespace, name, obj.DeepCopy())
				printCache()
			case watch.Modified:
				log.Printf("Event: MODIFIED %s/%s", namespace, name)
				cache.Set(namespace, name, obj.DeepCopy())
			case watch.Deleted:
				log.Printf("Event: DELETED %s/%s", namespace, name)
				cache.Delete(namespace, name)
				printCache()
			case watch.Error:
				log.Printf("Event: ERROR %s/%s", namespace, name)
			}
		}

		log.Println("Watch connection closed, reconnecting in 5 seconds...")
		watcher.Stop()
		time.Sleep(5 * time.Second)
	}
}
