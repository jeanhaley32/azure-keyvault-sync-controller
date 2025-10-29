package cache

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

func TestNewCache(t *testing.T) {
	cache := NewCache()

	assert.NotNil(t, cache)
	assert.NotNil(t, cache.objects)
	assert.Equal(t, 0, len(cache.objects))
}

func TestCacheKey(t *testing.T) {
	tests := []struct {
		namespace string
		name      string
		expected  string
	}{
		{"default", "my-secret", "default/my-secret"},
		{"kube-system", "controller", "kube-system/controller"},
		{"", "no-namespace", "/no-namespace"},
		{"namespace", "", "namespace/"},
		{"", "", "/"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := cacheKey(tt.namespace, tt.name)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCache_SetAndHas(t *testing.T) {
	cache := NewCache()
	obj := createTestObject("default", "test-spc")

	// Initially should not exist
	assert.False(t, cache.Has("default", "test-spc"))

	// Set object
	cache.Set("default", "test-spc", obj)

	// Now should exist
	assert.True(t, cache.Has("default", "test-spc"))

	// Different namespace should not exist
	assert.False(t, cache.Has("other-namespace", "test-spc"))

	// Different name should not exist
	assert.False(t, cache.Has("default", "other-name"))
}

func TestCache_SetOverwrite(t *testing.T) {
	cache := NewCache()
	obj1 := createTestObject("default", "test-spc")
	obj1.Spec.Provider = "provider1"

	obj2 := createTestObject("default", "test-spc")
	obj2.Spec.Provider = "provider2"

	// Set first object
	cache.Set("default", "test-spc", obj1)
	list := cache.List()
	assert.Len(t, list, 1)
	assert.Equal(t, secretsstorev1.Provider("provider1"), list[0].Spec.Provider)

	// Overwrite with second object
	cache.Set("default", "test-spc", obj2)
	list = cache.List()
	assert.Len(t, list, 1)
	assert.Equal(t, secretsstorev1.Provider("provider2"), list[0].Spec.Provider)
}

func TestCache_Delete(t *testing.T) {
	cache := NewCache()
	obj := createTestObject("default", "test-spc")

	// Add object
	cache.Set("default", "test-spc", obj)
	assert.True(t, cache.Has("default", "test-spc"))

	// Delete object
	cache.Delete("default", "test-spc")
	assert.False(t, cache.Has("default", "test-spc"))

	// Delete non-existent object (should not panic)
	cache.Delete("default", "non-existent")
	assert.False(t, cache.Has("default", "non-existent"))
}

func TestCache_List_Empty(t *testing.T) {
	cache := NewCache()

	list := cache.List()
	assert.NotNil(t, list)
	assert.Len(t, list, 0)
}

func TestCache_List_Multiple(t *testing.T) {
	cache := NewCache()

	// Add multiple objects
	cache.Set("default", "spc1", createTestObject("default", "spc1"))
	cache.Set("default", "spc2", createTestObject("default", "spc2"))
	cache.Set("kube-system", "spc3", createTestObject("kube-system", "spc3"))

	list := cache.List()
	assert.Len(t, list, 3)

	// Verify all objects are present (order not guaranteed)
	names := make(map[string]bool)
	for _, obj := range list {
		key := cacheKey(obj.Namespace, obj.Name)
		names[key] = true
	}

	assert.True(t, names["default/spc1"])
	assert.True(t, names["default/spc2"])
	assert.True(t, names["kube-system/spc3"])
}

func TestCache_List_IsCopy(t *testing.T) {
	cache := NewCache()
	obj := createTestObject("default", "test-spc")
	cache.Set("default", "test-spc", obj)

	// Get list
	list1 := cache.List()
	assert.Len(t, list1, 1)

	// Modify the object in the cache
	cache.Set("default", "test-spc2", createTestObject("default", "test-spc2"))

	// Original list should still have only 1 item
	assert.Len(t, list1, 1)

	// New list should have 2 items
	list2 := cache.List()
	assert.Len(t, list2, 2)
}

func TestCache_SameNameDifferentNamespaces(t *testing.T) {
	cache := NewCache()

	// Add objects with same name but different namespaces
	cache.Set("namespace1", "same-name", createTestObject("namespace1", "same-name"))
	cache.Set("namespace2", "same-name", createTestObject("namespace2", "same-name"))

	// Both should exist independently
	assert.True(t, cache.Has("namespace1", "same-name"))
	assert.True(t, cache.Has("namespace2", "same-name"))

	// List should have both
	list := cache.List()
	assert.Len(t, list, 2)

	// Delete one shouldn't affect the other
	cache.Delete("namespace1", "same-name")
	assert.False(t, cache.Has("namespace1", "same-name"))
	assert.True(t, cache.Has("namespace2", "same-name"))
}

func TestCache_ConcurrentReads(t *testing.T) {
	cache := NewCache()

	// Populate cache
	for i := 0; i < 10; i++ {
		name := string(rune('a' + i))
		cache.Set("default", name, createTestObject("default", name))
	}

	// Concurrent reads
	var wg sync.WaitGroup
	numReaders := 50

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Perform multiple read operations
			cache.Has("default", "a")
			cache.List()
			cache.Has("default", "z")
		}()
	}

	wg.Wait()

	// Verify data integrity
	list := cache.List()
	assert.Len(t, list, 10)
}

func TestCache_ConcurrentWrites(t *testing.T) {
	cache := NewCache()
	var wg sync.WaitGroup
	numWriters := 50

	// Concurrent writes to different keys
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := string(rune('a' + (id % 26)))
			namespace := string(rune('A' + (id / 26)))
			cache.Set(namespace, name, createTestObject(namespace, name))
		}(i)
	}

	wg.Wait()

	// All objects should be in cache
	list := cache.List()
	assert.True(t, len(list) > 0 && len(list) <= numWriters)
}

func TestCache_ConcurrentReadWrite(t *testing.T) {
	cache := NewCache()
	var wg sync.WaitGroup

	// Initial population
	for i := 0; i < 10; i++ {
		name := string(rune('a' + i))
		cache.Set("default", name, createTestObject("default", name))
	}

	// Concurrent readers
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cache.Has("default", "a")
				cache.List()
			}
		}()
	}

	// Concurrent writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				name := string(rune('a' + (id % 10)))
				cache.Set("default", name, createTestObject("default", name))
			}
		}(i)
	}

	// Concurrent deleters
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				name := string(rune('a' + (id % 10)))
				cache.Delete("default", name)
			}
		}(i)
	}

	wg.Wait()

	// Cache should still be functional
	cache.Set("test", "verify", createTestObject("test", "verify"))
	assert.True(t, cache.Has("test", "verify"))
}

func TestCache_RaceConditions(t *testing.T) {
	// This test is specifically for running with -race flag
	cache := NewCache()
	var wg sync.WaitGroup

	// Mix of all operations concurrently
	for i := 0; i < 100; i++ {
		wg.Add(4)

		go func(id int) {
			defer wg.Done()
			cache.Set("ns", "name", createTestObject("ns", "name"))
		}(i)

		go func(id int) {
			defer wg.Done()
			cache.Has("ns", "name")
		}(i)

		go func(id int) {
			defer wg.Done()
			cache.List()
		}(i)

		go func(id int) {
			defer wg.Done()
			cache.Delete("ns", "name")
		}(i)
	}

	wg.Wait()

	// If we get here without race detector errors, test passes
	assert.NotNil(t, cache)
}

func TestCache_StressTest(t *testing.T) {
	cache := NewCache()
	var wg sync.WaitGroup
	operations := 1000

	for i := 0; i < operations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			namespace := string(rune('A' + (id % 5)))
			name := string(rune('a' + (id % 20)))

			// Perform random operation
			switch id % 4 {
			case 0:
				cache.Set(namespace, name, createTestObject(namespace, name))
			case 1:
				cache.Has(namespace, name)
			case 2:
				cache.List()
			case 3:
				cache.Delete(namespace, name)
			}
		}(i)
	}

	wg.Wait()

	// Final verification - cache should be operational
	testObj := createTestObject("final", "test")
	cache.Set("final", "test", testObj)
	assert.True(t, cache.Has("final", "test"))
	list := cache.List()
	assert.NotNil(t, list)
}

func TestCache_EmptyNamespace(t *testing.T) {
	cache := NewCache()
	obj := createTestObject("", "test-name")

	cache.Set("", "test-name", obj)
	assert.True(t, cache.Has("", "test-name"))

	list := cache.List()
	assert.Len(t, list, 1)
	assert.Equal(t, "", list[0].Namespace)
	assert.Equal(t, "test-name", list[0].Name)
}

func TestCache_EmptyName(t *testing.T) {
	cache := NewCache()
	obj := createTestObject("test-namespace", "")

	cache.Set("test-namespace", "", obj)
	assert.True(t, cache.Has("test-namespace", ""))

	list := cache.List()
	assert.Len(t, list, 1)
	assert.Equal(t, "test-namespace", list[0].Namespace)
	assert.Equal(t, "", list[0].Name)
}

func TestCache_NilObject(t *testing.T) {
	cache := NewCache()

	// Setting nil should work (might be used for deletion marker or similar)
	cache.Set("default", "test", nil)
	assert.True(t, cache.Has("default", "test"))

	list := cache.List()
	assert.Len(t, list, 1)
	assert.Nil(t, list[0])
}

// Helper function to create test objects
func createTestObject(namespace, name string) *secretsstorev1.SecretProviderClass {
	return &secretsstorev1.SecretProviderClass{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: secretsstorev1.SecretProviderClassSpec{
			Provider: "azure",
		},
	}
}
