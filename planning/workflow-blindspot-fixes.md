# Workflow Blindspot Fixes

## Overview

This document outlines fixes for identified blindspots in the Phase 4 implementation where the controller doesn't properly handle annotation removal, vault deletions, or immediate event-driven reconciliation.

## Identified Blindspots

### Blindspot #1: Annotation Removal Doesn't Clean Up Fields ⚠️

**Current Behavior:**
When a user removes the `azure-keyvault-sync/secret-objects` annotation:

```go
// controller.go:322-325
enableSecretObjects := annotations != nil && annotations[annotationSecretObjects] == annotationEnabledValue

if enableSecretObjects || enableCertObjects {
    // This block doesn't execute when annotation removed
}
// secretObjectsToSync remains nil
```

**In patch function** (update.go:199-205):
```go
if secretObjects != nil {
    // This never executes when annotation removed
    patch = append(patch, ...)
}
```

**Problem:** The `spec.secretObjects` field persists in the SecretProviderClass even though the annotation is gone.

**Expected Behavior:** Field should be cleared entirely when annotation is removed.

---

### Blindspot #2: Vault Deletions Don't Clean Up Objects ⚠️

**Current Behavior:**
When a secret is deleted from Azure Key Vault:

```go
// update.go:101-115 (MergeObjects)
// Add existing objects first (they take precedence)
for _, obj := range existing {
    objectMap[obj.ObjectName] = obj  // Includes deleted secret
}

// Add discovered objects (only if not already present)
for _, obj := range discovered {  // Deleted secret NOT here
    if _, exists := objectMap[obj.ObjectName]; !exists {
        objectMap[obj.ObjectName] = obj
    }
}
```

**Problem:** Deleted vault secret remains in objects array indefinitely. This can cause CSI driver errors ("secret not found").

**Expected Behavior:** Objects array should reflect only current vault contents.

---

### Blindspot #3: Can't Distinguish Manual vs Auto-Generated ⚠️

**Current Behavior:**
Current merge logic preserves ALL existing objects, assuming they might be manual.

**Problem:** No way to distinguish:
- **Manual objects**: Added by human (e.g., pinned version `objectVersion: "abc123"`)
- **Auto-generated objects**: Previously discovered by controller

**Decision:** Use vault as single source of truth. Manual objects must exist in vault.

---

### Blindspot #4: No Immediate Reconciliation on Events ⚠️

**Current Behavior:**
When a SecretProviderClass is added or modified, the event handlers only update the in-memory cache:

```go
func (ctrl *Controller) handleAdded(obj *unstructured.Unstructured) {
    // ...
    ctrl.cache.Set(namespace, name, obj.DeepCopy())  // Only updates cache
    // NO vault sync or SecretProviderClass update here
}

func (ctrl *Controller) handleModified(obj *unstructured.Unstructured) {
    // ...
    ctrl.cache.Set(namespace, name, obj.DeepCopy())  // Only updates cache
    // NO vault sync or SecretProviderClass update here
}
```

**Problem:** Actual vault discovery and SecretProviderClass updates only happen during periodic `syncCache()` calls (every 5 minutes by default).

**Impact:**
- Add `azure-keyvault-sync/enabled: "true"` → Wait up to 5 minutes for sync
- Add `azure-keyvault-sync/secret-objects: "true"` → Wait up to 5 minutes
- Create new SecretProviderClass with annotations → Wait up to 5 minutes
- User experience is poor - appears "broken" until next sync

**Expected Behavior:** Immediate reconciliation when SecretProviderClass is added or annotations are modified.

---

## Architecture Decision: Vault as Source of Truth

**Philosophy Change:** Remove "preserve manual objects" merge logic. Vault contents are the **single source of truth**.

**Benefits:**
- Simple, predictable behavior
- Objects array always matches current vault state
- No need to track manual vs auto-generated
- Deletions from vault are properly reflected

**Trade-off:**
- Manual object customizations (e.g., pinned versions) must be managed through vault versioning or separate SecretProviderClass resources

---

## Implementation Plan

### 1. Simplify Object Generation (Remove Merge Logic)

**File:** `update.go`

**Current Functions to Modify:**
- `MergeObjects()` - Remove merge logic, return only discovered items
- `MergeSecretObjects()` - Remove merge logic, return only generated items

**New Approach:**
```go
// No longer need merge - just use discovered directly
func GenerateObjectsFromVault(secrets []string, certs []string) []VaultObject {
    var objects []VaultObject

    // Add secrets
    for _, secretName := range secrets {
        objects = append(objects, VaultObject{
            ObjectName:    secretName,
            ObjectType:    "secret",
            ObjectVersion: "",
        })
    }

    // Add certificates
    for _, certName := range certs {
        objects = append(objects, VaultObject{
            ObjectName:    certName,
            ObjectType:    "cert",
            ObjectVersion: "",
        })
    }

    // Sort for consistent output
    sort.Slice(objects, func(i, j int) bool {
        return objects[i].ObjectName < objects[j].ObjectName
    })

    return objects
}

// Similar simplification for secretObjects
func GenerateSecretObjectsFromVault(secrets []string, certs []string, enableSecrets bool, enableCerts bool) []SecretObject {
    var secretObjects []SecretObject

    // Add secrets (type: Opaque) if enabled
    if enableSecrets {
        for _, secretName := range secrets {
            secretObjects = append(secretObjects, SecretObject{
                SecretName: secretName,
                Type:       "Opaque",
                Data: []SecretObjectData{
                    {
                        Key:        secretName,
                        ObjectName: secretName,
                    },
                },
            })
        }
    }

    // Add certificates (type: kubernetes.io/tls) if enabled
    if enableCerts {
        for _, certName := range certs {
            secretObjects = append(secretObjects, SecretObject{
                SecretName: certName,
                Type:       "kubernetes.io/tls",
                Data: []SecretObjectData{
                    {
                        Key:        "tls.key",
                        ObjectName: certName,
                    },
                    {
                        Key:        "tls.crt",
                        ObjectName: certName,
                    },
                },
            })
        }
    }

    // Sort for consistent output
    sort.Slice(secretObjects, func(i, j int) bool {
        return secretObjects[i].SecretName < secretObjects[j].SecretName
    })

    return secretObjects
}
```

**Functions to Remove:**
- `ParseExistingObjects()` - No longer needed
- `ParseExistingSecretObjects()` - No longer needed
- `MergeObjects()` - Replaced by simplified generation
- `MergeSecretObjects()` - Replaced by simplified generation

---

### 2. Add Explicit Field Cleanup on Annotation Removal

**File:** `controller.go`

**Current Logic:**
```go
// Only processes secretObjects if annotation enabled
if enableSecretObjects || enableCertObjects {
    // Generate secretObjects
}
```

**New Logic:**
```go
var secretObjectsToSync interface{}

// Check if secretObjects should exist
if enableSecretObjects || enableCertObjects {
    // Generate secretObjects based on vault + annotations
    generatedSecretObjects := GenerateSecretObjectsFromVault(
        discoveredSecrets,
        discoveredCerts,
        enableSecretObjects,
        enableCertObjects,
    )
    secretObjectsToSync = generatedSecretObjects
} else {
    // Annotations disabled - check if field exists and needs removal
    existingSecretObjects, found, _ := unstructured.NestedSlice(item.Object, "spec", "secretObjects")
    if found && len(existingSecretObjects) > 0 {
        // Field exists but should be removed
        secretObjectsToSync = "REMOVE_FIELD" // Special marker for removal
        log.Printf("Annotation disabled for %s/%s, will clear secretObjects field",
            item.GetNamespace(), item.GetName())
    }
}
```

---

### 3. Update Patch Function to Handle Field Removal

**File:** `update.go`

**Modify PatchSecretProviderClass():**

```go
func PatchSecretProviderClass(
    ctx context.Context,
    client dynamic.Interface,
    namespace string,
    name string,
    gvr schema.GroupVersionResource,
    objectsYAML string,
    secretObjects interface{},
    timestamp string,
) error {
    log.Printf("Patching SecretProviderClass %s/%s", namespace, name)

    // Create JSON Patch payload
    patch := []map[string]interface{}{
        {
            "op":    "replace",
            "path":  "/spec/parameters/objects",
            "value": objectsYAML,
        },
        {
            "op":    "add",
            "path":  "/metadata/annotations/azure-keyvault-sync~1last-sync",
            "value": timestamp,
        },
    }

    // Handle secretObjects field
    if secretObjects != nil {
        if secretObjectsStr, ok := secretObjects.(string); ok && secretObjectsStr == "REMOVE_FIELD" {
            // Remove field using JSON Patch "remove" operation
            patch = append(patch, map[string]interface{}{
                "op":   "remove",
                "path": "/spec/secretObjects",
            })
            log.Printf("Removing secretObjects field from %s/%s", namespace, name)
        } else {
            // Replace field with new value
            patch = append(patch, map[string]interface{}{
                "op":    "replace",
                "path":  "/spec/secretObjects",
                "value": secretObjects,
            })
        }
    }

    // Marshal and apply patch
    patchBytes, err := json.Marshal(patch)
    if err != nil {
        return fmt.Errorf("error marshaling patch: %w", err)
    }

    log.Printf("DEBUG: Applying JSON Patch to %s/%s: %s", namespace, name, string(patchBytes))

    _, err = client.Resource(gvr).Namespace(namespace).Patch(
        ctx,
        name,
        types.JSONPatchType,
        patchBytes,
        metav1.PatchOptions{},
    )

    if err != nil {
        return fmt.Errorf("error applying patch: %w", err)
    }

    log.Printf("Successfully patched %s/%s", namespace, name)
    return nil
}
```

---

### 4. Update Controller Integration

**File:** `controller.go`

**Modify syncCache() integration:**

```go
// Generate objects from vault (no merge, vault is source of truth)
discoveredObjects := GenerateObjectsFromVault(discoveredSecrets, discoveredCerts)

// Format as YAML
newObjects, err := FormatObjectsYAML(discoveredObjects)
if err != nil {
    log.Printf("Error formatting objects for %s/%s: %v",
        item.GetNamespace(), item.GetName(), err)
    // Skip update for this resource
    continue
}

// Process secretObjects
var secretObjectsToSync interface{}
annotations := item.GetAnnotations()
enableSecretObjects := annotations != nil && annotations[annotationSecretObjects] == annotationEnabledValue
enableCertObjects := annotations != nil && annotations[annotationCertObjects] == annotationEnabledValue

if enableSecretObjects || enableCertObjects {
    log.Printf("Processing secretObjects for %s/%s (secrets: %v, certs: %v)",
        item.GetNamespace(), item.GetName(), enableSecretObjects, enableCertObjects)

    // Generate secretObjects from vault + annotations
    generatedSecretObjects := GenerateSecretObjectsFromVault(
        discoveredSecrets,
        discoveredCerts,
        enableSecretObjects,
        enableCertObjects,
    )

    secretObjectsToSync = generatedSecretObjects
} else {
    // Check if field exists and needs removal
    existingSecretObjects, found, _ := unstructured.NestedSlice(item.Object, "spec", "secretObjects")
    if found && len(existingSecretObjects) > 0 {
        secretObjectsToSync = "REMOVE_FIELD"
        log.Printf("Annotation disabled for %s/%s, will clear secretObjects field",
            item.GetNamespace(), item.GetName())
    }
}

// Check if update needed
currentObjects, _, _ := unstructured.NestedString(item.Object, "spec", "parameters", "objects")
objectsChanged := DetectChanges(currentObjects, newObjects)

// Check if secretObjects changed
secretObjectsChanged := false
if secretObjectsToSync != nil {
    secretObjectsChanged = true // Field needs update or removal
}

if !objectsChanged && !secretObjectsChanged {
    log.Printf("No changes detected for %s/%s, skipping update",
        item.GetNamespace(), item.GetName())
} else {
    // Patch the resource
    timestamp := time.Now().Format(time.RFC3339)
    log.Printf("Updating %s/%s (objects changed: %v, secretObjects changed: %v)",
        item.GetNamespace(), item.GetName(), objectsChanged, secretObjectsChanged)
    err = PatchSecretProviderClass(
        ctrl.ctx,
        ctrl.client,
        item.GetNamespace(),
        item.GetName(),
        ctrl.gvr,
        newObjects,
        secretObjectsToSync,
        timestamp,
    )
    if err != nil {
        log.Printf("Error patching %s/%s: %v",
            item.GetNamespace(), item.GetName(), err)
        // Continue processing other resources
    } else {
        log.Printf("Successfully updated %s/%s with %d objects (%d secrets, %d certs)",
            item.GetNamespace(), item.GetName(), len(discoveredObjects),
            len(discoveredSecrets), len(discoveredCerts))
    }
}
```

---

### 5. Add Immediate Event-Driven Reconciliation

**File:** `controller.go`

**Create new function:** `reconcileResource(obj *unstructured.Unstructured)`

This extracts the core vault sync logic from `syncCache()` into a reusable function that can be called both from periodic sync AND from event handlers.

**New Function Structure:**
```go
// reconcileResource performs vault discovery and SecretProviderClass update for a single resource
func (ctrl *Controller) reconcileResource(obj *unstructured.Unstructured) error {
    namespace := obj.GetNamespace()
    name := obj.GetName()

    // Extract clientID
    clientID, err := ExtractClientID(obj)
    if err != nil {
        return fmt.Errorf("missing clientID: %w", err)
    }

    // Get service account
    serviceAccount, hasServiceAccount := getServiceAccount(obj)
    if !hasServiceAccount {
        return fmt.Errorf("missing service-account annotation")
    }

    // Get K8s token
    token, err := ctrl.tokenCache.GetToken(ctrl.ctx, ctrl.clientset, namespace, serviceAccount)
    if err != nil {
        return fmt.Errorf("error getting token: %w", err)
    }

    // Extract tenantID
    tenantID, err := ExtractTenantID(obj)
    if err != nil {
        return fmt.Errorf("missing tenantID: %w", err)
    }

    // Get Azure AD token
    azureToken, _, err := ctrl.azureTokenCache.GetToken(ctrl.ctx, namespace, serviceAccount, clientID, tenantID, token)
    if err != nil {
        return fmt.Errorf("error getting Azure token: %w", err)
    }

    // Extract keyvaultName
    keyvaultName, err := ExtractKeyvaultName(obj)
    if err != nil {
        return fmt.Errorf("missing keyvaultName: %w", err)
    }

    // List secrets from vault
    secrets, err := ListSecretsFromVault(ctrl.ctx, azureToken, keyvaultName)
    if err != nil {
        return fmt.Errorf("error listing secrets: %w", err)
    }

    // List certificates from vault
    certificates, err := ListCertificatesFromVault(ctrl.ctx, azureToken, keyvaultName)
    if err != nil {
        return fmt.Errorf("error listing certificates: %w", err)
    }

    // Generate objects from vault
    discoveredObjects := GenerateObjectsFromVault(secrets, certificates)

    // Format as YAML
    newObjects, err := FormatObjectsYAML(discoveredObjects)
    if err != nil {
        return fmt.Errorf("error formatting objects: %w", err)
    }

    // Process secretObjects
    var secretObjectsToSync interface{}
    annotations := obj.GetAnnotations()
    enableSecretObjects := annotations != nil && annotations[annotationSecretObjects] == annotationEnabledValue
    enableCertObjects := annotations != nil && annotations[annotationCertObjects] == annotationEnabledValue

    if enableSecretObjects || enableCertObjects {
        generatedSecretObjects := GenerateSecretObjectsFromVault(
            secrets,
            certificates,
            enableSecretObjects,
            enableCertObjects,
        )
        secretObjectsToSync = generatedSecretObjects
    } else {
        // Check if field exists and needs removal
        existingSecretObjects, found, _ := unstructured.NestedSlice(obj.Object, "spec", "secretObjects")
        if found && len(existingSecretObjects) > 0 {
            secretObjectsToSync = "REMOVE_FIELD"
        }
    }

    // Check if update needed
    currentObjects, _, _ := unstructured.NestedString(obj.Object, "spec", "parameters", "objects")
    objectsChanged := DetectChanges(currentObjects, newObjects)
    secretObjectsChanged := secretObjectsToSync != nil

    if !objectsChanged && !secretObjectsChanged {
        log.Printf("No changes detected for %s/%s, skipping update", namespace, name)
        return nil
    }

    // Patch the resource
    timestamp := time.Now().Format(time.RFC3339)
    err = PatchSecretProviderClass(
        ctrl.ctx,
        ctrl.client,
        namespace,
        name,
        ctrl.gvr,
        newObjects,
        secretObjectsToSync,
        timestamp,
    )
    if err != nil {
        return fmt.Errorf("error patching: %w", err)
    }

    log.Printf("Successfully updated %s/%s with %d objects (%d secrets, %d certs)",
        namespace, name, len(discoveredObjects), len(secrets), len(certificates))

    return nil
}
```

**Modify `syncCache()`:**
Replace the inline vault sync logic with calls to `reconcileResource()`:

```go
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
        if valid, _ := isValidForSync(&item); valid {
            // Reconcile this resource
            err := ctrl.reconcileResource(&item)
            if err != nil {
                log.Printf("Error reconciling %s/%s: %v", item.GetNamespace(), item.GetName(), err)
                continue
            }

            ctrl.cache.Set(item.GetNamespace(), item.GetName(), item.DeepCopy())
            validCount++
        } else if isSyncEnabled(&item) {
            log.Printf("Warning: %s/%s has sync enabled but missing service-account annotation", item.GetNamespace(), item.GetName())
        }
    }

    log.Printf("Resync complete: %d objects in cache (%d total, %d enabled, %d valid)",
        ctrl.cache.Size(), len(result.Items), enabledCount, validCount)
    ctrl.printCache()
}
```

**Modify `handleAdded()`:**
```go
func (ctrl *Controller) handleAdded(obj *unstructured.Unstructured) {
    namespace := obj.GetNamespace()
    name := obj.GetName()

    if valid, serviceAccount := isValidForSync(obj); valid {
        log.Printf("Event: ADDED %s/%s (sync enabled, service-account: %s)", namespace, name, serviceAccount)

        // Immediate reconciliation
        err := ctrl.reconcileResource(obj)
        if err != nil {
            log.Printf("Error reconciling %s/%s: %v", namespace, name, err)
            // Still add to cache even if reconciliation fails
        }

        ctrl.cache.Set(namespace, name, obj.DeepCopy())
        ctrl.printCache()
    } else if isSyncEnabled(obj) {
        log.Printf("Event: ADDED %s/%s (sync enabled but missing service-account annotation, skipping)", namespace, name)
    } else {
        log.Printf("Event: ADDED %s/%s (sync disabled, skipping)", namespace, name)
    }
}
```

**Modify `handleModified()`:**
```go
func (ctrl *Controller) handleModified(obj *unstructured.Unstructured) {
    namespace := obj.GetNamespace()
    name := obj.GetName()
    enabled := isSyncEnabled(obj)
    inCache := ctrl.cache.Has(namespace, name)
    serviceAccount, hasServiceAccount := getServiceAccount(obj)

    if enabled && !inCache {
        if hasServiceAccount {
            log.Printf("Event: MODIFIED %s/%s (annotation enabled, service-account: %s, adding to cache)", namespace, name, serviceAccount)

            // Immediate reconciliation
            err := ctrl.reconcileResource(obj)
            if err != nil {
                log.Printf("Error reconciling %s/%s: %v", namespace, name, err)
            }

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

            // Immediate reconciliation
            err := ctrl.reconcileResource(obj)
            if err != nil {
                log.Printf("Error reconciling %s/%s: %v", namespace, name, err)
            }

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
```

---

## Expected Behavior After Fix

| Scenario | Current (Broken) | After Fix (Working) |
|----------|------------------|---------------------|
| Remove `secret-objects` annotation | Field persists | Field removed entirely via JSON Patch "remove" |
| Delete secret from vault | Object stays in array | Object removed (array reflects vault only) |
| Add secret to vault | Object added (after 5min) | Object added (after 5min periodic sync) |
| Re-add annotation | Uses old field | Regenerates from current vault |
| Manual object with version | Preserved in merge | Lost (must be in vault or separate resource) |
| Add new SecretProviderClass | Wait up to 5 min | Immediate reconciliation |
| Enable sync annotation | Wait up to 5 min | Immediate reconciliation |
| Add secret-objects annotation | Wait up to 5 min | Immediate reconciliation |

---

## Testing Plan

### Unit Testing (Future Enhancement)

**Test Cases:**
1. `GenerateObjectsFromVault()` with empty vault
2. `GenerateObjectsFromVault()` with secrets only
3. `GenerateObjectsFromVault()` with certs only
4. `GenerateObjectsFromVault()` with both
5. `GenerateSecretObjectsFromVault()` with annotations enabled/disabled
6. Patch with "REMOVE_FIELD" marker

### Integration Testing

**Scenario 1: Annotation Removal**
1. Start with SecretProviderClass with `secret-objects: "true"`
2. Verify secretObjects field populated
3. Remove annotation
4. Wait for resync (or restart controller)
5. **Expected:** Field removed from SecretProviderClass
6. **Verify:** `kubectl get secretproviderclass <name> -o yaml | grep secretObjects` returns nothing

**Scenario 2: Vault Deletion**
1. Start with 3 secrets in vault
2. Verify objects array has 3 items
3. Delete 1 secret from vault
4. Wait for resync
5. **Expected:** Objects array has 2 items
6. **Verify:** Deleted secret not in array

**Scenario 3: Vault Addition**
1. Start with 2 secrets in vault
2. Verify objects array has 2 items
3. Add 1 secret to vault
4. Wait for resync
5. **Expected:** Objects array has 3 items
6. **Verify:** New secret appears in array

**Scenario 4: Annotation Toggle**
1. Start with `secret-objects: "true"` and 3 secrets
2. Verify secretObjects has 3 items
3. Remove annotation
4. Verify field cleared
5. Re-add annotation
6. **Expected:** secretObjects regenerated with 3 current vault secrets
7. **Verify:** Field matches vault state

---

## Breaking Changes

**Impact:** This is a breaking change for users relying on manual object management.

**Migration Path:**
1. **Option A:** Move manual objects to vault
   - Any pinned versions or custom objects should be managed in vault
   - Use vault versioning features for pinning

2. **Option B:** Use separate SecretProviderClass
   - Create separate resource for manual objects
   - Don't add `azure-keyvault-sync/enabled` annotation
   - Manually manage that resource

3. **Option C:** Disable controller for specific resources
   - Remove `azure-keyvault-sync/enabled: "true"` annotation
   - Controller ignores resource entirely

**Documentation Update Needed:**
- Update README to explain "vault as source of truth" model
- Add migration guide for existing manual objects
- Document how to disable sync for specific resources

---

## Success Criteria

- [x] Identified blindspots documented (including immediate reconciliation)
- [x] Architecture decision made (vault as source of truth)
- [x] Remove merge logic from update.go
- [x] Add field removal detection in controller.go
- [x] Implement JSON Patch "remove" operation
- [x] Update change detection logic
- [ ] Extract reconciliation logic into reusable function
- [ ] Add immediate reconciliation to handleAdded()
- [ ] Add immediate reconciliation to handleModified()
- [ ] Refactor syncCache() to use reconcileResource()
- [ ] Test annotation removal → field cleared
- [ ] Test vault deletion → object removed
- [ ] Test vault addition → object added (periodic sync)
- [ ] Test annotation toggle → clean rebuild
- [ ] Test immediate reconciliation on ADD event
- [ ] Test immediate reconciliation on MODIFY event
- [ ] Update documentation with breaking changes

---

## Estimated Complexity

**Development Time:** 3-4 hours
- Remove merge functions: 30 minutes ✅
- Add field removal logic: 1 hour ✅
- Controller integration updates: 1 hour ✅
- Extract reconciliation function: 1 hour
- Update event handlers: 30 minutes
- Testing and debugging: 30-60 minutes

**Testing Time:** 1.5 hours
- Annotation removal: 15 minutes
- Vault deletion: 15 minutes
- Vault addition: 15 minutes
- Annotation toggle: 15 minutes
- Immediate reconciliation on ADD: 15 minutes
- Immediate reconciliation on MODIFY: 15 minutes

**Total:** 4.5-5.5 hours for complete implementation and testing

---

## Related Documentation

- Phase 4 Implementation: `planning/secretproviderclass-updates.md`
- JSON Patch RFC: https://tools.ietf.org/html/rfc6902
- JSON Pointer RFC: https://tools.ietf.org/html/rfc6901
