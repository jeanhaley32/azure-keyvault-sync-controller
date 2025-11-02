package cache

import (
	"fmt"
	"sync"

	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

func cacheKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

type SecretProviderClassCache struct {
	mu          sync.RWMutex
	objects     map[string]*secretsstorev1.SecretProviderClass
	secretToSPC map[string]string // Maps "namespace/secretName" -> "spcName"
}

func NewCache() *SecretProviderClassCache {
	return &SecretProviderClassCache{
		objects:     make(map[string]*secretsstorev1.SecretProviderClass),
		secretToSPC: make(map[string]string),
	}
}

func (c *SecretProviderClassCache) Set(namespace, name string, obj *secretsstorev1.SecretProviderClass) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := cacheKey(namespace, name)

	// Remove old secret mappings for this SPC if it existed
	if oldSPC, exists := c.objects[key]; exists && oldSPC != nil {
		for _, secretObj := range oldSPC.Spec.SecretObjects {
			secretKey := cacheKey(namespace, secretObj.SecretName)
			delete(c.secretToSPC, secretKey)
		}
	}

	// Store the SPC
	c.objects[key] = obj

	// Add new secret mappings (skip if obj is nil)
	if obj != nil {
		for _, secretObj := range obj.Spec.SecretObjects {
			secretKey := cacheKey(namespace, secretObj.SecretName)
			c.secretToSPC[secretKey] = name
		}
	}
}

func (c *SecretProviderClassCache) Delete(namespace, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := cacheKey(namespace, name)

	// Remove secret mappings for this SPC
	if spc, exists := c.objects[key]; exists && spc != nil {
		for _, secretObj := range spc.Spec.SecretObjects {
			secretKey := cacheKey(namespace, secretObj.SecretName)
			delete(c.secretToSPC, secretKey)
		}
	}

	// Delete the SPC
	delete(c.objects, key)
}

func (c *SecretProviderClassCache) Has(namespace, name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, exists := c.objects[cacheKey(namespace, name)]
	return exists
}

func (c *SecretProviderClassCache) List() []*secretsstorev1.SecretProviderClass {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]*secretsstorev1.SecretProviderClass, 0, len(c.objects))
	for _, obj := range c.objects {
		result = append(result, obj)
	}
	return result
}

// FindSPCForSecret returns the name of the SecretProviderClass that manages
// a secret with the given namespace and name. Returns empty string if not found.
func (c *SecretProviderClassCache) FindSPCForSecret(namespace, secretName string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	secretKey := cacheKey(namespace, secretName)
	return c.secretToSPC[secretKey]
}
