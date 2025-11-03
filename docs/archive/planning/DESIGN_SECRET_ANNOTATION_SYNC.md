# Secret Annotation Synchronization Design

> **Status**: Design Proposal
> **Created**: 2025-10-31
> **Authors**: Architecture Team
> **Related Components**: azure-keyvault-sync-controller, Azure Secrets Store CSI Driver

---

## Table of Contents

1. [Problem Statement](#problem-statement)
2. [Goals and Non-Goals](#goals-and-non-goals)
3. [Architecture Overview](#architecture-overview)
4. [Detailed Design](#detailed-design)
5. [Data Flow](#data-flow)
6. [Configuration Examples](#configuration-examples)
7. [Implementation Plan](#implementation-plan)
8. [Alternative: Zero-Sync-Time Configuration](#alternative-zero-sync-time-configuration)
9. [Testing Strategy](#testing-strategy)
10. [Operational Considerations](#operational-considerations)
11. [Architectural Decision Analysis](#architectural-decision-analysis)
12. [Future Enhancements](#future-enhancements)

---

## Problem Statement

### Current Situation

The Azure Secrets Store CSI Driver synchronizes secret **content** from Azure Key Vault to Kubernetes, but does not provide a mechanism to propagate custom **metadata** (annotations/labels) from Azure Key Vault to the Kubernetes `Secret` objects it creates.

This limitation prevents using ecosystem tools like `kubernetes-reflector` that rely on specific annotations (e.g., `reflector.v1.k8s.emberstack.com/reflection-allowed: "true"`) to enable cross-namespace secret sharing.

### The Gap

```
┌──────────────────────┐
│  Azure Key Vault     │  ← Source of truth for content AND metadata (tags)
│  - Secrets           │
│  - Tags (metadata)   │
└──────────────────────┘
           ↓
┌──────────────────────┐
│  CSI Driver          │  ← Syncs content only
└──────────────────────┘
           ↓
┌──────────────────────┐
│  Kubernetes Secret   │  ← Has content, but NO metadata
│  - Data: ✅          │
│  - Annotations: ❌   │
└──────────────────────┘
```

### Business Impact

- Cannot leverage Azure Key Vault as single source of truth for secret metadata
- Manual annotation management is error-prone and doesn't scale
- Ecosystem tools (reflector, external-secrets, etc.) cannot be used effectively
- Violates principle of declarative infrastructure

---

## Goals and Non-Goals

### Goals ✅

1. **Single Source of Truth**: Use Azure Key Vault tags as the authoritative source for secret metadata
2. **Per-Secret Metadata**: Support different annotations for each secret in the vault
3. **Kubernetes-Native**: Leverage standard Kubernetes patterns and APIs
4. **Separation of Concerns**: Clear boundaries between components
5. **Eventual Consistency**: Guarantee annotations will be applied, but allow for asynchronous reconciliation
6. **Maintainability**: Simple, testable, and debuggable implementation

### Non-Goals ❌

1. **Replacing CSI Driver**: We use the existing Azure CSI Driver for content synchronization
2. **Managing Other Controllers**: We don't control or coordinate with controllers that consume annotations (e.g., `kubernetes-reflector`)
3. **Real-Time Guarantees**: We accept eventual consistency (seconds of delay acceptable)
4. **Annotation Validation**: We don't validate what annotations mean or how they're used
5. **Secret Content Management**: We don't modify or validate secret values

### Explicit Scope

**Our Controller's Responsibility:**
- Read secret metadata (tags) from Azure Key Vault
- Embed per-secret annotations in `SecretProviderClass` metadata
- Watch for `Secret` objects created by CSI Driver
- Patch those `Secret` objects with appropriate annotations
- Maintain annotation synchronization over time

**Not Our Responsibility:**
- What other controllers do with those annotations
- Timing of downstream processing (reflection, replication, etc.)
- Pod mounting or CSI Driver lifecycle
- Secret content validation or transformation

---

## Architecture Overview

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     Azure Key Vault                              │
│  Secret: my-db-password                                          │
│    Tags:                                                         │
│      k8s-annotation.reflector/allowed: "true"                    │
│      k8s-annotation.owner: "team-alpha"                          │
└─────────────────────────────────────────────────────────────────┘
                               ↓
                   (Controller reads tags)
                               ↓
┌─────────────────────────────────────────────────────────────────┐
│              SecretProviderClass (Intermediate State)            │
│  metadata:                                                       │
│    annotations:                                                  │
│      secret-metadata.azure-keyvault-sync.io/my-db-password.reflector/allowed: "true" │
│      secret-metadata.azure-keyvault-sync.io/my-db-password.owner: "team-alpha"      │
│  spec:                                                           │
│    secretObjects:                                                │
│      - secretName: my-db-password-secret                         │
│        data:                                                     │
│          - objectName: my-db-password                            │
│            key: password                                         │
└─────────────────────────────────────────────────────────────────┘
                               ↓
                    (CSI Driver creates Secret)
                               ↓
┌─────────────────────────────────────────────────────────────────┐
│              Kubernetes Secret (No annotations yet)              │
│  apiVersion: v1                                                  │
│  kind: Secret                                                    │
│  metadata:                                                       │
│    name: my-db-password-secret                                   │
│  data:                                                           │
│    password: <base64-encoded-value>                              │
└─────────────────────────────────────────────────────────────────┘
                               ↓
                  (Controller patches annotations)
                               ↓
┌─────────────────────────────────────────────────────────────────┐
│         Kubernetes Secret (With correct annotations)             │
│  apiVersion: v1                                                  │
│  kind: Secret                                                    │
│  metadata:                                                       │
│    name: my-db-password-secret                                   │
│    annotations:                                                  │
│      reflector.v1.k8s.emberstack.com/reflection-allowed: "true" │
│      app-owner: "team-alpha"                                     │
│  data:                                                           │
│    password: <base64-encoded-value>                              │
└─────────────────────────────────────────────────────────────────┘
                               ↓
                    (Other controllers discover it)
                               ↓
              (kubernetes-reflector, etc. do their thing)
```

### Component Interaction

```
┌──────────────────────┐
│  Azure Key Vault     │
│  (Source of Truth)   │
└──────────────────────┘
           ↓
    (Read tags)
           ↓
┌──────────────────────────────────────────────────────────┐
│  Existing Controller (Enhanced)                          │
│                                                          │
│  1. SecretProviderClass Reconciler                       │
│     - Reads vault secrets and tags                       │
│     - Generates/updates SPC with embedded annotations    │
│                                                          │
│  2. Secret Annotation Reconciler (NEW)                   │
│     - Watches Secret creation/update events              │
│     - Matches Secrets to SPC definitions                 │
│     - Extracts annotations from SPC metadata             │
│     - Patches Secret with annotations                    │
└──────────────────────────────────────────────────────────┘
           ↓                              ↓
    (Updates)                       (Patches)
           ↓                              ↓
┌──────────────────────┐      ┌──────────────────────┐
│ SecretProviderClass  │      │  Kubernetes Secret   │
│ (with annotations)   │      │  (with annotations)  │
└──────────────────────┘      └──────────────────────┘
```

### Design Decision: One Controller vs Two Controllers

This section analyzes whether to implement this feature as:
- **Option A**: Single enhanced controller (extend existing controller)
- **Option B**: Two separate controllers (create dedicated secret-annotation controller)

See [Architectural Decision Analysis](#architectural-decision-analysis) below for detailed comparison.

**Preliminary Recommendation**: Single Enhanced Controller (Option A)

Rationale:
1. **Shared Context**: Both reconciliation loops need Azure Key Vault access and credentials
2. **Shared Infrastructure**: Can reuse existing circuit breakers, rate limiters, caching
3. **Operational Simplicity**: Single deployment, single RBAC configuration, single monitoring
4. **Logical Cohesion**: Both functions serve the same goal (Azure → Kubernetes sync)
5. **Code Reuse**: Can leverage existing patterns and utilities
6. **Target Audience Alignment**: Users managing secrets purely from Azure Vault benefit from single integration point

See detailed analysis in [Architectural Decision Analysis](#architectural-decision-analysis) section.

---

## Detailed Design

### 1. Azure Key Vault Tag Schema

#### Tag Format

Azure Key Vault secrets are tagged with metadata that should become Kubernetes annotations:

```
Tag Key: k8s-annotation.<annotation-key>
Tag Value: <annotation-value>

Examples:
  k8s-annotation.reflector.v1.k8s.emberstack.com/reflection-allowed = "true"
  k8s-annotation.app-owner = "team-alpha"
  k8s-annotation.environment = "production"
```

#### Tag Transformation Rules

| Azure Key Vault Tag | Kubernetes Annotation |
|---------------------|----------------------|
| `k8s-annotation.reflector.v1.k8s.emberstack.com/reflection-allowed` = `"true"` | `reflector.v1.k8s.emberstack.com/reflection-allowed: "true"` |
| `k8s-annotation.owner` = `"team-alpha"` | `owner: "team-alpha"` |

**Prefix Stripping**: The `k8s-annotation.` prefix is removed when creating the annotation.

#### Tag Filtering

Only tags with the `k8s-annotation.` prefix are processed. Other tags are ignored.

### 2. SecretProviderClass Annotation Embedding

#### Annotation Naming Convention

Per-secret metadata is embedded in the `SecretProviderClass` using a structured prefix:

```
secret-metadata.azure-keyvault-sync.io/<objectName>.<annotation-key>: <annotation-value>
```

**Components:**
- `secret-metadata.azure-keyvault-sync.io/` - Fixed prefix (our domain)
- `<objectName>` - The Azure Key Vault secret name (e.g., `my-db-password`)
- `<annotation-key>` - The final annotation key (e.g., `reflector/allowed`)
- `<annotation-value>` - The annotation value

#### Example SecretProviderClass

```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: my-app-secrets
  namespace: default
  annotations:
    # System annotation (existing)
    azure-keyvault-sync/last-sync: "2025-10-31T12:00:00Z"

    # Per-secret metadata (NEW)
    secret-metadata.azure-keyvault-sync.io/my-db-password.reflector.v1.k8s.emberstack.com/reflection-allowed: "true"
    secret-metadata.azure-keyvault-sync.io/my-db-password.app-owner: "team-alpha"
    secret-metadata.azure-keyvault-sync.io/my-api-key.app-owner: "team-beta"

spec:
  provider: azure
  parameters:
    keyvaultName: "my-app-vault"
    tenantId: "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
    objects: |
      array:
        - |
          objectName: my-db-password
          objectType: secret
        - |
          objectName: my-api-key
          objectType: secret

  secretObjects:
    - secretName: my-db-password-secret
      type: Opaque
      data:
        - objectName: my-db-password
          key: password

    - secretName: my-api-key-secret
      type: Opaque
      data:
        - objectName: my-api-key
          key: key
```

### 3. Secret Annotation Reconciliation Logic

#### Reconciliation Trigger

The controller watches for:
1. `Secret` creation events
2. `Secret` update events
3. `SecretProviderClass` update events (triggers recheck of related Secrets)

#### Reconciliation Algorithm

```go
func (ctrl *Controller) reconcileSecretAnnotations(ctx context.Context, secret *corev1.Secret) error {
    // 1. Find SecretProviderClass that references this Secret
    spc, objectName, err := ctrl.findSPCForSecret(ctx, secret)
    if err != nil || spc == nil {
        return err // No SPC found, nothing to do
    }

    // 2. Extract desired annotations from SPC metadata
    desiredAnnotations := extractAnnotationsForObject(spc, objectName)

    // 3. Compare with current Secret annotations
    needsPatch := false
    for key, desiredValue := range desiredAnnotations {
        if secret.Annotations[key] != desiredValue {
            needsPatch = true
            break
        }
    }

    if !needsPatch {
        return nil // Already in sync
    }

    // 4. Patch the Secret
    return ctrl.patchSecretAnnotations(ctx, secret, desiredAnnotations)
}
```

#### Finding Matching SecretProviderClass

```go
func (ctrl *Controller) findSPCForSecret(ctx context.Context, secret *corev1.Secret) (*secretsstorev1.SecretProviderClass, string, error) {
    // List all SPCs in the same namespace
    spcs, err := ctrl.client.SecretsstoreV1().SecretProviderClasses(secret.Namespace).List(ctx, metav1.ListOptions{})
    if err != nil {
        return nil, "", err
    }

    // Find SPC with matching secretObjects entry
    for _, spc := range spcs.Items {
        for _, secretObj := range spc.Spec.SecretObjects {
            if secretObj.SecretName == secret.Name {
                // Found match! Get the objectName
                if len(secretObj.Data) > 0 {
                    return &spc, secretObj.Data[0].ObjectName, nil
                }
            }
        }
    }

    return nil, "", nil // No match found
}
```

#### Extracting Annotations

```go
func extractAnnotationsForObject(spc *secretsstorev1.SecretProviderClass, objectName string) map[string]string {
    prefix := fmt.Sprintf("secret-metadata.azure-keyvault-sync.io/%s.", objectName)
    result := make(map[string]string)

    for key, value := range spc.Annotations {
        if strings.HasPrefix(key, prefix) {
            // Strip the prefix to get the final annotation key
            annotationKey := strings.TrimPrefix(key, prefix)
            result[annotationKey] = value
        }
    }

    return result
}
```

#### Patching Secret

```go
func (ctrl *Controller) patchSecretAnnotations(ctx context.Context, secret *corev1.Secret, annotations map[string]string) error {
    // Build JSON patch operations
    var patches []map[string]interface{}

    for key, value := range annotations {
        patches = append(patches, map[string]interface{}{
            "op":    "add",
            "path":  fmt.Sprintf("/metadata/annotations/%s", escapeJSONPointer(key)),
            "value": value,
        })
    }

    patchBytes, err := json.Marshal(patches)
    if err != nil {
        return err
    }

    _, err = ctrl.clientset.CoreV1().Secrets(secret.Namespace).Patch(
        ctx,
        secret.Name,
        types.JSONPatchType,
        patchBytes,
        metav1.PatchOptions{},
    )

    return err
}
```

### 4. Enhanced SecretProviderClass Generation

Extend the existing `GenerateSecretObjectsFromVault` function to include annotation embedding:

```go
// In internal/update/update.go
func GenerateSecretObjectsFromVault(
    vaultSecrets []azure.VaultSecret,
    spcAnnotations map[string]string, // Existing SPC annotations
) []*secretsstorev1.SecretObject {

    // Build new annotations map with per-secret metadata
    enhancedAnnotations := make(map[string]string)

    // Copy existing annotations
    for k, v := range spcAnnotations {
        enhancedAnnotations[k] = v
    }

    // Add per-secret metadata from vault secret tags
    for _, vaultSecret := range vaultSecrets {
        for tagKey, tagValue := range vaultSecret.Tags {
            if strings.HasPrefix(tagKey, "k8s-annotation.") {
                // Transform to SPC annotation format
                annotationKey := strings.TrimPrefix(tagKey, "k8s-annotation.")
                spcKey := fmt.Sprintf("secret-metadata.azure-keyvault-sync.io/%s.%s",
                    vaultSecret.Name, annotationKey)
                enhancedAnnotations[spcKey] = tagValue
            }
        }
    }

    // Generate secretObjects (one per vault secret)
    var secretObjects []*secretsstorev1.SecretObject
    for _, vaultSecret := range vaultSecrets {
        secretObjects = append(secretObjects, &secretsstorev1.SecretObject{
            SecretName: fmt.Sprintf("%s-secret", vaultSecret.Name),
            Type:       "Opaque",
            Data: []*secretsstorev1.SecretObjectData{
                {
                    ObjectName: vaultSecret.Name,
                    Key:        "value",
                },
            },
        })
    }

    return secretObjects
}
```

---

## Data Flow

### End-to-End Flow

```
1. Azure Key Vault State Change
   └─> Tag added/updated on secret "my-db-password"
       Tag: k8s-annotation.reflector/allowed = "true"

2. Controller Reconciliation (Periodic or Watch-Triggered)
   └─> Reads vault secrets and tags
   └─> Generates/updates SecretProviderClass
       Annotation: secret-metadata.azure-keyvault-sync.io/my-db-password.reflector/allowed: "true"

3. CSI Driver (Independent, Async)
   └─> Pod mounts volume
   └─> Creates Secret "my-db-password-secret" with content
       (No annotations yet)

4. Controller Secret Watcher
   └─> Detects Secret creation
   └─> Finds matching SPC
   └─> Extracts annotations for objectName "my-db-password"
   └─> Patches Secret with: reflector/allowed: "true"

5. External Controllers (Independent)
   └─> kubernetes-reflector sees annotation
   └─> Reflects secret to other namespaces
```

### Timing Diagram

```
Time  →
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

T0    Azure KV: Tag added to secret

T1    Controller sync: SPC updated with annotation

T2    Pod starts mounting volume

T3    CSI Driver: Secret created (no annotations)

T4    Controller: Detects Secret, patches with annotations

T5    kubernetes-reflector: Sees annotation, reflects secret

      ▲
      │
      └─── Eventual consistency window (typically 1-5 seconds)
```

**Note**: The exact timing between T3 and T4 varies, but the system guarantees annotations will be applied. Other controllers handle their own reconciliation when they see the annotations.

---

## Configuration Examples

### Example 1: Basic Single Secret

**Azure Key Vault:**
```
Secret: database-password
Tags:
  k8s-annotation.reflector.v1.k8s.emberstack.com/reflection-allowed: "true"
  k8s-annotation.app-owner: "platform-team"
```

**Generated SecretProviderClass:**
```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: my-app-secrets
  namespace: default
  annotations:
    azure-keyvault-sync/last-sync: "2025-10-31T12:00:00Z"
    secret-metadata.azure-keyvault-sync.io/database-password.reflector.v1.k8s.emberstack.com/reflection-allowed: "true"
    secret-metadata.azure-keyvault-sync.io/database-password.app-owner: "platform-team"
spec:
  provider: azure
  parameters:
    keyvaultName: "my-vault"
    tenantId: "..."
    objects: |
      array:
        - |
          objectName: database-password
          objectType: secret
  secretObjects:
    - secretName: database-password-secret
      type: Opaque
      data:
        - objectName: database-password
          key: password
```

**Resulting Kubernetes Secret:**
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: database-password-secret
  namespace: default
  annotations:
    reflector.v1.k8s.emberstack.com/reflection-allowed: "true"
    app-owner: "platform-team"
type: Opaque
data:
  password: <base64-value>
```

### Example 2: Multiple Secrets with Different Annotations

**Azure Key Vault:**
```
Secret: prod-db-password
Tags:
  k8s-annotation.reflector.v1.k8s.emberstack.com/reflection-allowed: "true"
  k8s-annotation.environment: "production"
  k8s-annotation.team: "backend"

Secret: dev-db-password
Tags:
  k8s-annotation.environment: "development"
  k8s-annotation.team: "backend"
  (No reflection annotation - not reflected)

Secret: api-key
Tags:
  k8s-annotation.reflector.v1.k8s.emberstack.com/reflection-allowed: "true"
  k8s-annotation.reflector.v1.k8s.emberstack.com/reflection-allowed-namespaces: "api-ns,worker-ns"
  k8s-annotation.team: "api-team"
```

**Generated SecretProviderClass:**
```yaml
metadata:
  annotations:
    # prod-db-password annotations
    secret-metadata.azure-keyvault-sync.io/prod-db-password.reflector.v1.k8s.emberstack.com/reflection-allowed: "true"
    secret-metadata.azure-keyvault-sync.io/prod-db-password.environment: "production"
    secret-metadata.azure-keyvault-sync.io/prod-db-password.team: "backend"

    # dev-db-password annotations
    secret-metadata.azure-keyvault-sync.io/dev-db-password.environment: "development"
    secret-metadata.azure-keyvault-sync.io/dev-db-password.team: "backend"

    # api-key annotations
    secret-metadata.azure-keyvault-sync.io/api-key.reflector.v1.k8s.emberstack.com/reflection-allowed: "true"
    secret-metadata.azure-keyvault-sync.io/api-key.reflector.v1.k8s.emberstack.com/reflection-allowed-namespaces: "api-ns,worker-ns"
    secret-metadata.azure-keyvault-sync.io/api-key.team: "api-team"

spec:
  secretObjects:
    - secretName: prod-db-password-secret
      type: Opaque
      data:
        - objectName: prod-db-password
          key: password

    - secretName: dev-db-password-secret
      type: Opaque
      data:
        - objectName: dev-db-password
          key: password

    - secretName: api-key-secret
      type: Opaque
      data:
        - objectName: api-key
          key: key
```

### Example 3: Complex Annotation Keys

**Azure Key Vault:**
```
Secret: tls-cert
Tags:
  k8s-annotation.cert-manager.io/issuer: "letsencrypt-prod"
  k8s-annotation.cert-manager.io/common-name: "example.com"
  k8s-annotation.nginx.ingress.kubernetes.io/ssl-redirect: "true"
```

**Resulting Secret:**
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: tls-cert-secret
  annotations:
    cert-manager.io/issuer: "letsencrypt-prod"
    cert-manager.io/common-name: "example.com"
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
```

---

## Implementation Plan

### Phase 1: Foundation (Week 1)

**Goal**: Set up basic infrastructure for annotation handling

1. **Add Azure Tag Reading**
   - Extend `internal/azure/vault.go` to read tags from secrets
   - Update `VaultSecret` struct to include `Tags map[string]string`
   - Add tag filtering logic (`k8s-annotation.` prefix)

2. **Extend SPC Generation**
   - Update `GenerateSecretObjectsFromVault` to embed annotations
   - Implement annotation key transformation (tag → SPC annotation format)
   - Add tests for annotation embedding logic

3. **Update Tests**
   - Add test fixtures with tagged secrets
   - Test annotation extraction and transformation
   - Verify SPC generation includes embedded annotations

**Deliverables:**
- [ ] Azure tag reading implemented and tested
- [ ] SPC generation includes embedded annotations
- [ ] Unit tests for tag → annotation transformation

### Phase 2: Secret Reconciliation (Week 2)

**Goal**: Implement the Secret annotation patching logic

1. **Add Secret Watcher**
   - Set up informer for `Secret` resources
   - Filter events to relevant secrets only
   - Add rate limiting and error handling

2. **Implement Reconciliation Logic**
   - `findSPCForSecret()` - Match Secret to SPC
   - `extractAnnotationsForObject()` - Parse SPC annotations
   - `patchSecretAnnotations()` - Apply annotations to Secret

3. **Add RBAC Permissions**
   - Update ClusterRole with Secret watch/patch permissions
   - Document security implications

**Deliverables:**
- [ ] Secret watcher operational
- [ ] Annotation reconciliation working
- [ ] RBAC properly configured

### Phase 3: Integration & Testing (Week 3)

**Goal**: End-to-end testing and edge case handling

1. **Integration Tests**
   - Test full flow: Azure KV → SPC → Secret
   - Test annotation updates when tags change
   - Test Secret deletion and recreation

2. **Edge Cases**
   - Handle Secret exists before SPC
   - Handle SPC deleted while Secret exists
   - Handle multiple SPCs referencing same Secret (conflict detection)

3. **Documentation**
   - User guide for tagging secrets in Azure KV
   - Troubleshooting guide
   - Migration guide for existing deployments

**Deliverables:**
- [ ] Integration tests passing
- [ ] Edge cases handled
- [ ] Documentation complete

### Phase 4: Observability & Rollout (Week 4)

**Goal**: Production-ready monitoring and gradual rollout

1. **Metrics & Logging**
   - Add Prometheus metrics for annotation operations
   - Enhance logging for debugging
   - Add events to Secrets when patched

2. **Feature Flag**
   - Add configuration option to enable/disable annotation sync
   - Default to disabled for safe rollout

3. **Gradual Rollout**
   - Deploy to dev environment
   - Monitor for 1 week
   - Deploy to staging
   - Deploy to production

**Deliverables:**
- [ ] Metrics and logging implemented
- [ ] Feature flag operational
- [ ] Successfully deployed to production

---

## Alternative: Zero-Sync-Time Configuration

### Overview

This section describes an **optional** architecture using a **Mutating Admission Webhook** to apply annotations **atomically** during Secret creation, eliminating the eventual consistency window.

**Status**: Not planned for initial implementation, but documented for potential future use.

### When to Consider This Approach

Consider implementing the webhook if:
- You experience actual production issues with the eventual consistency model
- You have strict ordering requirements between controllers
- You need to guarantee annotations are present before any other controller sees the Secret
- You have mature webhook infrastructure already in place

### Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     Kubernetes API Server                        │
└─────────────────────────────────────────────────────────────────┘
                               ↓
                  (Secret CREATE request from CSI Driver)
                               ↓
┌─────────────────────────────────────────────────────────────────┐
│              MutatingWebhookConfiguration                        │
│  - Intercepts Secret creation                                    │
│  - Calls our webhook endpoint                                    │
└─────────────────────────────────────────────────────────────────┘
                               ↓
┌─────────────────────────────────────────────────────────────────┐
│              Annotation Webhook Server                           │
│  1. Receives AdmissionReview request                             │
│  2. Finds matching SecretProviderClass                           │
│  3. Extracts annotations for this secret                         │
│  4. Mutates Secret to include annotations                        │
│  5. Returns modified Secret                                      │
└─────────────────────────────────────────────────────────────────┘
                               ↓
                  (Secret persisted WITH annotations)
                               ↓
┌─────────────────────────────────────────────────────────────────┐
│              Kubernetes Secret (Atomically Created)              │
│  - Content from CSI Driver                                       │
│  - Annotations from webhook                                      │
│  - Single creation event                                         │
└─────────────────────────────────────────────────────────────────┘
```

### Implementation Components

#### 1. Webhook Server

```go
// cmd/webhook/main.go
package main

import (
    "context"
    "crypto/tls"
    "net/http"

    "sigs.k8s.io/controller-runtime/pkg/webhook"
    "sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func main() {
    // Setup webhook server with TLS
    hookServer := webhook.NewServer(webhook.Options{
        Port:    9443,
        CertDir: "/etc/webhook/certs",
    })

    // Register mutating webhook handler
    hookServer.Register("/mutate-v1-secret",
        &webhook.Admission{Handler: &SecretAnnotator{}})

    // Start server
    if err := hookServer.Start(context.Background()); err != nil {
        panic(err)
    }
}

// SecretAnnotator handles Secret mutation
type SecretAnnotator struct {
    Client  client.Client
    decoder *admission.Decoder
}

func (s *SecretAnnotator) Handle(ctx context.Context, req admission.Request) admission.Response {
    secret := &corev1.Secret{}

    if err := s.decoder.Decode(req, secret); err != nil {
        return admission.Errored(http.StatusBadRequest, err)
    }

    // Only process Secrets created by CSI Driver
    if !isCSIDriverSecret(secret) {
        return admission.Allowed("not a CSI driver secret")
    }

    // Find matching SecretProviderClass
    spc, objectName, err := s.findMatchingSPC(ctx, secret)
    if err != nil {
        slog.Warn("Error finding SPC for secret", "error", err)
        return admission.Allowed("no matching SPC found")
    }
    if spc == nil {
        return admission.Allowed("no matching SPC")
    }

    // Extract annotations from SPC
    annotations := extractAnnotationsForObject(spc, objectName)
    if len(annotations) == 0 {
        return admission.Allowed("no annotations to apply")
    }

    // Merge annotations into Secret
    if secret.Annotations == nil {
        secret.Annotations = make(map[string]string)
    }
    for k, v := range annotations {
        secret.Annotations[k] = v
    }

    // Return mutated Secret
    marshaledSecret, err := json.Marshal(secret)
    if err != nil {
        return admission.Errored(http.StatusInternalServerError, err)
    }

    return admission.PatchResponseFromRaw(req.Object.Raw, marshaledSecret)
}

func isCSIDriverSecret(secret *corev1.Secret) bool {
    // Check for CSI driver labels or owner references
    if secret.Labels == nil {
        return false
    }
    _, hasLabel := secret.Labels["secrets-store.csi.k8s.io/managed"]
    return hasLabel
}
```

#### 2. MutatingWebhookConfiguration

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: azure-keyvault-sync-webhook
  annotations:
    cert-manager.io/inject-ca-from: kube-system/azure-keyvault-sync-webhook-cert
webhooks:
  - name: secrets.mutate.azure-keyvault-sync.io
    admissionReviewVersions: ["v1"]

    # CRITICAL: Fail open - don't block Secret creation if webhook is down
    failurePolicy: Ignore

    matchPolicy: Equivalent
    sideEffects: None
    timeoutSeconds: 5

    rules:
      - operations: ["CREATE"]
        apiGroups: [""]
        apiVersions: ["v1"]
        resources: ["secrets"]
        scope: "Namespaced"

    # Optional: Only watch specific namespaces
    namespaceSelector:
      matchExpressions:
        - key: azure-keyvault-sync/enabled
          operator: In
          values: ["true"]

    clientConfig:
      service:
        name: azure-keyvault-sync-webhook
        namespace: kube-system
        path: "/mutate-v1-secret"
        port: 443
```

#### 3. Certificate Management

```yaml
# Using cert-manager for automatic TLS cert rotation
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: azure-keyvault-sync-webhook-cert
  namespace: kube-system
spec:
  secretName: azure-keyvault-sync-webhook-tls
  dnsNames:
    - azure-keyvault-sync-webhook.kube-system.svc
    - azure-keyvault-sync-webhook.kube-system.svc.cluster.local
  issuerRef:
    name: selfsigned-issuer
    kind: ClusterIssuer
```

#### 4. Webhook Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: azure-keyvault-sync-webhook
  namespace: kube-system
spec:
  replicas: 2  # High availability
  selector:
    matchLabels:
      app: azure-keyvault-sync-webhook
  template:
    metadata:
      labels:
        app: azure-keyvault-sync-webhook
    spec:
      containers:
        - name: webhook
          image: azure-keyvault-sync-controller:latest
          command: ["/webhook"]
          ports:
            - containerPort: 9443
              name: webhook
          volumeMounts:
            - name: certs
              mountPath: /etc/webhook/certs
              readOnly: true
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              cpu: 500m
              memory: 512Mi
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8081
          readinessProbe:
            httpGet:
              path: /readyz
              port: 8081
      volumes:
        - name: certs
          secret:
            secretName: azure-keyvault-sync-webhook-tls
---
apiVersion: v1
kind: Service
metadata:
  name: azure-keyvault-sync-webhook
  namespace: kube-system
spec:
  ports:
    - port: 443
      targetPort: 9443
  selector:
    app: azure-keyvault-sync-webhook
```

### Hybrid Architecture: Webhook + Controller

**Recommended Approach**: Use both for defense in depth:

```
┌────────────────────────────────────────────────────────────┐
│  PRIMARY: Mutating Webhook (Fast Path)                     │
│  - Applies annotations during Secret creation              │
│  - Zero sync time for 99% of cases                         │
│  - failurePolicy: Ignore (non-blocking)                    │
└────────────────────────────────────────────────────────────┘
                          ↓
                   (If webhook fails)
                          ↓
┌────────────────────────────────────────────────────────────┐
│  FALLBACK: Controller Reconciliation (Safety Net)          │
│  - Detects Secrets without proper annotations              │
│  - Patches them asynchronously                             │
│  - Catches cases where webhook was down                    │
└────────────────────────────────────────────────────────────┘
```

**Benefits of Hybrid:**
1. Fast path (webhook) handles normal operation
2. Slow path (controller) ensures eventual consistency
3. System degrades gracefully if webhook is unavailable
4. No single point of failure

### Operational Considerations for Webhook

#### Pros ✅
- **Zero Sync Time**: Annotations present from moment of Secret creation
- **Single Event**: Other controllers see one creation event with annotations
- **Atomic Operation**: No window where Secret exists without annotations
- **Predictable Ordering**: Webhook runs before any other controller

#### Cons ⚠️
- **Operational Complexity**:
  - Requires TLS certificate management (cert-manager recommended)
  - Webhook server must be highly available
  - Adds latency to ALL Secret creation operations
  - More complex deployment and troubleshooting
- **Critical Path Dependency**:
  - If webhook is slow, all Secret creation is slow
  - If webhook is down and `failurePolicy: Fail`, Secret creation blocks
- **Debugging Difficulty**:
  - Webhook failures are harder to debug than controller errors
  - Admission logs are separate from controller logs
- **Resource Overhead**:
  - Requires additional deployment and service
  - Network hop for every Secret creation
- **Blast Radius**:
  - Webhook applies to ALL Secret creation in matched namespaces
  - Bugs can affect entire cluster's Secret management

#### When NOT to Use Webhook

Don't implement the webhook if:
- Your eventual consistency window (1-5 seconds) is acceptable
- You don't have webhook infrastructure (cert-manager, etc.)
- You're concerned about operational complexity
- You can't guarantee webhook high availability
- Other controllers handle their own reconciliation properly

#### Migration Path

If you later decide you need the webhook:

1. **Phase 1**: Deploy webhook with `failurePolicy: Ignore`
   - Webhook attempts to add annotations
   - Falls back to controller if webhook fails
   - Monitor webhook success rate

2. **Phase 2**: Monitor and tune
   - Ensure webhook is stable and fast
   - Verify no performance impact on cluster
   - Confirm annotations are being applied

3. **Phase 3**: Optionally tighten policy
   - If needed, change to `failurePolicy: Fail`
   - Only do this if webhook is proven reliable

### Decision Matrix: Controller vs Webhook

| Criteria | Controller Only | Webhook Only | Hybrid |
|----------|----------------|--------------|--------|
| **Complexity** | Low ✅ | Medium ⚠️ | High ❌ |
| **Operational Overhead** | Low ✅ | Medium ⚠️ | High ❌ |
| **Sync Time** | 1-5 seconds ⚠️ | 0 seconds ✅ | 0 seconds ✅ |
| **Failure Mode** | Graceful ✅ | Can block Secrets ⚠️ | Graceful ✅ |
| **Debuggability** | Easy ✅ | Hard ❌ | Medium ⚠️ |
| **Blast Radius** | Limited ✅ | Cluster-wide ❌ | Cluster-wide ❌ |
| **HA Requirements** | Standard ✅ | High ❌ | High ❌ |
| **Infrastructure Needs** | None ✅ | cert-manager ⚠️ | cert-manager ⚠️ |

**Recommendation**: Start with **Controller Only**. Add webhook later only if you discover actual problems with eventual consistency.

---

## Testing Strategy

### Unit Tests

1. **Tag Transformation**
   - Test `k8s-annotation.` prefix stripping
   - Test annotation key escaping/encoding
   - Test invalid tag formats

2. **Annotation Extraction**
   - Test `extractAnnotationsForObject()` with various prefixes
   - Test handling of malformed annotation keys
   - Test empty/nil cases

3. **SPC Matching**
   - Test `findSPCForSecret()` with multiple SPCs
   - Test no match scenarios
   - Test multiple objectName entries

### Integration Tests

1. **Full Flow**
   - Create secret in Azure KV with tags
   - Verify SPC gets correct annotations
   - Verify Secret gets patched with annotations

2. **Update Scenarios**
   - Tag added to existing secret
   - Tag removed from secret
   - Tag value changed

3. **Edge Cases**
   - Secret created before SPC
   - SPC deleted while Secret exists
   - Multiple SPCs reference same Secret

### End-to-End Tests

Using testcontainers or kind clusters:

```go
func TestE2E_SecretAnnotationSync(t *testing.T) {
    // 1. Setup test cluster
    cluster := setupTestCluster(t)
    defer cluster.Cleanup()

    // 2. Deploy controller
    deployController(t, cluster)

    // 3. Create mock Azure KV with tagged secret
    mockVault := createMockVault(t, map[string]string{
        "k8s-annotation.test": "value",
    })

    // 4. Create SPC
    spc := createTestSPC(t, cluster, mockVault)

    // 5. Simulate CSI Driver creating Secret
    secret := simulateCSIDriverSecret(t, cluster, spc)

    // 6. Wait for controller to patch Secret
    err := wait.PollImmediate(time.Second, 30*time.Second, func() (bool, error) {
        s, err := cluster.GetSecret(secret.Namespace, secret.Name)
        if err != nil {
            return false, err
        }
        return s.Annotations["test"] == "value", nil
    })

    assert.NoError(t, err, "Secret should have annotation")
}
```

### Performance Tests

1. **Scalability**
   - Test with 100+ secrets per SPC
   - Test with 50+ SPCs in namespace
   - Measure reconciliation time

2. **Annotation Size**
   - Test with max annotation size (256KB)
   - Test with many annotations per secret (50+)
   - Verify SPC size limits

### Webhook Testing (If Implemented)

1. **Unit Tests**
   - Test admission request parsing
   - Test Secret mutation logic
   - Test error handling

2. **Integration Tests**
   - Test webhook intercepts Secret creation
   - Test failurePolicy behavior
   - Test webhook unavailability scenarios

---

## Operational Considerations

### Monitoring

#### Key Metrics

```prometheus
# Annotation operations
azure_keyvault_sync_annotations_applied_total
azure_keyvault_sync_annotation_errors_total
azure_keyvault_sync_spc_annotations_embedded_total

# Reconciliation timing
azure_keyvault_sync_secret_reconciliation_duration_seconds
azure_keyvault_sync_secret_patch_duration_seconds

# Queue metrics
azure_keyvault_sync_secret_queue_depth
azure_keyvault_sync_secret_queue_latency_seconds

# Error tracking
azure_keyvault_sync_spc_not_found_total
azure_keyvault_sync_secret_patch_failures_total
```

#### Alerts

```yaml
# Alert if annotations aren't being applied
- alert: AnnotationSyncFailing
  expr: rate(azure_keyvault_sync_annotations_applied_total[5m]) == 0
    AND rate(azure_keyvault_sync_secret_reconciliation_duration_seconds_count[5m]) > 0
  for: 10m
  severity: warning

# Alert if error rate is high
- alert: AnnotationSyncHighErrorRate
  expr: rate(azure_keyvault_sync_annotation_errors_total[5m]) > 0.1
  for: 5m
  severity: critical
```

### Logging

```go
// Structured logging for debugging
slog.Info("Patching secret with annotations",
    "namespace", secret.Namespace,
    "secretName", secret.Name,
    "objectName", objectName,
    "spcName", spc.Name,
    "annotationCount", len(annotations))

slog.Warn("No matching SPC found for secret",
    "namespace", secret.Namespace,
    "secretName", secret.Name)

slog.Error("Failed to patch secret annotations",
    "error", err,
    "namespace", secret.Namespace,
    "secretName", secret.Name,
    "retryCount", retryCount)
```

### RBAC Requirements

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: azure-keyvault-sync-controller
rules:
  # Existing SPC permissions
  - apiGroups: ["secrets-store.csi.x-k8s.io"]
    resources: ["secretproviderclasses"]
    verbs: ["get", "list", "watch", "create", "update", "patch"]

  # NEW: Secret permissions
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list", "watch", "patch"]

  # Events for debugging
  - apiGroups: [""]
    resources: ["events"]
    verbs: ["create", "patch"]
```

### Configuration

```go
// Feature flag for gradual rollout
type Config struct {
    // Existing config...

    // NEW: Annotation sync configuration
    AnnotationSyncEnabled       bool          `env:"ANNOTATION_SYNC_ENABLED" default:"true"`
    AnnotationSyncWorkerCount   int           `env:"ANNOTATION_SYNC_WORKERS" default:"2"`
    SecretResyncInterval        time.Duration `env:"SECRET_RESYNC_INTERVAL" default:"5m"`
}
```

### Troubleshooting

#### Secret not getting annotations

1. Check SPC has embedded annotations:
   ```bash
   kubectl get spc <name> -o yaml | grep secret-metadata
   ```

2. Check Secret matches SPC definition:
   ```bash
   kubectl get spc <name> -o jsonpath='{.spec.secretObjects[*].secretName}'
   ```

3. Check controller logs:
   ```bash
   kubectl logs -l app=azure-keyvault-sync-controller | grep "secret.*annotation"
   ```

4. Check controller has RBAC permissions:
   ```bash
   kubectl auth can-i patch secrets --as=system:serviceaccount:kube-system:azure-keyvault-sync
   ```

#### Annotations not syncing from Azure KV

1. Check Azure KV secret has proper tags:
   ```bash
   az keyvault secret show --vault-name <vault> --name <secret> --query tags
   ```

2. Check tag prefix is correct (`k8s-annotation.`)

3. Check controller has Azure permissions to read tags

#### Performance issues

1. Check queue depth:
   ```bash
   kubectl get --raw /metrics | grep azure_keyvault_sync_secret_queue_depth
   ```

2. Check reconciliation latency:
   ```bash
   kubectl get --raw /metrics | grep azure_keyvault_sync_secret_reconciliation_duration
   ```

3. Adjust worker count if needed

---

## Architectural Decision Analysis

### Overview

This section provides a comprehensive analysis to decide between:
- **Option A**: Single enhanced controller (one controller with two reconciliation loops)
- **Option B**: Two separate controllers (existing SPC controller + new Secret annotation controller)

### Target Audience Context

**Primary Users**: Teams using Azure CSI Driver to synchronize secrets into their Kubernetes clusters who want to manage secret state purely from Azure Key Vault.

**User Journey**:
1. Store secrets in Azure Key Vault
2. Tag secrets with metadata (e.g., reflection configuration)
3. Deploy SecretProviderClass pointing to vault
4. Secrets appear in cluster with correct annotations
5. Ecosystem tools (reflector, etc.) work automatically

**Success Criteria**:
- Simple installation and configuration
- Minimal operational overhead
- Clear mental model of how system works
- Easy troubleshooting when issues arise

---

### Option A: Single Enhanced Controller

#### Architecture

```
┌─────────────────────────────────────────────────────────┐
│    azure-keyvault-sync-controller (Enhanced)            │
│                                                          │
│  ┌────────────────────────────────────────────────┐    │
│  │  SPC Reconciliation Loop (Existing)            │    │
│  │  - Watches SecretProviderClass resources       │    │
│  │  - Reads Azure Key Vault secrets & tags        │    │
│  │  - Generates/updates SPC with embedded annots  │    │
│  └────────────────────────────────────────────────┘    │
│                                                          │
│  ┌────────────────────────────────────────────────┐    │
│  │  Secret Annotation Loop (NEW)                  │    │
│  │  - Watches Secret resources                    │    │
│  │  - Matches Secrets to SPCs                     │    │
│  │  - Patches Secrets with annotations from SPC   │    │
│  └────────────────────────────────────────────────┘    │
│                                                          │
│  Shared Components:                                     │
│  - Azure credentials & authentication                   │
│  - Circuit breaker (Azure API protection)               │
│  - Rate limiter (API quota management)                  │
│  - Cache (vault secret metadata)                        │
│  - Health checks & metrics                              │
│  - Logging infrastructure                               │
└─────────────────────────────────────────────────────────┘
```

#### Implementation Details

**Code Organization**:
```
internal/
├── controller/
│   ├── controller.go         (main controller setup)
│   ├── spc_reconciler.go     (existing SPC logic)
│   └── secret_reconciler.go  (NEW: Secret annotation logic)
├── azure/
│   ├── vault.go              (Azure KV integration)
│   └── filter.go             (tag filtering)
├── update/
│   ├── update.go             (SPC generation)
│   └── patch.go              (Secret patching)
└── ...
```

**Shared State**:
- Single set of Azure credentials
- Single connection pool to Azure API
- Shared circuit breaker state
- Unified metrics registry
- Common logger instance

**Deployment**:
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: azure-keyvault-sync-controller
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: controller
          image: azure-keyvault-sync-controller:v2.0
          env:
            - name: ENABLE_SECRET_ANNOTATION_SYNC
              value: "true"
```

---

### Option B: Two Separate Controllers

#### Architecture

```
┌──────────────────────────────────────────────┐
│  azure-keyvault-sync-controller (Existing)   │
│                                              │
│  - Watches SecretProviderClass              │
│  - Reads Azure Key Vault secrets & tags     │
│  - Generates/updates SPC with annotations   │
│                                              │
│  Components:                                 │
│  - Azure credentials                         │
│  - Circuit breaker                           │
│  - Rate limiter                              │
│  - Cache                                     │
└──────────────────────────────────────────────┘
                    ↓
              (Creates SPCs with
           embedded annotations)
                    ↓
┌──────────────────────────────────────────────┐
│  secret-annotation-controller (NEW)          │
│                                              │
│  - Watches Secret resources                 │
│  - Watches SecretProviderClass resources    │
│  - Matches Secrets to SPCs                  │
│  - Patches Secrets with annotations         │
│                                              │
│  Components:                                 │
│  - Kubernetes client only                   │
│  - No Azure dependencies                    │
│  - Separate health/metrics                  │
└──────────────────────────────────────────────┘
```

#### Implementation Details

**Code Organization**:
```
# Existing controller (unchanged)
azure-keyvault-sync-controller/
├── internal/
│   ├── controller/
│   │   └── controller.go
│   ├── azure/
│   └── update/

# New controller (separate repo/module)
secret-annotation-controller/
├── internal/
│   ├── controller/
│   │   └── controller.go      (Secret reconciliation only)
│   ├── spc/
│   │   └── matcher.go         (SPC matching logic)
│   └── patch/
│       └── secret.go          (Secret patching)
```

**Isolated State**:
- Each controller has own credentials (if needed)
- Independent circuit breakers
- Separate rate limiters
- Independent caches
- Separate metrics endpoints
- Independent logging

**Deployment**:
```yaml
# Deployment 1: Existing SPC controller
apiVersion: apps/v1
kind: Deployment
metadata:
  name: azure-keyvault-sync-controller
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: controller
          image: azure-keyvault-sync-controller:v1.5

---
# Deployment 2: New Secret annotation controller
apiVersion: apps/v1
kind: Deployment
metadata:
  name: secret-annotation-controller
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: controller
          image: secret-annotation-controller:v1.0
```

---

### Comparative Analysis

#### 1. User Experience

| Aspect | Option A (Single) | Option B (Two) | Winner |
|--------|-------------------|----------------|--------|
| **Installation** | Single Helm chart/manifest | Two separate installations | ✅ **A** |
| **Configuration** | One set of environment variables | Two sets of configuration | ✅ **A** |
| **Mental Model** | "One controller syncs Azure → K8s" | "Two controllers collaborate" | ✅ **A** |
| **Documentation** | Single README and guide | Two separate docs to understand | ✅ **A** |
| **Upgrades** | Single version to track | Two versions to coordinate | ✅ **A** |
| **Feature Discovery** | Annotations "just work" | Must install second controller | ✅ **A** |

**Analysis**: For the target audience (users wanting to manage secrets from Azure Vault), Option A provides a significantly simpler experience. They install one thing, configure Azure credentials once, and everything works.

---

#### 2. Operational Complexity

| Aspect | Option A (Single) | Option B (Two) | Winner |
|--------|-------------------|----------------|--------|
| **Deployments** | 1 deployment | 2 deployments | ✅ **A** |
| **RBAC** | 1 ServiceAccount, 1 ClusterRole | 2 ServiceAccounts, 2 ClusterRoles | ✅ **A** |
| **Monitoring** | 1 metrics endpoint | 2 metrics endpoints | ✅ **A** |
| **Logging** | Unified log stream | Two separate log streams | ✅ **A** |
| **Health Checks** | 1 health endpoint | 2 health endpoints | ✅ **A** |
| **Resource Usage** | ~150MB memory, 1 pod | ~250MB memory, 2 pods | ✅ **A** |
| **Failure Modes** | Easier (single point) | More complex (two points) | ✅ **A** |

**Analysis**: Option A reduces operational overhead by 50%. Fewer moving parts means less to monitor, configure, and troubleshoot.

---

#### 3. Development & Maintenance

| Aspect | Option A (Single) | Option B (Two) | Winner |
|--------|-------------------|----------------|--------|
| **Code Organization** | Clear internal modules | Clean separation | ⚠️ **Tie** |
| **Testing** | Shared test infrastructure | Independent test suites | ⚠️ **Tie** |
| **Release Process** | Single version, single release | Two versions, coordinated releases | ✅ **A** |
| **Bug Fixes** | Single patch release | Coordinate two releases | ✅ **A** |
| **Code Reuse** | Maximum reuse | Potential duplication | ✅ **A** |
| **Dependency Management** | Single go.mod | Two go.mod files | ✅ **A** |
| **CI/CD** | One pipeline | Two pipelines | ✅ **A** |

**Analysis**: Option A simplifies the development lifecycle. Bug fixes, features, and releases happen in one place with one timeline.

---

#### 4. Technical Architecture

| Aspect | Option A (Single) | Option B (Two) | Winner |
|--------|-------------------|----------------|--------|
| **Azure Auth** | Shared credentials | Only in SPC controller | ⚠️ **Tie** |
| **Circuit Breaker** | Shared (better protection) | N/A for Secret controller | ✅ **A** |
| **Rate Limiting** | Unified quota management | Only SPC controller | ✅ **A** |
| **Caching** | Shared cache (more efficient) | N/A for Secret controller | ✅ **A** |
| **Responsibility Isolation** | Internal modules | Process boundaries | ✅ **B** |
| **Failure Isolation** | Single point of failure | Independent failures | ✅ **B** |
| **Resource Scaling** | Can't scale independently | Can scale separately | ✅ **B** |

**Analysis**: Option B provides cleaner architectural boundaries and better failure isolation, but Option A is more resource-efficient and provides better Azure API protection through shared infrastructure.

---

#### 5. Troubleshooting & Debugging

| Scenario | Option A (Single) | Option B (Two) | Winner |
|----------|-------------------|----------------|--------|
| **"Annotations not appearing"** | Check one controller log | Check two controller logs | ✅ **A** |
| **"Is system healthy?"** | Check 1 pod status | Check 2 pod statuses | ✅ **A** |
| **"Azure auth failing"** | Fix in one place | Only affects SPC controller | ⚠️ **Tie** |
| **"Secret patching slow"** | Profile one controller | Isolate to Secret controller | ✅ **B** |
| **"Memory leak"** | Harder to isolate | Clear which controller | ✅ **B** |
| **"Rate limit hit"** | Clear unified view | Split across controllers | ✅ **A** |

**Analysis**: Option A makes common troubleshooting simpler (single log stream, single pod), but Option B makes isolating specific component failures easier.

---

#### 6. Failure Mode Analysis

##### Option A Failure Modes:

**Scenario 1: Controller Pod Crashes**
- **Impact**: Both SPC sync AND Secret annotation stop
- **MTTR**: Kubernetes restarts pod (~30 seconds)
- **Data Loss**: None (reconciliation resumes)
- **User Impact**: Temporary sync delay

**Scenario 2: Azure API Rate Limit**
- **Impact**: SPC updates delayed, but Secret patching continues (reads from existing SPCs)
- **Circuit Breaker**: Protects both functions
- **User Impact**: Minimal (existing SPCs cached)

**Scenario 3: Bug in Secret Reconciliation**
- **Impact**: Secret annotations fail, but SPC generation continues
- **Workaround**: Disable feature flag, roll back
- **User Impact**: Annotations missing until fixed

##### Option B Failure Modes:

**Scenario 1: SPC Controller Pod Crashes**
- **Impact**: Only SPC sync stops
- **Secret Controller**: Continues patching existing Secrets
- **User Impact**: New secrets not added until restart

**Scenario 2: Secret Controller Pod Crashes**
- **Impact**: Only Secret annotation stops
- **SPC Controller**: Continues updating SPCs
- **User Impact**: New Secrets lack annotations until restart

**Scenario 3: Azure API Rate Limit**
- **Impact**: Only affects SPC controller
- **Secret Controller**: Unaffected, continues working
- **User Impact**: Minimal (Secret controller operates independently)

**Analysis**: Option B provides better failure isolation, but Option A's simpler architecture means fewer failure modes overall.

---

#### 7. Resource Utilization

##### Option A (Single Controller):

```yaml
Resources:
  CPU Request: 100m
  CPU Limit: 500m
  Memory Request: 128Mi
  Memory Limit: 512Mi

Total Cluster Resources:
  Pods: 1
  Total Memory: 512Mi
  Total CPU: 500m
```

##### Option B (Two Controllers):

```yaml
# SPC Controller
Resources:
  CPU Request: 100m
  CPU Limit: 500m
  Memory Request: 128Mi
  Memory Limit: 512Mi

# Secret Annotation Controller
Resources:
  CPU Request: 50m
  CPU Limit: 250m
  Memory Request: 64Mi
  Memory Limit: 256Mi

Total Cluster Resources:
  Pods: 2
  Total Memory: 768Mi
  Total CPU: 750m
```

**Resource Efficiency**: Option A uses ~33% less memory and ~33% less CPU

---

#### 8. Code Complexity

##### Option A (Single Controller):

**Pros**:
- Shared utilities and helpers
- Unified error handling patterns
- Common configuration
- Single main.go entry point

**Cons**:
- Larger controller.go file (but modularized)
- More internal packages
- Need to manage feature flags

**Estimated Code Size**: +2,000 lines to existing codebase

##### Option B (Two Controllers):

**Pros**:
- Clean separation of concerns
- Each controller is simpler individually
- Easier to understand in isolation
- No feature flags needed

**Cons**:
- Duplicate infrastructure code (logging, metrics)
- Duplicate client setup
- Coordination complexity (version compatibility)

**Estimated Code Size**: +3,500 lines total (new repo + changes to existing)

---

#### 9. Real-World Scenarios

##### Scenario 1: "I just want to use kubernetes-reflector with Azure CSI Driver"

**Option A**:
```bash
# Install single controller
helm install azure-keyvault-sync ./chart

# Tag secret in Azure
az keyvault secret set-attributes \
  --vault-name my-vault \
  --name my-secret \
  --tags k8s-annotation.reflector/allowed=true

# Done! Annotations appear automatically
```

**Option B**:
```bash
# Install SPC controller
helm install azure-keyvault-sync ./spc-controller-chart

# Install Secret annotation controller
helm install secret-annotation ./secret-controller-chart

# Tag secret in Azure
az keyvault secret set-attributes \
  --vault-name my-vault \
  --name my-secret \
  --tags k8s-annotation.reflector/allowed=true

# Verify both controllers running
kubectl get pods -l app=azure-keyvault-sync-controller
kubectl get pods -l app=secret-annotation-controller
```

**Winner**: ✅ **Option A** - Simpler for end users

---

##### Scenario 2: "Secret annotation controller has a bug"

**Option A**:
```bash
# Disable feature flag
kubectl set env deployment/azure-keyvault-sync-controller \
  ENABLE_SECRET_ANNOTATION_SYNC=false

# SPC sync continues working
# Deploy fix and re-enable
```

**Option B**:
```bash
# Delete Secret controller deployment
kubectl delete deployment secret-annotation-controller

# SPC controller unaffected
# Deploy fix as separate release
```

**Winner**: ✅ **Option B** - Better isolation

---

##### Scenario 3: "Azure API is rate limiting us"

**Option A**:
```bash
# Shared circuit breaker triggers
# Both loops back off together
# Single metrics endpoint shows unified rate limit status
```

**Option B**:
```bash
# Only SPC controller affected
# Secret controller continues independently
# Need to check two metrics endpoints
```

**Winner**: ⚠️ **Tie** - Different trade-offs

---

### Decision Matrix

| Criteria | Weight | Option A Score | Option B Score | Weighted A | Weighted B |
|----------|--------|----------------|----------------|------------|------------|
| **User Experience** | 10 | 9 | 5 | 90 | 50 |
| **Operational Simplicity** | 9 | 9 | 4 | 81 | 36 |
| **Resource Efficiency** | 7 | 8 | 5 | 56 | 35 |
| **Development Velocity** | 7 | 8 | 6 | 56 | 42 |
| **Troubleshooting** | 6 | 7 | 6 | 42 | 36 |
| **Failure Isolation** | 6 | 5 | 8 | 30 | 48 |
| **Code Maintainability** | 5 | 7 | 8 | 35 | 40 |
| **Scalability** | 4 | 6 | 8 | 24 | 32 |
| **Total** | | | | **414** | **319** |

**Scores**: 1-10 scale (10 = best)
**Weights**: Based on target audience priorities

---

### Recommendation: Option A (Single Enhanced Controller)

#### Primary Justification

For the stated target audience ("folks utilizing Azure CSI Driver to synchronize secrets into their cluster, and want a way to manage the state of their secrets purely from Azure vault"), **Option A provides superior value**:

1. **Single Integration Point**: Users install one controller and configure Azure credentials once
2. **Simpler Mental Model**: "This controller syncs everything from Azure to Kubernetes"
3. **Lower Operational Burden**: 50% fewer components to deploy, monitor, and maintain
4. **Resource Efficiency**: 33% less cluster resources required
5. **Unified Troubleshooting**: Single log stream and metrics endpoint
6. **Faster Development**: Single codebase and release cycle

#### When Option B Would Be Better

Consider two separate controllers if:
- You have **completely different deployment patterns** (e.g., SPC controller in cluster A, Secret controller in cluster B)
- You need to **scale components independently** (e.g., 10 SPC controller replicas, 1 Secret controller)
- You have **different release cycles** for each function
- You want to **reuse Secret controller** with other Secret sources (not just Azure CSI Driver)
- You have **organizational boundaries** (different teams own each controller)

**None of these conditions apply to the stated use case.**

#### Risk Mitigation for Option A

To address Option B's advantages while staying with Option A:

1. **Internal Modularity**: Structure code as if they were separate controllers
   ```go
   internal/
   ├── spc/         // SPC reconciliation (isolated module)
   ├── secret/      // Secret reconciliation (isolated module)
   └── shared/      // Common utilities
   ```

2. **Feature Flag**: Allow disabling Secret annotation sync if issues arise
   ```yaml
   ENABLE_SECRET_ANNOTATION_SYNC=false
   ```

3. **Independent Metrics**: Separate Prometheus metrics for each reconciliation loop
   ```
   azure_keyvault_sync_spc_*
   azure_keyvault_sync_secret_*
   ```

4. **Isolated Testing**: Write tests for each module independently
   ```go
   internal/spc/reconciler_test.go
   internal/secret/reconciler_test.go
   ```

5. **Clear Logging**: Tag logs by reconciliation loop
   ```go
   slog.Info("reconciling", "component", "spc")
   slog.Info("reconciling", "component", "secret")
   ```

---

### Implementation Path

#### Recommended: Option A (Single Controller)

**Phase 1: Internal Modularization** (Week 1)
- Refactor existing controller code into `internal/spc/` module
- Establish patterns for isolated reconciliation loops
- Add feature flag infrastructure

**Phase 2: Secret Reconciliation** (Week 2)
- Implement `internal/secret/` module
- Add Secret watcher and reconciliation logic
- Feature flag defaults to **disabled**

**Phase 3: Testing & Validation** (Week 3)
- Comprehensive testing of both modules
- Performance testing with both loops enabled
- Verify resource usage within limits

**Phase 4: Gradual Rollout** (Week 4)
- Enable feature flag in dev (monitor for 1 week)
- Enable in staging (monitor for 1 week)
- Enable in production (gradual rollout by namespace)

#### Alternative: Option B (Two Controllers)

If you decide to go with Option B despite the recommendation:

**Phase 1: Extract Common Code** (Week 1)
- Create shared library for SPC matching logic
- Extract annotation transformation utilities
- Define inter-controller contract

**Phase 2: Build Secret Controller** (Week 2)
- Create new repository
- Implement Secret reconciliation
- Add independent testing

**Phase 3: Integration Testing** (Week 3)
- Test both controllers together
- Verify coordination works correctly
- Load testing with both controllers

**Phase 4: Coordinated Deployment** (Week 4)
- Deploy SPC controller updates (with embedded annotations)
- Deploy Secret annotation controller
- Monitor both controllers for 2 weeks before production

---

### Conclusion

**Final Recommendation**: **Option A - Single Enhanced Controller**

The decision aligns with:
- Target audience needs (simplicity, Azure-focused)
- Operational priorities (low overhead, easy troubleshooting)
- Resource constraints (efficient cluster usage)
- Development velocity (faster delivery, single codebase)

While Option B has advantages in failure isolation and architectural purity, these benefits don't outweigh the significant complexity increase for the target use case.

**Decision**: Proceed with extending the existing `azure-keyvault-sync-controller` with a second reconciliation loop for Secret annotation synchronization.

---

## Future Enhancements

### 1. Label Synchronization

Support syncing labels in addition to annotations:

```
Tag: k8s-label.environment = "production"
→ Label: environment: "production"
```

### 2. Annotation Templates

Support templating in tag values:

```
Tag: k8s-annotation.owner = "team-${VAULT_NAME}"
→ Annotation: owner: "team-my-vault"
```

### 3. Annotation Validation

Validate annotations before applying:
- Check annotation key format
- Validate against schema
- Prevent dangerous annotations

### 4. Multi-Cluster Support

Sync annotations across multiple clusters from single source.

### 5. Annotation Drift Detection

Alert when Secret annotations drift from SPC definition.

### 6. Dry-Run Mode

Preview what annotations would be applied without actually patching.

---

## Appendix

### Glossary

- **SPC**: SecretProviderClass - Kubernetes custom resource for CSI Driver configuration
- **CSI Driver**: Container Storage Interface driver for mounting secrets as volumes
- **Eventual Consistency**: State where system guarantees convergence but allows temporary inconsistency
- **Admission Webhook**: Kubernetes extension point for intercepting API requests
- **Mutating Webhook**: Webhook that can modify resources before persistence

### Related Documentation

- [Azure Secrets Store CSI Driver](https://github.com/Azure/secrets-store-csi-driver-provider-azure)
- [Kubernetes Reflector](https://github.com/emberstack/kubernetes-reflector)
- [Kubernetes Admission Controllers](https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/)
- [Azure Key Vault Tags](https://learn.microsoft.com/en-us/azure/key-vault/general/overview)

### Security Considerations

1. **Tag Permissions**: Ensure only authorized users can set `k8s-annotation.*` tags in Azure KV
2. **Annotation Injection**: Validate annotation keys to prevent injection attacks
3. **RBAC**: Limit Secret patch permissions to controller service account only
4. **Audit Logging**: Enable audit logs for Secret patch operations
5. **Namespace Isolation**: Controller respects namespace boundaries

---

**Document Version**: 1.0
**Last Updated**: 2025-10-31
**Status**: Design Proposal - Ready for Implementation
