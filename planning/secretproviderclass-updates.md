# Phase 4: SecretProviderClass Updates

## Overview

Phase 4 implements automatic population of the `objects` array in SecretProviderClass resources based on discovered vault contents from Phase 3. This eliminates the need for manual maintenance of secret lists in manifests.

## Research Findings

### Kubernetes Patch Strategies

Kubernetes supports three patching approaches:

1. **Strategic Merge Patch** - Kubernetes-specific, best for built-in resources
   - **Limitation**: Not supported for Custom Resource Definitions (CRDs)

2. **Merge Patch (RFC 7386)** - Simple JSON merge
   - **Limitation**: Cannot remove fields, limited control

3. **JSON Patch (RFC 6902)** - Explicit operations
   - **Operations**: add, remove, replace, test, copy, move
   - **Recommendation**: Use this for SecretProviderClass

**Decision**: Use **JSON Patch** (`types.JSONPatchType`) because:
- CRDs don't support Strategic Merge Patch
- Explicit operations provide clear control
- Can handle both value replacement and annotation additions
- Industry standard (RFC 6902)

### Dynamic Client Patch Method

```go
import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/types"
)

// Patch method signature
client.Resource(gvr).Namespace(namespace).Patch(
    ctx context.Context,
    name string,
    pt types.PatchType,  // types.JSONPatchType
    data []byte,         // JSON-encoded patch
    opts metav1.PatchOptions,
    subresources ...string,
) (*unstructured.Unstructured, error)
```

**Key Points:**
- Works with `unstructured.Unstructured` objects
- Requires `schema.GroupVersionResource` (we already have this)
- Patch payload must be JSON-encoded
- Returns updated resource or error

### SecretProviderClass Objects Format

The `spec.parameters.objects` field contains a YAML string with this structure:

```yaml
spec:
  provider: azure
  parameters:
    keyvaultName: staging-flow-vault
    clientID: "..."
    tenantId: "..."
    objects: |
      array:
        - |
          objectName: secret-name
          objectType: secret
          objectVersion: ""
        - |
          objectName: another-secret
          objectType: secret
          objectVersion: ""
        - |
          objectName: cert-name
          objectType: cert
          objectVersion: ""
```

**Format Requirements:**
- YAML string (not structured YAML)
- Top-level `array:` key
- Each item prefixed with `- |` (block scalar)
- Required fields: `objectName`, `objectType`
- Optional field: `objectVersion` (empty = latest)
- Valid `objectType` values: `secret`, `cert`, `key`

**Azure Provider Specifics:**
- This format is unique to the Azure Key Vault provider
- Other providers may use different formats
- The pipe character (`|`) indicates a YAML block scalar

### JSON Patch Path Escaping

JSON Patch uses JSON Pointer (RFC 6901) for paths, which requires escaping:

- `/` → `~1`
- `~` → `~0`

**Example**:
```go
// Annotation key: "azure-keyvault-sync/last-sync"
// JSON Patch path: "/metadata/annotations/azure-keyvault-sync~1last-sync"

patch := map[string]interface{}{
    "op":    "add",
    "path":  "/metadata/annotations/azure-keyvault-sync~1last-sync",
    "value": "2025-10-26T18:30:00Z",
}
```

## Architecture Decisions

### 1. Preserve Manual Objects

**Challenge**: Some objects may be manually defined in SecretProviderClass for specific reasons (e.g., referencing specific versions).

**Solution**: Merge strategy that preserves existing objects:
1. Parse current `objects` string to extract existing items
2. Add discovered items that aren't already present
3. Use `objectName` as unique identifier for deduplication

**Benefits**:
- Respects operator intent for manual definitions
- Idempotent updates (re-running doesn't create duplicates)
- Safe to run repeatedly

### 2. Change Detection

**Challenge**: Avoid unnecessary updates that trigger reconciliation loops.

**Solution**: Compare current and new objects YAML before patching:
```go
if currentObjects == newObjects {
    log.Printf("No changes detected, skipping update")
    return nil
}
```

**Benefits**:
- Reduces API server load
- Prevents unnecessary pod restarts (if configured with restart policy)
- Cleaner audit logs

### 3. Last-Sync Annotation

**Purpose**: Track when controller last synchronized vault contents.

**Annotation Key**: `azure-keyvault-sync/last-sync`

**Value Format**: RFC3339 timestamp (e.g., `2025-10-26T18:30:00Z`)

**Use Cases**:
- Debugging: When was this resource last synced?
- Monitoring: Alert if sync hasn't happened in X time
- Troubleshooting: Check sync timestamp vs vault changes

### 4. Error Handling Strategy

**Best-Effort Approach**: Patch failures don't stop entire sync process.

**Error Scenarios**:

1. **Parse Errors** (malformed existing objects):
   - Log warning with details
   - Skip update for this resource
   - Continue processing other resources

2. **Patch Failures** (API errors):
   - Log error with full context
   - Continue processing other resources
   - Don't mark as successful in cache

3. **Permission Errors** (RBAC):
   - Log clearly indicating RBAC issue
   - Provide helpful message about required permissions
   - Don't retry (requires manual RBAC fix)

**Philosophy**: One resource failure shouldn't break entire controller sync.

### 5. YAML Formatting

**Challenge**: Azure provider expects specific YAML format with block scalars.

**Solution**: Use `gopkg.in/yaml.v3` with custom marshaling:
```go
// Target format:
// array:
//   - |
//     objectName: secret-1
//     objectType: secret
```

**Implementation Approach**:
- Marshal VaultObject structs to YAML
- Add required block scalar indicators (`|`)
- Ensure proper indentation (2 spaces)
- Validate output format

## Implementation Plan

### File Structure

```
operator/
├── update.go          # New: SecretProviderClass update logic
├── controller.go      # Modified: Integrate update calls
├── go.mod            # Modified: Add yaml.v3 dependency
└── planning/
    └── secretproviderclass-updates.md  # This file
```

### Data Structures

```go
// VaultObject represents a single object in the objects array
type VaultObject struct {
    ObjectName    string `yaml:"objectName"`
    ObjectType    string `yaml:"objectType"`  // "secret" or "cert"
    ObjectVersion string `yaml:"objectVersion,omitempty"`
}

// ObjectsSpec represents the full objects structure
type ObjectsSpec struct {
    Array []VaultObject `yaml:"array"`
}
```

### Function Breakdown

#### 1. `ParseExistingObjects(obj *unstructured.Unstructured) ([]VaultObject, error)`

**Purpose**: Extract and parse current objects from SecretProviderClass

**Steps**:
1. Get `spec.parameters.objects` string using `unstructured.NestedString`
2. If empty, return empty slice (not an error)
3. Unmarshal YAML to `ObjectsSpec` struct
4. Return `ObjectsSpec.Array` slice
5. Handle YAML parse errors gracefully

**Error Handling**:
- Empty/missing objects field → return `[]VaultObject{}, nil`
- Invalid YAML → return error with details

#### 2. `GenerateObjectsArray(secrets []string, certs []string) []VaultObject`

**Purpose**: Convert discovered vault items to VaultObject structs

**Steps**:
1. Create empty slice
2. For each secret: append `VaultObject{ObjectName: name, ObjectType: "secret"}`
3. For each cert: append `VaultObject{ObjectName: name, ObjectType: "cert"}`
4. Return combined slice

**Note**: ObjectVersion left empty (defaults to latest)

#### 3. `MergeObjects(existing []VaultObject, discovered []VaultObject) []VaultObject`

**Purpose**: Combine existing and discovered objects without duplicates

**Steps**:
1. Create map for deduplication: `map[string]VaultObject`
2. Add all existing objects to map (key = objectName)
3. Add all discovered objects to map (only if key not present)
4. Convert map values back to slice
5. Sort by objectName for consistent output

**Deduplication Logic**:
- Use `objectName` as unique identifier
- Existing objects take precedence (preserve manual definitions)
- Discovered objects added only if new

#### 4. `FormatObjectsYAML(objects []VaultObject) (string, error)`

**Purpose**: Convert VaultObject slice to Azure provider YAML format

**Steps**:
1. Create `ObjectsSpec{Array: objects}`
2. Marshal to YAML using `yaml.v3`
3. Ensure proper formatting with block scalars
4. Return YAML string

**Format Validation**:
- Verify `array:` key present
- Check indentation (2 spaces)
- Ensure block scalar indicators present

#### 5. `DetectChanges(current string, new string) bool`

**Purpose**: Determine if objects have changed

**Steps**:
1. Normalize whitespace in both strings
2. Compare normalized strings
3. Return true if different

**Note**: Simple string comparison sufficient for our use case

#### 6. `PatchSecretProviderClass(ctx, client, namespace, name, objectsYAML, timestamp string) error`

**Purpose**: Apply JSON Patch to SecretProviderClass

**Steps**:
1. Create JSON Patch payload (array of operations)
2. Operation 1: Replace `/spec/parameters/objects` with new YAML
3. Operation 2: Add/replace annotation with timestamp
4. Marshal patch to JSON
5. Call `client.Resource(gvr).Namespace(ns).Patch()`
6. Handle response and errors

**JSON Patch Structure**:
```go
patch := []map[string]interface{}{
    {
        "op":    "replace",
        "path":  "/spec/parameters/objects",
        "value": objectsYAML,
    },
    {
        "op":    "add",  // "add" works for both create and update
        "path":  "/metadata/annotations/azure-keyvault-sync~1last-sync",
        "value": timestamp,
    },
}
```

### Integration into Controller

**Location**: In `syncCache()` function, after vault listing

**Pseudocode**:
```go
// After listing secrets and certificates...

// Parse existing objects
existing, err := ParseExistingObjects(&item)
if err != nil {
    log.Printf("Error parsing existing objects for %s/%s: %v", namespace, name, err)
    // Continue with empty existing objects
    existing = []VaultObject{}
}

// Generate discovered objects
discovered := GenerateObjectsArray(secrets, certificates)

// Merge
merged := MergeObjects(existing, discovered)

// Format as YAML
newObjects, err := FormatObjectsYAML(merged)
if err != nil {
    log.Printf("Error formatting objects for %s/%s: %v", namespace, name, err)
    continue  // Skip this resource
}

// Check if update needed
currentObjects, _, _ := unstructured.NestedString(item.Object, "spec", "parameters", "objects")
if !DetectChanges(currentObjects, newObjects) {
    log.Printf("No changes detected for %s/%s, skipping update", namespace, name)
    continue
}

// Patch the resource
timestamp := time.Now().Format(time.RFC3339)
err = PatchSecretProviderClass(
    ctrl.ctx,
    ctrl.client,
    item.GetNamespace(),
    item.GetName(),
    ctrl.gvr,
    newObjects,
    timestamp,
)
if err != nil {
    log.Printf("Error patching %s/%s: %v", namespace, name, err)
    // Continue processing other resources
} else {
    log.Printf("Successfully updated %s/%s with %d objects (%d secrets, %d certs)",
        namespace, name, len(merged), len(secrets), len(certificates))
}
```

## Testing Strategy

### Unit Tests (Future Enhancement)

**ParseExistingObjects**:
- Test with empty objects field
- Test with valid YAML
- Test with invalid YAML
- Test with missing array key

**GenerateObjectsArray**:
- Test with empty secrets and certs
- Test with only secrets
- Test with only certs
- Test with both

**MergeObjects**:
- Test with no existing objects
- Test with no discovered objects
- Test with overlapping objects (deduplication)
- Test with unique objects

**FormatObjectsYAML**:
- Validate YAML structure
- Check for proper indentation
- Verify block scalar indicators

### Integration Tests

**Scenario 1: Empty Existing Objects**
- Initial state: SecretProviderClass with empty/missing objects
- Expected: Populated with discovered secrets and certs
- Verify: All discovered items present in objects

**Scenario 2: Preserve Manual Objects**
- Initial state: SecretProviderClass with manually-defined object
- Expected: Manual object preserved + discovered items added
- Verify: Manual object still present, discovered items added

**Scenario 3: No Changes**
- Initial state: Objects already match discovered contents
- Expected: No patch applied (change detection)
- Verify: Log shows "No changes detected"

**Scenario 4: Vault Changes**
- Initial state: Objects reflect old vault state
- Action: Add secret to vault, trigger resync
- Expected: New secret appears in objects
- Verify: Patch applied, new object present

**Scenario 5: Permission Denied**
- Initial state: Controller lacks update/patch permission
- Expected: Error logged, processing continues
- Verify: Other resources still processed

### Manual Testing Plan

1. **Deploy to staging** with RBAC permissions
2. **Monitor logs** for first sync:
   ```
   Successfully updated default/flow-staging-secrets with 3 objects (3 secrets, 0 certs)
   ```
3. **Verify SecretProviderClass** updated:
   ```bash
   kubectl get secretproviderclass flow-staging-secrets -o yaml
   ```
4. **Check annotation** added:
   ```yaml
   metadata:
     annotations:
       azure-keyvault-sync/last-sync: "2025-10-26T18:30:00Z"
   ```
5. **Add test secret** to vault:
   ```bash
   az keyvault secret set --vault-name staging-flow-vault --name test-new-secret --value "test"
   ```
6. **Wait for resync** (5 minutes) or restart controller
7. **Verify new secret** appears in objects
8. **Check logs** for update message

## Example Transformation

### Before Update

```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: flow-staging-secrets
  namespace: default
  annotations:
    azure-keyvault-sync/enabled: "true"
    azure-keyvault-sync/service-account: "aks-staging-flow"
spec:
  provider: azure
  parameters:
    keyvaultName: staging-flow-vault
    clientID: "aac3d546-358f-4e74-94e5-bb4c472d7cc0"
    tenantId: "8b83ab42-3e3f-422d-85ca-fe2d40c51e35"
    objects: ""  # Empty or missing
```

### After Update (Auto-Populated)

```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: flow-staging-secrets
  namespace: default
  annotations:
    azure-keyvault-sync/enabled: "true"
    azure-keyvault-sync/service-account: "aks-staging-flow"
    azure-keyvault-sync/last-sync: "2025-10-26T18:30:00Z"  # Added by controller
spec:
  provider: azure
  parameters:
    keyvaultName: staging-flow-vault
    clientID: "aac3d546-358f-4e74-94e5-bb4c472d7cc0"
    tenantId: "8b83ab42-3e3f-422d-85ca-fe2d40c51e35"
    objects: |
      array:
        - |
          objectName: azure-flow-api-secret
          objectType: secret
          objectVersion: ""
        - |
          objectName: flow-api-secret
          objectType: secret
          objectVersion: ""
        - |
          objectName: testing-secret
          objectType: secret
          objectVersion: ""
```

## Dependencies

**New Dependency**:
- `gopkg.in/yaml.v3` - YAML parsing and marshaling

**Existing Dependencies** (already in project):
- `k8s.io/client-go/dynamic` - Dynamic client
- `k8s.io/apimachinery/pkg/types` - Patch types
- `k8s.io/apimachinery/pkg/apis/meta/v1/unstructured` - Unstructured objects

## RBAC Requirements

**Additional Permissions Needed**:
```yaml
- apiGroups: ["secrets-store.csi.x-k8s.io"]
  resources: ["secretproviderclasses"]
  verbs: ["update", "patch"]  # Add these verbs
```

**Current Permissions**:
- Already have: `get`, `list`, `watch`
- Need to add: `update`, `patch`

**Update `deploy/rbac.yaml`** to include these verbs.

## Success Criteria

- [x] Research Kubernetes patching strategies
- [ ] Parse existing objects from SecretProviderClass
- [ ] Generate objects array from discovered secrets/certs
- [ ] Merge existing and discovered objects without duplicates
- [ ] Format objects as Azure provider YAML string
- [ ] Detect changes to avoid unnecessary updates
- [ ] Create and apply JSON Patch payload
- [ ] Add last-sync annotation with timestamp
- [ ] Handle errors gracefully (best-effort)
- [ ] Test with staging SecretProviderClass
- [ ] Verify no duplicates after multiple syncs
- [ ] Verify manual objects preserved
- [ ] All updates logged clearly

## Risks and Mitigation

**Risk: Malformed YAML in Existing Objects**
- **Probability**: Low (usually auto-generated)
- **Impact**: Medium (parsing failure)
- **Mitigation**: Handle parse errors gracefully, continue with empty existing objects

**Risk: Patch Conflicts**
- **Probability**: Low (single controller modifying)
- **Impact**: Low (next sync will retry)
- **Mitigation**: Log error, continue processing other resources

**Risk: RBAC Permissions Missing**
- **Probability**: Medium (easy to forget update/patch)
- **Impact**: High (no updates work)
- **Mitigation**: Clear error messages, documentation of required permissions

**Risk: Duplicate Objects Created**
- **Probability**: Medium (merge logic bug)
- **Impact**: Medium (pod mount errors possible)
- **Mitigation**: Thorough testing of merge logic, use objectName as unique key

**Risk: Performance with Large Objects Arrays**
- **Probability**: Low (most vaults < 100 items)
- **Impact**: Low (YAML string still small)
- **Mitigation**: Current approach sufficient, optimize if needed

## Looking Ahead to Phase 5

**What Phase 4 Provides**:
- Automatic SecretProviderClass updates
- Preserved manual configurations
- Last-sync tracking
- Change detection

**What Phase 5 Needs** (Production Readiness):
- Prometheus metrics for tracking updates
- Structured logging with log levels
- Health check endpoints
- Configuration management via ConfigMap
- Security hardening (non-root, read-only filesystem)
- Comprehensive test coverage
- Retry logic with exponential backoff
- Rate limiting for API calls

## Estimated Complexity

**Development Time**: 3-4 hours
- Data structures and parsing: 1 hour
- YAML formatting: 1 hour
- Patch implementation: 1 hour
- Controller integration: 30 minutes
- Testing and debugging: 1 hour

**Testing Time**: 1-2 hours
- Local testing: 30 minutes
- Staging deployment: 30 minutes
- Real SecretProviderClass testing: 1 hour

**Total**: 4-6 hours for complete Phase 4 implementation and testing

---

## Phase 4.1: Automatic secretObjects Generation

### Overview

Add functionality to automatically populate the `secretObjects` field in SecretProviderClass, which instructs the CSI driver to create Kubernetes Secret resources from vault secrets and certificates.

### New Annotations

1. **`azure-keyvault-sync/secret-objects: "true"`**
   - Enables automatic Kubernetes Secret creation for vault secrets
   - Each vault secret becomes a Kubernetes Secret (type: Opaque)

2. **`azure-keyvault-sync/cert-objects: "true"`**
   - Enables automatic Kubernetes Secret creation for vault certificates
   - Each vault certificate becomes a Kubernetes TLS Secret (type: kubernetes.io/tls)

### secretObjects Structure

**For Secrets (type: Opaque):**
```yaml
secretObjects:
  - secretName: flow-api-secret
    type: Opaque
    data:
      - key: flow-api-secret
        objectName: flow-api-secret
```

**For Certificates (type: kubernetes.io/tls):**
```yaml
secretObjects:
  - secretName: my-cert
    type: kubernetes.io/tls
    data:
      - key: tls.key
        objectName: my-cert
      - key: tls.crt
        objectName: my-cert
```

### Key Mapping Strategy

**For Secrets (Opaque):**
- `secretName`: same as vault secret name
- `key`: same as vault secret name  
- `objectName`: same as vault secret name

**For Certificates (TLS):**
- `secretName`: same as vault cert name
- `keys`: `tls.key` and `tls.crt` (Kubernetes TLS secret standard)
- `objectName`: same as vault cert name (referenced twice for key and cert)

### Implementation Plan

#### 1. Add New Structs to update.go

```go
type SecretObject struct {
    SecretName string              `yaml:"secretName"`
    Type       string              `yaml:"type"`
    Data       []SecretObjectData  `yaml:"data"`
}

type SecretObjectData struct {
    Key        string `yaml:"key"`
    ObjectName string `yaml:"objectName"`
}
```

#### 2. Add New Functions to update.go

**ParseExistingSecretObjects(obj *unstructured.Unstructured) ([]SecretObject, error)**
- Extract `spec.secretObjects` array
- Parse YAML to SecretObject structs
- Handle missing/empty field (return empty slice)
- Return parsed slice or error

**GenerateSecretObjects(secrets []string, certs []string, enableSecrets bool, enableCerts bool) []SecretObject**
- Create empty slice
- If enableSecrets: for each secret, create Opaque SecretObject with single data entry
- If enableCerts: for each cert, create TLS SecretObject with tls.key and tls.crt entries
- Return combined slice

**MergeSecretObjects(existing []SecretObject, generated []SecretObject) []SecretObject**
- Use secretName as unique key
- Add existing secretObjects to map
- Add generated secretObjects if not already present (preserve manual ones)
- Convert map to sorted slice
- Return merged slice

**FormatSecretObjectsYAML(objects []SecretObject) (interface{}, error)**
- If empty, return nil (omit field entirely)
- Return objects slice for YAML marshaling
- Let YAML marshaler handle formatting

#### 3. Modify PatchSecretProviderClass Function

Update signature to accept optional secretObjects:
```go
func PatchSecretProviderClass(
    ctx context.Context,
    client dynamic.Interface,
    namespace string,
    name string,
    gvr schema.GroupVersionResource,
    objectsYAML string,
    secretObjects interface{},  // NEW: can be nil or []SecretObject
    timestamp string,
) error
```

Add secretObjects to patch operations if not nil:
```go
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

// Add secretObjects patch if provided
if secretObjects != nil {
    patch = append(patch, map[string]interface{}{
        "op":    "replace",
        "path":  "/spec/secretObjects",
        "value": secretObjects,
    })
}
```

#### 4. Integrate into controller.go

Add after objects update logic:

```go
// Check if secretObjects sync enabled
secretObjectsEnabled := false
certObjectsEnabled := false

if annotations := item.GetAnnotations(); annotations != nil {
    secretObjectsEnabled = annotations["azure-keyvault-sync/secret-objects"] == "true"
    certObjectsEnabled = annotations["azure-keyvault-sync/cert-objects"] == "true"
}

var secretObjectsToUpdate interface{} = nil

if secretObjectsEnabled || certObjectsEnabled {
    log.Printf("SecretObjects sync enabled for %s/%s (secrets=%v, certs=%v)",
        item.GetNamespace(), item.GetName(), secretObjectsEnabled, certObjectsEnabled)
    
    // Parse existing secretObjects
    existingSecretObjects, err := ParseExistingSecretObjects(&item)
    if err != nil {
        log.Printf("Error parsing existing secretObjects: %v", err)
        existingSecretObjects = []SecretObject{}
    }
    
    // Generate secretObjects
    generatedSecretObjects := GenerateSecretObjects(
        discoveredSecrets,
        discoveredCerts,
        secretObjectsEnabled,
        certObjectsEnabled,
    )
    
    // Merge
    mergedSecretObjects := MergeSecretObjects(existingSecretObjects, generatedSecretObjects)
    
    // Format for YAML
    secretObjectsToUpdate, err = FormatSecretObjectsYAML(mergedSecretObjects)
    if err != nil {
        log.Printf("Error formatting secretObjects: %v", err)
        secretObjectsToUpdate = nil
    } else {
        log.Printf("Generated %d secretObjects for %s/%s",
            len(mergedSecretObjects), item.GetNamespace(), item.GetName())
    }
}

// Update call to include secretObjects
err = PatchSecretProviderClass(
    ctrl.ctx,
    ctrl.client,
    item.GetNamespace(),
    item.GetName(),
    ctrl.gvr,
    newObjects,
    secretObjectsToUpdate,  // NEW parameter
    timestamp,
)
```

### Example Transformation

**Input:**
```yaml
annotations:
  azure-keyvault-sync/enabled: "true"
  azure-keyvault-sync/service-account: "aks-staging-flow"
  azure-keyvault-sync/secret-objects: "true"
  azure-keyvault-sync/cert-objects: "true"
```

**Discovered:** 3 secrets (azure-flow-api-secret, flow-api-secret, testing-secret), 0 certificates

**Output:**
```yaml
spec:
  parameters:
    objects: |
      array:
        - objectName: azure-flow-api-secret
          objectType: secret
        - objectName: flow-api-secret
          objectType: secret
        - objectName: testing-secret
          objectType: secret
  secretObjects:
    - secretName: azure-flow-api-secret
      type: Opaque
      data:
        - key: azure-flow-api-secret
          objectName: azure-flow-api-secret
    - secretName: flow-api-secret
      type: Opaque
      data:
        - key: flow-api-secret
          objectName: flow-api-secret
    - secretName: testing-secret
      type: Opaque
      data:
        - key: testing-secret
          objectName: testing-secret
```

### Testing Plan

1. **Add annotation to staging SecretProviderClass:**
   ```bash
   kubectl annotate secretproviderclass flow-staging-secrets \
     azure-keyvault-sync/secret-objects=true -n default
   ```

2. **Restart controller and check logs:**
   - Verify "SecretObjects sync enabled" message
   - Check "Generated X secretObjects" message

3. **Verify SecretProviderClass updated:**
   ```bash
   kubectl get secretproviderclass flow-staging-secrets -n default -o jsonpath='{.spec.secretObjects}' | jq .
   ```

4. **Wait for CSI driver to create Kubernetes Secrets:**
   ```bash
   kubectl get secrets -n default | grep flow
   ```

5. **Test certificates:**
   - Add `azure-keyvault-sync/cert-objects: "true"` annotation
   - Add certificate to vault
   - Verify TLS secret created

6. **Test merge behavior:**
   - Manually add a secretObject
   - Restart controller
   - Verify manual secretObject preserved + discovered ones added

### Success Criteria

- [ ] Parse existing secretObjects from SecretProviderClass
- [ ] Generate secretObjects for secrets when annotation enabled
- [ ] Generate secretObjects for certificates when annotation enabled
- [ ] Merge existing and generated secretObjects without duplicates
- [ ] Format as proper YAML structure
- [ ] Apply patch with secretObjects included
- [ ] Test with secrets-only annotation
- [ ] Test with certs-only annotation
- [ ] Test with both annotations
- [ ] Verify Kubernetes Secrets created by CSI driver
- [ ] Verify manual secretObjects preserved

### Estimated Complexity

**Development Time:** 2-3 hours
- New structs and parsing: 30 minutes
- Generate functions: 1 hour
- Merge logic: 30 minutes
- Controller integration: 30 minutes
- Testing and debugging: 30-60 minutes

**Testing Time:** 1 hour
- Annotation testing: 30 minutes
- CSI driver secret verification: 30 minutes

**Total:** 3-4 hours for complete implementation and testing
