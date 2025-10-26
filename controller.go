package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	resyncInterval = 5 * time.Minute
	retryDelay     = 5 * time.Second

	annotationEnabled        = "azure-keyvault-sync/enabled"
	annotationServiceAccount = "azure-keyvault-sync/service-account"
	annotationEnabledValue   = "true"
)

func isSyncEnabled(obj *unstructured.Unstructured) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}
	return annotations[annotationEnabled] == annotationEnabledValue
}

func getServiceAccount(obj *unstructured.Unstructured) (string, bool) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return "", false
	}
	sa, exists := annotations[annotationServiceAccount]
	return sa, exists
}

func isValidForSync(obj *unstructured.Unstructured) (bool, string) {
	if !isSyncEnabled(obj) {
		return false, ""
	}
	serviceAccount, hasServiceAccount := getServiceAccount(obj)
	if !hasServiceAccount {
		return false, ""
	}
	return true, serviceAccount
}

type Controller struct {
	client     dynamic.Interface
	clientset  kubernetes.Interface
	cache      *SecretProviderClassCache
	tokenCache *TokenCache
	gvr        schema.GroupVersionResource
	ctx        context.Context
}

func NewController(client dynamic.Interface, clientset kubernetes.Interface) *Controller {
	return &Controller{
		client:     client,
		clientset:  clientset,
		cache:      NewCache(),
		tokenCache: NewTokenCache(),
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

func (ctrl *Controller) handleAdded(obj *unstructured.Unstructured) {
	namespace := obj.GetNamespace()
	name := obj.GetName()
	enabled := isSyncEnabled(obj)
	serviceAccount, hasServiceAccount := getServiceAccount(obj)

	if enabled {
		if hasServiceAccount {
			log.Printf("Event: ADDED %s/%s (sync enabled, service-account: %s)", namespace, name, serviceAccount)
			ctrl.cache.Set(namespace, name, obj.DeepCopy())
			ctrl.printCache()
		} else {
			log.Printf("Event: ADDED %s/%s (sync enabled but missing service-account annotation, skipping)", namespace, name)
		}
	} else {
		log.Printf("Event: ADDED %s/%s (sync disabled, skipping)", namespace, name)
	}
}

func (ctrl *Controller) handleModified(obj *unstructured.Unstructured) {
	namespace := obj.GetNamespace()
	name := obj.GetName()
	enabled := isSyncEnabled(obj)
	inCache := ctrl.cache.Has(namespace, name)
	serviceAccount, hasServiceAccount := getServiceAccount(obj)

	if enabled && !inCache {
		if hasServiceAccount {
			log.Printf("Event: MODIFIED %s/%s (annotation enabled, service-account: %s, adding to cache)", namespace, name, serviceAccount)
			ctrl.cache.Set(namespace, name, obj.DeepCopy())
			ctrl.printCache()
		} else {
			log.Printf("Event: MODIFIED %s/%s (annotation enabled but missing service-account annotation, skipping)", namespace, name)
		}
	} else if !enabled && inCache {
		log.Printf("Event: MODIFIED %s/%s (annotation disabled, removing from cache)", namespace, name)
		ctrl.cache.Delete(namespace, name)
		ctrl.printCache()
	} else if enabled && inCache {
		if hasServiceAccount {
			log.Printf("Event: MODIFIED %s/%s (updating, service-account: %s)", namespace, name, serviceAccount)
			ctrl.cache.Set(namespace, name, obj.DeepCopy())
		} else {
			log.Printf("Event: MODIFIED %s/%s (missing service-account annotation, removing from cache)", namespace, name)
			ctrl.cache.Delete(namespace, name)
			ctrl.printCache()
		}
	} else {
		log.Printf("Event: MODIFIED %s/%s (sync disabled, skipping)", namespace, name)
	}
}

func (ctrl *Controller) handleDeleted(namespace, name string, inCache bool) {
	if inCache {
		log.Printf("Event: DELETED %s/%s", namespace, name)
		ctrl.cache.Delete(namespace, name)
		ctrl.printCache()
	} else {
		log.Printf("Event: DELETED %s/%s (not in cache, skipping)", namespace, name)
	}
}

func (ctrl *Controller) handleEvent(event watch.Event) {
	obj, ok := event.Object.(*unstructured.Unstructured)
	if !ok {
		log.Printf("Unexpected object type: %T", event.Object)
		return
	}

	namespace := obj.GetNamespace()
	name := obj.GetName()
	inCache := ctrl.cache.Has(namespace, name)

	switch event.Type {
	case watch.Added:
		ctrl.handleAdded(obj)

	case watch.Modified:
		ctrl.handleModified(obj)

	case watch.Deleted:
		ctrl.handleDeleted(namespace, name, inCache)

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
	validCount := 0
	for _, item := range result.Items {
		if isSyncEnabled(&item) {
			enabledCount++
		}
		if valid, serviceAccount := isValidForSync(&item); valid {
			// Extract clientID from spec
			clientID, err := ExtractClientID(&item)
			if err != nil {
				log.Printf("Warning: %s/%s missing clientID: %v", item.GetNamespace(), item.GetName(), err)
				continue
			}

			// Get token
			token, err := ctrl.tokenCache.GetToken(
				ctrl.ctx,
				ctrl.clientset,
				item.GetNamespace(),
				serviceAccount,
			)
			if err != nil {
				log.Printf("Error getting token for %s/%s: %v", item.GetNamespace(), item.GetName(), err)
				continue
			}

			// Log token acquisition success
			log.Printf("Obtained Kubernetes token for %s/%s, ready for Azure authentication with clientID: %s",
				item.GetNamespace(), serviceAccount, clientID)

			// Debug: Print token snippet for verification
			tokenSnippet := fmt.Sprintf("%s...%s", token[:5], token[len(token)-5:])
			log.Printf("DEBUG: Token for %s/%s: %s", item.GetNamespace(), serviceAccount, tokenSnippet)

			ctrl.cache.Set(item.GetNamespace(), item.GetName(), item.DeepCopy())
			validCount++
		} else if isSyncEnabled(&item) {
			log.Printf("Warning: %s/%s has sync enabled but missing service-account annotation", item.GetNamespace(), item.GetName())
		}
	}

	log.Printf("Resync complete: %d objects in cache (%d total, %d enabled, %d valid)", validCount, len(result.Items), enabledCount, validCount)
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
