package main

import (
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func cacheKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
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
