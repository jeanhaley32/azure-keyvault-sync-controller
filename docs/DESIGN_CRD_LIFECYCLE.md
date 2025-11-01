# AzureKeyVaultSync CRD: Full Lifecycle Management

> **Status**: Design Specification
> **Created**: 2025-11-01
> **Authors**: Architecture Team
> **Related**: DESIGN_SECRET_ANNOTATION_SYNC.md

---

## Table of Contents

1. [Overview](#overview)
2. [CRD API Specification](#crd-api-specification)
3. [Azure Vault Tag Schema](#azure-vault-tag-schema)
4. [Dual-Mode Architecture](#dual-mode-architecture)
5. [Resource Lifecycle](#resource-lifecycle)
6. [Configuration Examples](#configuration-examples)
7. [User Workflows](#user-workflows)
8. [Design Decisions](#design-decisions)

---

## Overview

### Purpose

The `AzureKeyVaultSync` Custom Resource Definition (CRD) enables **full lifecycle management** of Kubernetes secrets from Azure Key Vault. Instead of manually creating and maintaining `SecretProviderClass` resources, users define a single CRD that:

1. Connects to an Azure Key Vault
2. Reads secrets and their metadata (tags)
3. Generates a complete `SecretProviderClass` automatically
4. Manages the SPC lifecycle (create, update, delete)
5. Synchronizes secret annotations to Kubernetes Secrets

### Goals

✅ **Declarative Management**: Single CRD defines entire secret sync configuration
✅ **Azure as Source of Truth**: Vault tags drive Kubernetes resource generation
✅ **Automation**: No manual SPC updates when vault changes
✅ **Multi-tenancy**: Support shared vaults with tag-based filtering
✅ **GitOps Friendly**: Version-controlled, reviewable configuration
✅ **Backwards Compatible**: Coexists with manual SPC + annotation mode

### Non-Goals

❌ Replacing Azure Secrets Store CSI Driver (we use it for content sync)
❌ Managing vault permissions or Azure RBAC
❌ Validating secret content or performing transformations
❌ Cross-cloud secret synchronization

### Relationship to Secret Annotation Sync

This CRD builds on the Secret Annotation Synchronization feature (see `DESIGN_SECRET_ANNOTATION_SYNC.md`):

```
Secret Annotation Sync (Base Feature):
  - Reads Azure vault tags
  - Embeds annotations in SPC metadata
  - Patches Kubernetes Secrets with annotations

CRD Lifecycle Management (This Feature):
  - Generates entire SPC from vault data
  - Manages SPC ownership and deletion
  - Provides declarative API for sync configuration
```

Both features share the Secret annotation patching mechanism.

---

## CRD API Specification

### Resource Definition

```yaml
apiVersion: azure-keyvault-sync.io/v1alpha1
kind: AzureKeyVaultSync
metadata:
  name: <string>        # SPC will match this name
  namespace: <string>   # SPC will be created in this namespace
spec:
  # Required fields
  keyvaultName: <string>
  tenantId: <string>
  clientID: <string>
  serviceAccount: <string>

  # Optional fields
  filters: <map[string]string>
  deletePolicy: <Cascade|Orphan>

status:
  conditions: <[]Condition>
  lastSyncTime: <Time>
  secretCount: <int>
  secretObjectCount: <int>
  generatedSPCName: <string>
  observedGeneration: <int64>
```

### Spec Fields

#### Required Fields

| Field | Type | Description | Example |
|-------|------|-------------|---------|
| `keyvaultName` | string | Azure Key Vault name | `staging-flow-vault` |
| `tenantId` | string | Azure tenant ID (UUID) | `8b83ab42-3e3f-422d-85ca-fe2d40c51e35` |
| `clientID` | string | Azure client ID for workload identity (UUID) | `aac3d546-358f-4e74-94e5-bb4c472d7cc0` |
| `serviceAccount` | string | Kubernetes ServiceAccount name with Azure workload identity annotations | `aks-staging-flow` |

#### Optional Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `filters` | map[string]string | `nil` | Tag key/value pairs for filtering vault secrets. If omitted, all secrets from vault are synced. |
| `deletePolicy` | DeletePolicy | `Cascade` | What happens to SPC when CRD is deleted. `Cascade` deletes SPC (and its Secrets), `Orphan` leaves SPC running. |

#### DeletePolicy Values

```go
type DeletePolicy string

const (
    DeletePolicyCascade DeletePolicy = "Cascade"  // SPC deleted when CRD deleted
    DeletePolicyOrphan  DeletePolicy = "Orphan"   // SPC remains when CRD deleted
)
```

### Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `conditions` | []metav1.Condition | Standard Kubernetes conditions (SPCReady, SecretsReady, etc.) |
| `lastSyncTime` | *metav1.Time | Last successful vault sync timestamp |
| `secretCount` | int | Number of secrets synced from vault (after filtering) |
| `secretObjectCount` | int | Number of secretObjects created (secrets with `secret-object: "true"` tag) |
| `generatedSPCName` | string | Name of the generated SPC (matches CRD name) |
| `observedGeneration` | int64 | Last observed generation of the CRD spec |

### Status Conditions

| Type | Reason | Status | Description |
|------|--------|--------|-------------|
| `SPCReady` | `SPCCreated` | True | SecretProviderClass successfully created/updated |
| `SPCReady` | `VaultReadFailed` | False | Failed to read vault secrets |
| `SPCReady` | `SPCCreateFailed` | False | Failed to create/update SPC |
| `SecretsReady` | `AnnotationsApplied` | True | Secret annotations successfully applied |
| `SecretsReady` | `NoPodMounting` | Unknown | No pods mounting this SPC yet (no Secrets created) |

### Validation Rules

**Field Validation:**
- `keyvaultName`: Must be valid Azure Key Vault name (alphanumeric + hyphens, 3-24 chars)
- `tenantId`: Must be valid UUID
- `clientID`: Must be valid UUID
- `serviceAccount`: Must be valid Kubernetes resource name
- `filters`: Keys and values must be valid Azure tag names (no special chars)
- `deletePolicy`: Must be `Cascade` or `Orphan`

**Cross-Field Validation:**
- If `deletePolicy` is `Orphan`, a warning condition is set (orphaned SPCs can cause confusion)

### Example Complete CRD

```yaml
apiVersion: azure-keyvault-sync.io/v1alpha1
kind: AzureKeyVaultSync
metadata:
  name: flow-staging-secrets
  namespace: default
  labels:
    app: flow
    environment: staging
spec:
  # Required: Azure connection details
  keyvaultName: staging-flow-vault
  tenantId: 8b83ab42-3e3f-422d-85ca-fe2d40c51e35
  clientID: aac3d546-358f-4e74-94e5-bb4c472d7cc0
  serviceAccount: aks-staging-flow

  # Optional: Filter by tags (sync only matching secrets)
  filters:
    service: flow
    environment: staging

  # Optional: Deletion policy (default: Cascade)
  deletePolicy: Cascade

status:
  conditions:
    - type: SPCReady
      status: "True"
      reason: SPCCreated
      message: "SecretProviderClass flow-staging-secrets created successfully"
      lastTransitionTime: "2025-11-01T10:00:00Z"
    - type: SecretsReady
      status: "True"
      reason: AnnotationsApplied
      message: "3 secrets synchronized with annotations"
      lastTransitionTime: "2025-11-01T10:00:15Z"
  lastSyncTime: "2025-11-01T10:00:00Z"
  secretCount: 5
  secretObjectCount: 3
  generatedSPCName: flow-staging-secrets
  observedGeneration: 1
```

---

## Azure Vault Tag Schema

### Overview

Azure Key Vault secrets use **tags** to define how they should be synchronized to Kubernetes. The controller reads these tags and generates appropriate Kubernetes resources.

### Tag Categories

#### 1. Annotation Tags

**Format**: `k8s-annotation.<annotation-key>: <annotation-value>`

**Purpose**: Becomes a Kubernetes Secret annotation

**Processing**:
1. Controller reads tag from vault secret
2. Strips `k8s-annotation.` prefix
3. Embeds in SPC metadata as `secret-metadata.azure-keyvault-sync.io/<secretName>.<annotation-key>`
4. Patches Kubernetes Secret with final annotation

**Examples**:

| Azure Tag | SPC Annotation | Secret Annotation |
|-----------|----------------|-------------------|
| `k8s-annotation.reflector.v1.k8s.emberstack.com/reflection-allowed: "true"` | `secret-metadata.../db-password.reflector.v1.k8s.emberstack.com/reflection-allowed: "true"` | `reflector.v1.k8s.emberstack.com/reflection-allowed: "true"` |
| `k8s-annotation.owner: "team-backend"` | `secret-metadata.../db-password.owner: "team-backend"` | `owner: "team-backend"` |
| `k8s-annotation.app: "myapp"` | `secret-metadata.../db-password.app: "myapp"` | `app: "myapp"` |

#### 2. SecretObject Control Tags

**Tag**: `secret-object: "true"`

**Purpose**: Include this secret in SPC `secretObjects` array (sync to Kubernetes Secret, not just volume)

**Default**: If tag is absent or `"false"`, secret appears in `objects` array only (mounted as volume file)

**Tag**: `secret-object-name: "<custom-name>"`

**Purpose**: Override default Secret name

**Default**: If absent, Secret name is `<vault-secret-name>-secret`

**Examples**:

| Vault Secret Name | `secret-object` Tag | `secret-object-name` Tag | Resulting Secret Name |
|-------------------|---------------------|--------------------------|----------------------|
| `database-password` | `"true"` | (absent) | `database-password-secret` |
| `db-pwd` | `"true"` | `"db-creds"` | `db-creds` |
| `api-key` | `"false"` or absent | - | (no Secret created, volume only) |

#### 3. Filter Tags

**Purpose**: Used with CRD `spec.filters` to select subset of vault secrets

**Common Examples**:
- `service: <service-name>` - Which service owns this secret
- `environment: <env>` - Which environment (staging, prod, etc.)
- `team: <team-name>` - Which team manages this secret
- `kubernetes-ready: "true"` - Opt-in for kubernetes sync

**Matching Logic**:
- If `filters` is specified in CRD, secret must match **ALL** filter key/value pairs
- If `filters` is omitted, all secrets are synced (no filtering)

**Example**:

```yaml
# CRD filters
spec:
  filters:
    service: flow
    environment: staging

# Vault secret tags
Tags:
  service: flow
  environment: staging
  owner: team-a
# ✅ MATCH - has both required tags

# Vault secret tags
Tags:
  service: backend
  environment: staging
# ❌ NO MATCH - service doesn't match

# Vault secret tags
Tags:
  environment: staging
# ❌ NO MATCH - missing service tag
```

### Complete Tag Example

**Azure Key Vault Secret: `database-password`**

```
Tags:
  k8s-annotation.reflector.v1.k8s.emberstack.com/reflection-allowed: "true"
  k8s-annotation.reflector.v1.k8s.emberstack.com/reflection-allowed-namespaces: "backend,frontend"
  k8s-annotation.owner: "team-backend"
  k8s-annotation.criticality: "high"
  secret-object: "true"
  secret-object-name: "db-creds"
  service: "flow"
  environment: "staging"
  team: "backend"
```

**Generated Resources:**

```yaml
# SecretProviderClass (partial)
metadata:
  annotations:
    secret-metadata.azure-keyvault-sync.io/database-password.reflector.v1.k8s.emberstack.com/reflection-allowed: "true"
    secret-metadata.azure-keyvault-sync.io/database-password.reflector.v1.k8s.emberstack.com/reflection-allowed-namespaces: "backend,frontend"
    secret-metadata.azure-keyvault-sync.io/database-password.owner: "team-backend"
    secret-metadata.azure-keyvault-sync.io/database-password.criticality: "high"
spec:
  secretObjects:
    - secretName: db-creds  # Custom name from tag
      type: Opaque
      data:
        - objectName: database-password
          key: password

---
# Kubernetes Secret (created by CSI Driver, patched by controller)
apiVersion: v1
kind: Secret
metadata:
  name: db-creds
  annotations:
    reflector.v1.k8s.emberstack.com/reflection-allowed: "true"
    reflector.v1.k8s.emberstack.com/reflection-allowed-namespaces: "backend,frontend"
    owner: "team-backend"
    criticality: "high"
type: Opaque
data:
  password: <base64-value>
```

### Tag Naming Best Practices

1. **Use consistent prefixes**: `k8s-annotation.` for all Kubernetes annotations
2. **Use qualified names**: Full annotation keys (e.g., `reflector.v1.k8s.emberstack.com/...`)
3. **Be explicit**: Always set `secret-object: "true"` when you want a Secret (don't rely on defaults)
4. **Document filter tags**: Establish org-wide standards for `service`, `environment`, etc.
5. **Avoid conflicts**: Don't use Azure reserved tag names

---

## Dual-Mode Architecture

### Overview

The controller supports two operational modes that can coexist:

1. **CRD Mode**: Full lifecycle management via `AzureKeyVaultSync` CRD
2. **Annotation Mode**: Enhancement of manually created SPCs

### Mode Detection

The controller determines mode based on SPC ownership:

```
When reconciling a SecretProviderClass:

1. Check OwnerReferences
   ├─ Has owner: kind=AzureKeyVaultSync
   │  └─ CRD MODE: Skip (CRD controller manages it)
   │
   └─ No AKV owner
      ├─ Has annotation: azure-keyvault-sync/enabled=true
      │  └─ ANNOTATION MODE: Enhance SPC
      │
      └─ No annotation
         └─ IGNORE: Not managed by us
```

### CRD Mode (Full Lifecycle)

**Trigger**: User creates `AzureKeyVaultSync` CRD

**Flow**:
```
1. User creates AzureKeyVaultSync CRD
   ↓
2. Controller reconciles CRD
   ├─ Reads Azure Key Vault secrets with tags
   ├─ Applies filters (if specified)
   ├─ Generates complete SPC specification
   ├─ Sets ownerReference on SPC
   └─ Creates or updates SPC
   ↓
3. Azure CSI Driver (independent)
   ├─ Pod mounts volume
   ├─ Reads SPC configuration
   └─ Creates Kubernetes Secrets (for secretObjects)
   ↓
4. Secret Annotation Controller (shared)
   ├─ Watches Secret creation
   ├─ Finds matching SPC
   ├─ Extracts annotations from SPC metadata
   └─ Patches Secret with annotations
```

**Characteristics**:
- Controller **creates** the SPC
- Controller **owns** the SPC (via ownerReference)
- Controller **updates** SPC when vault changes
- Controller **deletes** SPC when CRD deleted (if deletePolicy: Cascade)
- User never manually edits SPC

**Who Manages What**:

| Resource | Creator | Owner | Updates | Deletes |
|----------|---------|-------|---------|---------|
| AzureKeyVaultSync | User | User | User | User |
| SecretProviderClass | Controller | CRD | Controller | CRD (cascade) |
| Secret | CSI Driver | CSI Driver | CSI Driver | CSI Driver |
| Secret annotations | Controller | - | Controller | - |

### Annotation Mode (Enhancement Only)

**Trigger**: User creates SPC with annotation `azure-keyvault-sync/enabled: "true"`

**Flow**:
```
1. User manually creates SecretProviderClass
   metadata:
     annotations:
       azure-keyvault-sync/enabled: "true"
       azure-keyvault-sync/respect-tags: "true"
   spec:
     parameters:
       keyvaultName: my-vault
       objects: |
         array:
           - objectName: secret1
   ↓
2. Controller reconciles SPC
   ├─ Reads Azure Key Vault secrets with tags
   ├─ Filters by SPC labels (if respect-tags: true)
   ├─ Embeds annotations in SPC metadata
   └─ Updates SPC (adds annotations, may update objects array)
   ↓
3. Azure CSI Driver creates Secrets
   ↓
4. Secret Annotation Controller patches Secrets
```

**Characteristics**:
- User **creates** the SPC manually
- User **owns** the SPC (can edit it)
- Controller **enhances** the SPC (adds metadata)
- Controller does **not** delete SPC when controller removed
- Used for gradual adoption or specific use cases

**Who Manages What**:

| Resource | Creator | Owner | Updates | Deletes |
|----------|---------|-------|---------|---------|
| SecretProviderClass | User | User | User + Controller | User |
| Secret | CSI Driver | CSI Driver | CSI Driver | CSI Driver |
| Secret annotations | Controller | - | Controller | - |

### Coexistence

Both modes can exist in the same cluster/namespace:

```yaml
# Same namespace: default

# CRD Mode
apiVersion: azure-keyvault-sync.io/v1alpha1
kind: AzureKeyVaultSync
metadata:
  name: app-a-secrets
  namespace: default
spec:
  keyvaultName: vault-a
  # ... generates SPC named "app-a-secrets"

---
# Annotation Mode
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: app-b-secrets
  namespace: default
  annotations:
    azure-keyvault-sync/enabled: "true"
spec:
  parameters:
    keyvaultName: vault-b
  # ... enhanced by controller
```

**No conflicts**: Mode detection ensures each SPC is managed by only one mechanism.

### When to Use Each Mode

#### Use CRD Mode When:
- ✅ Starting fresh with new secrets
- ✅ Want full automation
- ✅ Managing many secrets from same vault
- ✅ Using GitOps workflows
- ✅ Azure vault is your source of truth
- ✅ Want automatic updates when vault changes

#### Use Annotation Mode When:
- ✅ Already have manually created SPCs
- ✅ Need fine control over SPC spec
- ✅ Gradual migration to controller
- ✅ Complex SPC configurations
- ✅ Mixed management model (some manual, some automated)

---

## Resource Lifecycle

### CRD Creation

**User Action**: Create `AzureKeyVaultSync` resource

**Controller Actions**:
1. Validate CRD spec fields
2. Connect to Azure Key Vault using provided credentials
3. List all secrets in vault
4. Apply filters (if specified)
5. Read tags from each secret
6. Generate SPC specification:
   - Build `objects` array from filtered secrets
   - Build `secretObjects` array from secrets with `secret-object: "true"`
   - Embed annotations from `k8s-annotation.*` tags
7. Set `ownerReference` pointing to CRD (enables cascade deletion)
8. Create `SecretProviderClass` in same namespace with same name as CRD
9. Update CRD status with results

**Result**: SPC exists, ready for CSI Driver to mount

### SPC Generation Details

**Objects Array**: All filtered secrets appear here (required for CSI Driver to sync content)

```yaml
spec:
  parameters:
    objects: |
      array:
        - objectName: secret1
          objectType: secret
        - objectName: secret2
          objectType: secret
```

**SecretObjects Array**: Only secrets with `secret-object: "true"` tag

```yaml
spec:
  secretObjects:
    - secretName: secret1-secret  # or custom name from tag
      type: Opaque
      data:
        - objectName: secret1
          key: value
```

**Annotations**: Per-secret annotations embedded with prefix

```yaml
metadata:
  annotations:
    secret-metadata.azure-keyvault-sync.io/secret1.owner: "team-a"
    secret-metadata.azure-keyvault-sync.io/secret1.reflector/allowed: "true"
    secret-metadata.azure-keyvault-sync.io/secret2.owner: "team-b"
```

### Vault Updates

**Trigger**: Vault secrets or tags change

**Reconciliation**: Controller periodically re-reads vault (default: 5 minutes)

**Controller Actions**:
1. Re-read vault secrets and tags
2. Re-apply filters
3. Re-generate SPC specification
4. Compare with existing SPC
5. If different, update SPC
6. Update CRD status

**Effects**:
- New secrets → Added to `objects` array
- Deleted secrets → Removed from `objects` array
- Tag changes → Annotations updated in SPC
- Filter mismatch → Secret removed from sync

**Note**: CSI Driver handles content updates independently when pods mount volumes.

### CRD Updates

**User Action**: Edit `AzureKeyVaultSync` spec

**Supported Updates**:
- ✅ `filters`: Immediately re-evaluates which secrets to sync
- ✅ `deletePolicy`: Takes effect on next CRD deletion
- ❌ `keyvaultName`: Not recommended (creates entirely different SPC)
- ❌ `tenantId`, `clientID`: Requires controller restart for credential reload

**Controller Actions**:
1. Detect spec change (via `observedGeneration`)
2. Re-reconcile as if newly created
3. Update SPC to match new spec
4. Update status

### CRD Deletion

**User Action**: Delete `AzureKeyVaultSync` resource

**Behavior depends on `deletePolicy`**:

#### DeletePolicy: Cascade (Default)

```yaml
spec:
  deletePolicy: Cascade
```

**Flow**:
```
1. User deletes AzureKeyVaultSync CRD
   ↓
2. Kubernetes cascade deletion (ownerReference)
   ├─ Deletes SecretProviderClass
   │  ↓
   │  └─ CSI Driver stops managing volume mounts
   ↓
3. Secrets remain (not owned by SPC)
   - Pods continue using existing Secrets
   - No new mounts possible
   - Secrets must be manually deleted if desired
```

**Use Case**: Normal operation, clean cleanup

#### DeletePolicy: Orphan

```yaml
spec:
  deletePolicy: Orphan
```

**Flow**:
```
1. User deletes AzureKeyVaultSync CRD
   ↓
2. SPC has no ownerReference
   ├─ SecretProviderClass remains
   ├─ Secrets remain
   └─ Everything continues working
```

**Use Case**: Migrating away from CRD management while keeping secrets operational

**Warning**: Orphaned SPCs are no longer managed. Future updates require manual intervention.

### Secret Creation (CSI Driver)

**Trigger**: Pod mounts volume using SPC

**Flow**:
```
1. Pod spec references SPC:
   volumes:
     - name: secrets
       csi:
         driver: secrets-store.csi.k8s.io
         volumeAttributes:
           secretProviderClass: flow-staging-secrets
   ↓
2. CSI Driver reads SPC
   ├─ Mounts secrets as volume files
   └─ Creates Kubernetes Secrets (for secretObjects)
   ↓
3. Controller watches Secret creation
   ├─ Finds matching SPC
   ├─ Extracts annotations for this secret
   └─ Patches Secret with annotations
```

**Note**: Secrets are created **by CSI Driver**, not by our controller.

### Secret Annotation Patching

**Trigger**: Secret created by CSI Driver

**Controller Actions**:
1. Detect Secret creation (watch event)
2. Check if Secret was created by CSI Driver (label check)
3. Find SPC that references this Secret (match via `secretObjects[].secretName`)
4. Extract vault secret name (`objectName` from `secretObjects[].data`)
5. Look up annotations in SPC metadata with prefix `secret-metadata.../<objectName>.`
6. Strip prefix to get final annotation keys
7. Patch Secret with annotations (JSON Patch)
8. Emit event and update metrics

**Example**:

```yaml
# SPC has this annotation
metadata:
  annotations:
    secret-metadata.azure-keyvault-sync.io/db-password.owner: "team-backend"

# Secret created by CSI Driver
apiVersion: v1
kind: Secret
metadata:
  name: db-creds
type: Opaque

# Controller patches to:
apiVersion: v1
kind: Secret
metadata:
  name: db-creds
  annotations:
    owner: "team-backend"  # ← Added by controller
type: Opaque
```

---

## Configuration Examples

### Example 1: Minimal CRD

**Use Case**: Single vault, sync all secrets, create secretObjects for tagged secrets

```yaml
apiVersion: azure-keyvault-sync.io/v1alpha1
kind: AzureKeyVaultSync
metadata:
  name: my-app-secrets
  namespace: default
spec:
  keyvaultName: my-app-vault
  tenantId: 12345678-1234-1234-1234-123456789012
  clientID: 87654321-4321-4321-4321-210987654321
  serviceAccount: my-app-sa
```

**Behavior**:
- Syncs **all** secrets from `my-app-vault`
- Respects `secret-object` tags for secretObject creation
- Cascade deletes SPC when CRD deleted

**Generated SPC Name**: `my-app-secrets` (matches CRD name)

---

### Example 2: Filtered Secrets (Multi-Tenant Vault)

**Use Case**: Shared vault with multiple services, filter by service and environment

**Vault Contents**:
```
Secret: db-password-flow
Tags:
  service: flow
  environment: staging
  secret-object: "true"

Secret: db-password-backend
Tags:
  service: backend
  environment: staging
  secret-object: "true"

Secret: api-key-flow
Tags:
  service: flow
  environment: prod
  secret-object: "true"
```

**CRD Configuration**:
```yaml
apiVersion: azure-keyvault-sync.io/v1alpha1
kind: AzureKeyVaultSync
metadata:
  name: flow-staging-secrets
  namespace: default
  labels:
    service: flow
    environment: staging
spec:
  keyvaultName: company-shared-vault
  tenantId: 12345678-1234-1234-1234-123456789012
  clientID: 87654321-4321-4321-4321-210987654321
  serviceAccount: flow-staging-sa

  # Only sync secrets matching BOTH tags
  filters:
    service: flow
    environment: staging
```

**Result**:
- ✅ Syncs: `db-password-flow` (matches both filters)
- ❌ Ignores: `db-password-backend` (service doesn't match)
- ❌ Ignores: `api-key-flow` (environment doesn't match)

---

### Example 3: Orphan Deletion Policy

**Use Case**: Migrating away from CRD management but keeping secrets operational

```yaml
apiVersion: azure-keyvault-sync.io/v1alpha1
kind: AzureKeyVaultSync
metadata:
  name: legacy-secrets
  namespace: default
spec:
  keyvaultName: legacy-vault
  tenantId: 12345678-1234-1234-1234-123456789012
  clientID: 87654321-4321-4321-4321-210987654321
  serviceAccount: legacy-sa

  # SPC will be orphaned on CRD deletion
  deletePolicy: Orphan
```

**Behavior**:
- When CRD is deleted, SPC remains
- SPC is no longer managed by controller
- Useful for migration scenarios

---

### Example 4: Annotation Mode (Manual SPC)

**Use Case**: Existing SPC, want to add annotation sync

```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: manual-spc
  namespace: default
  annotations:
    # Opt-in to controller enhancement
    azure-keyvault-sync/enabled: "true"
    # Filter vault secrets by SPC labels
    azure-keyvault-sync/respect-tags: "true"
  labels:
    service: backend
    environment: prod
spec:
  provider: azure
  parameters:
    keyvaultName: backend-vault
    tenantId: 12345678-1234-1234-1234-123456789012
    clientID: 87654321-4321-4321-4321-210987654321
    objects: |
      array:
        - objectName: api-key
          objectType: secret
  secretObjects:
    - secretName: api-key-secret
      type: Opaque
      data:
        - objectName: api-key
          key: key
```

**Controller Behavior**:
- Reads vault `backend-vault`
- Filters secrets matching labels `service: backend`, `environment: prod`
- Embeds annotations from vault tags into SPC
- Does NOT modify `objects` or `secretObjects` arrays
- Patches Secrets with annotations

---

### Example 5: Dual Mode in Same Namespace

**Use Case**: Gradual migration - some secrets via CRD, some manual

```yaml
# CRD-managed secrets
apiVersion: azure-keyvault-sync.io/v1alpha1
kind: AzureKeyVaultSync
metadata:
  name: new-app-secrets
  namespace: default
spec:
  keyvaultName: new-app-vault
  tenantId: 12345678-1234-1234-1234-123456789012
  clientID: 87654321-4321-4321-4321-210987654321
  serviceAccount: new-app-sa
  filters:
    app: new-app

---
# Annotation-enhanced manual SPC
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: legacy-app-secrets
  namespace: default
  annotations:
    azure-keyvault-sync/enabled: "true"
spec:
  provider: azure
  parameters:
    keyvaultName: legacy-vault
    objects: |
      array:
        - objectName: old-secret
```

**Result**: Both modes coexist peacefully, no conflicts.

---

## User Workflows

### Quick Start: CRD Mode

**Goal**: Sync all secrets from a vault to Kubernetes

**Prerequisites**:
- Azure Key Vault created
- ServiceAccount with Azure workload identity configured
- CRD installed in cluster

**Steps**:

1. **Tag secrets in Azure Key Vault**:
```bash
# For each secret that should create a Kubernetes Secret:
az keyvault secret set-attribute \
  --vault-name my-vault \
  --name database-password \
  --tags secret-object=true \
         k8s-annotation.owner=team-backend
```

2. **Create AzureKeyVaultSync CRD**:
```yaml
apiVersion: azure-keyvault-sync.io/v1alpha1
kind: AzureKeyVaultSync
metadata:
  name: my-app-secrets
  namespace: default
spec:
  keyvaultName: my-vault
  tenantId: <your-tenant-id>
  clientID: <your-client-id>
  serviceAccount: my-app-sa
```

3. **Apply CRD**:
```bash
kubectl apply -f azure-keyvault-sync.yaml
```

4. **Verify SPC created**:
```bash
kubectl get secretproviderclass my-app-secrets
```

5. **Mount in pod** (triggers Secret creation):
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-app
spec:
  serviceAccountName: my-app-sa
  volumes:
    - name: secrets
      csi:
        driver: secrets-store.csi.k8s.io
        volumeAttributes:
          secretProviderClass: my-app-secrets
  containers:
    - name: app
      image: my-app:latest
      volumeMounts:
        - name: secrets
          mountPath: /mnt/secrets
```

6. **Verify Secrets created with annotations**:
```bash
kubectl get secret database-password-secret -o yaml
# Should show annotations from vault tags
```

---

### Quick Start: Annotation Mode

**Goal**: Add annotation sync to existing manual SPC

**Prerequisites**:
- Existing SecretProviderClass
- Secrets tagged in Azure Key Vault

**Steps**:

1. **Add opt-in annotation to existing SPC**:
```bash
kubectl annotate secretproviderclass my-spc \
  azure-keyvault-sync/enabled=true
```

2. **Verify controller enhances SPC**:
```bash
kubectl get secretproviderclass my-spc -o yaml
# Should show embedded annotations in metadata
```

3. **Remount pod** (if already running) to trigger Secret recreation with annotations.

---

### Filtering Secrets by Tags

**Scenario**: Shared vault with 100 secrets, only want subset

**Vault Organization**:
```
All secrets tagged with:
- service: <service-name>
- environment: <staging|prod>
```

**CRD Configuration**:
```yaml
apiVersion: azure-keyvault-sync.io/v1alpha1
kind: AzureKeyVaultSync
metadata:
  name: backend-prod-secrets
  namespace: production
spec:
  keyvaultName: company-shared-vault
  tenantId: ...
  clientID: ...
  serviceAccount: backend-prod-sa

  # Only sync secrets for backend service in prod
  filters:
    service: backend
    environment: prod
```

**Result**: Only secrets with BOTH tags `service: backend` AND `environment: prod` are synced.

---

### Managing SecretObjects

**Goal**: Control which vault secrets become Kubernetes Secrets vs volume files only

**Tag Strategy**:

**Volume-only secret** (no Kubernetes Secret):
```bash
az keyvault secret set-attribute \
  --vault-name my-vault \
  --name config-file \
  --tags k8s-annotation.owner=team-a
# No secret-object tag → appears in volume only
```

**Kubernetes Secret** (secretObject):
```bash
az keyvault secret set-attribute \
  --vault-name my-vault \
  --name api-key \
  --tags secret-object=true \
         k8s-annotation.owner=team-a
# secret-object=true → becomes Kubernetes Secret
```

**Custom Secret Name**:
```bash
az keyvault secret set-attribute \
  --vault-name my-vault \
  --name long-descriptive-vault-name \
  --tags secret-object=true \
         secret-object-name=short-name \
         k8s-annotation.owner=team-a
# Kubernetes Secret named "short-name" instead of "long-descriptive-vault-name-secret"
```

**Generated SPC**:
```yaml
spec:
  parameters:
    objects: |
      array:
        - objectName: config-file       # Volume only
        - objectName: api-key            # Volume + Secret
        - objectName: long-descriptive-vault-name  # Volume + Secret
  secretObjects:
    - secretName: api-key-secret        # Default name
      data:
        - objectName: api-key
          key: value
    - secretName: short-name            # Custom name
      data:
        - objectName: long-descriptive-vault-name
          key: value
```

---

## Design Decisions

### Why We Removed `respectTags` from CRD

**Context**: Annotation mode has `azure-keyvault-sync/respect-tags` annotation.

**Decision**: CRD mode does NOT have a `respectTags` field.

**Rationale**:

1. **CRD Mode Implies Tag-Based Management**: The entire premise of the CRD is managing secrets via Azure tags. Not respecting tags contradicts the design.

2. **Filters Field Provides Control**: The optional `filters` field naturally controls tag-based filtering:
   - `filters` present → Filter by tags
   - `filters` absent → Include all secrets
   - No need for separate boolean flag

3. **Tag Features Always Active**: In CRD mode:
   - `secret-object` tag is always respected
   - `k8s-annotation.*` tags are always respected
   - Only filtering tags are optional

4. **Simpler API**: One less field reduces cognitive load and potential confusion.

5. **Annotation Mode Still Has It**: Manual SPCs can still opt-in/out of tag filtering, which makes sense for gradual adoption.

**Example**:

```yaml
# CRD Mode - tags always respected
spec:
  keyvaultName: my-vault
  filters:              # ← Presence controls filtering
    service: myapp

# Annotation Mode - explicit opt-in
metadata:
  annotations:
    azure-keyvault-sync/respect-tags: "true"  # ← Boolean flag needed
```

---

### Why Namespace and Name Match CRD Metadata

**Decision**: Generated SPC always has:
- **Name**: Same as CRD name
- **Namespace**: Same as CRD namespace

**Rationale**:

1. **Predictable**: User knows exactly what SPC will be created
2. **1:1 Mapping**: One CRD creates one SPC, clear ownership
3. **No Naming Conflicts**: Kubernetes enforces uniqueness in namespace
4. **Easier Troubleshooting**: `kubectl get akv my-app` and `kubectl get spc my-app` refer to related resources
5. **OwnerReference Works**: Name matching simplifies ownership tracking

**Alternative Considered**: Allow `spec.spcName` override.

**Rejected Because**:
- Adds complexity
- Breaks 1:1 mental model
- Unclear what happens on rename
- Can be added later if truly needed

---

### Why Filters Are Optional

**Decision**: `spec.filters` field is optional (can be omitted)

**Rationale**:

1. **Simple Use Case**: Many users have dedicated vaults (e.g., `app-a-vault`, `app-b-vault`)
   - No filtering needed → sync everything
   - Omitting field keeps config minimal

2. **Multi-Tenant Support**: Organizations with shared vaults need filtering
   - Add `filters` field only when needed
   - Supports complex scenarios without forcing complexity on simple ones

3. **Migration Path**: Existing users may have vaults without consistent tagging
   - Can start without filters (sync all)
   - Add filtering later as they adopt tagging standards

4. **Backwards Compatible Future**: If we add default filtering behavior later, omitted field = existing behavior

**Example**:

```yaml
# Simple: Dedicated vault
spec:
  keyvaultName: app-a-vault
  # No filters → sync all secrets

# Complex: Shared vault
spec:
  keyvaultName: shared-vault
  filters:
    service: app-a
    environment: prod
```

---

### Tag-Based SecretObject Opt-In

**Decision**: Secrets become secretObjects only if tagged `secret-object: "true"`

**Rationale**:

1. **Explicit Intent**: Not all vault secrets should be Kubernetes Secrets
   - Configuration files → Volume mount only
   - Credentials → Kubernetes Secret (for env vars, etc.)
   - User explicitly marks which is which

2. **Volume vs Secret Difference**:
   - **Volume**: File in pod filesystem (`/mnt/secrets/db-password`)
   - **Secret**: K8s resource, can be referenced in env vars, used by other controllers
   - Different use cases, explicit choice

3. **Prevents Bloat**: Large vaults with 100s of secrets don't create 100s of K8s Secrets
   - Only create Secrets where actually needed
   - Reduces API server load

4. **Security**: Secrets in K8s are more visible (RBAC, auditing) than volume files
   - Opt-in ensures secrets aren't exposed unintentionally

**Alternative Considered**: All secrets become secretObjects by default.

**Rejected Because**:
- Creates unnecessary K8s Secrets
- No way to opt-out (tag absence is not explicit)
- Violates least-privilege principle

**Tag Design**:
```
secret-object: "true"   → Explicit opt-in
(no tag)                → Volume only (safe default)
secret-object: "false"  → Explicit opt-out (optional, same as no tag)
```

---

**Document Version**: 1.0
**Last Updated**: 2025-11-01
**Status**: Design Specification - Ready for Implementation
