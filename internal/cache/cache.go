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
	mu      sync.RWMutex
	objects map[string]*secretsstorev1.SecretProviderClass
}

func NewCache() *SecretProviderClassCache {
	return &SecretProviderClassCache{
		objects: make(map[string]*secretsstorev1.SecretProviderClass),
	}
}

func (c *SecretProviderClassCache) Set(namespace, name string, obj *secretsstorev1.SecretProviderClass) {
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

func (c *SecretProviderClassCache) List() []*secretsstorev1.SecretProviderClass {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]*secretsstorev1.SecretProviderClass, 0, len(c.objects))
	for _, obj := range c.objects {
		result = append(result, obj)
	}
	return result
}
