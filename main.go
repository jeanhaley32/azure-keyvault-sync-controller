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

const (
	resyncInterval = 5 * time.Minute
	retryDelay     = 5 * time.Second

	annotationKey   = "azure-keyvault-sync/enabled"
	annotationValue = "true"
)

func cacheKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

func isSyncEnabled(obj *unstructured.Unstructured) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}
	return annotations[annotationKey] == annotationValue
}

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
	c.objects[cacheKey(namespace, name)] = obj
}

func (c *SecretProviderClassCache) Delete(namespace, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.objects, cacheKey(namespace, name))
}

func (c *SecretProviderClassCache) Has(namespace, name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, exists := c.objects[cacheKey(namespace, name)]
	return exists
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

type Controller struct {
	client dynamic.Interface
	cache  *SecretProviderClassCache
	gvr    schema.GroupVersionResource
	ctx    context.Context
}

func NewController(client dynamic.Interface) *Controller {
	return &Controller{
		client: client,
		cache:  NewCache(),
		gvr: schema.GroupVersionResource{
			Group:    "secrets-store.csi.x-k8s.io",
			Version:  "v1",
			Resource: "secretproviderclasses",
		},
		ctx: context.Background(),
	}
}

func (ctrl *Controller) printCache() {
	objects := ctrl.cache.List()
	fmt.Printf("\n--- Current SecretProviderClass objects: %d ---\n", len(objects))
	for _, obj := range objects {
		fmt.Printf("  %s/%s\n", obj.GetNamespace(), obj.GetName())
	}
	fmt.Println("---")
}

func (ctrl *Controller) handleEvent(event watch.Event) {
	obj, ok := event.Object.(*unstructured.Unstructured)
	if !ok {
		log.Printf("Unexpected object type: %T", event.Object)
		return
	}

	namespace := obj.GetNamespace()
	name := obj.GetName()
	enabled := isSyncEnabled(obj)
	inCache := ctrl.cache.Has(namespace, name)

	switch event.Type {
	case watch.Added:
		if enabled {
			log.Printf("Event: ADDED %s/%s (sync enabled)", namespace, name)
			ctrl.cache.Set(namespace, name, obj.DeepCopy())
			ctrl.printCache()
		} else {
			log.Printf("Event: ADDED %s/%s (sync disabled, skipping)", namespace, name)
		}

	case watch.Modified:
		if enabled && !inCache {
			log.Printf("Event: MODIFIED %s/%s (annotation enabled, adding to cache)", namespace, name)
			ctrl.cache.Set(namespace, name, obj.DeepCopy())
			ctrl.printCache()
		} else if !enabled && inCache {
			log.Printf("Event: MODIFIED %s/%s (annotation disabled, removing from cache)", namespace, name)
			ctrl.cache.Delete(namespace, name)
			ctrl.printCache()
		} else if enabled && inCache {
			log.Printf("Event: MODIFIED %s/%s (updating)", namespace, name)
			ctrl.cache.Set(namespace, name, obj.DeepCopy())
		} else {
			log.Printf("Event: MODIFIED %s/%s (sync disabled, skipping)", namespace, name)
		}

	case watch.Deleted:
		if inCache {
			log.Printf("Event: DELETED %s/%s", namespace, name)
			ctrl.cache.Delete(namespace, name)
			ctrl.printCache()
		} else {
			log.Printf("Event: DELETED %s/%s (not in cache, skipping)", namespace, name)
		}

	case watch.Error:
		log.Printf("Event: ERROR %s/%s", namespace, name)
	}
}

func (ctrl *Controller) syncCache() {
	log.Println("Performing full resync")
	result, err := ctrl.client.Resource(ctrl.gvr).Namespace("").List(ctrl.ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("Error listing SecretProviderClasses: %v", err)
		return
	}

	enabledCount := 0
	for _, item := range result.Items {
		if isSyncEnabled(&item) {
			ctrl.cache.Set(item.GetNamespace(), item.GetName(), item.DeepCopy())
			enabledCount++
		}
	}

	log.Printf("Resync complete: %d objects in cache (%d total, %d enabled)", enabledCount, len(result.Items), enabledCount)
}

func (ctrl *Controller) startPeriodicResync() {
	ticker := time.NewTicker(resyncInterval)
	defer ticker.Stop()
	for range ticker.C {
		ctrl.syncCache()
	}
}

func (ctrl *Controller) Run() {
	ctrl.syncCache()
	ctrl.printCache()

	go ctrl.startPeriodicResync()

	log.Println("Watching for events...")

	for {
		watcher, err := ctrl.client.Resource(ctrl.gvr).Namespace("").Watch(ctrl.ctx, metav1.ListOptions{})
		if err != nil {
			log.Printf("Error creating watcher: %v", err)
			time.Sleep(retryDelay)
			continue
		}

		for event := range watcher.ResultChan() {
			ctrl.handleEvent(event)
		}

		log.Println("Watch connection closed, reconnecting in 5 seconds...")
		watcher.Stop()
		time.Sleep(retryDelay)
	}
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

	controller := NewController(dynamicClient)
	controller.Run()
}

