# Contributing to Azure Key Vault Sync Controller

Thank you for considering contributing to the Azure Key Vault Sync Controller! This document provides guidelines and instructions for contributing to the project.

## Table of Contents

1. [Code of Conduct](#code-of-conduct)
2. [Getting Started](#getting-started)
3. [Development Setup](#development-setup)
4. [Making Changes](#making-changes)
5. [Testing](#testing)
6. [Documentation](#documentation)
7. [Submitting Changes](#submitting-changes)
8. [Code Review Process](#code-review-process)

---

## Code of Conduct

This project follows a professional and respectful collaboration model:

- Be respectful and constructive in all interactions
- Focus on what is best for the project and community
- Accept constructive criticism gracefully
- Show empathy towards other community members

---

## Getting Started

### Prerequisites

**Required:**
- Go 1.22 or later
- Access to a Kubernetes cluster (for integration testing)
- Git

**Recommended:**
- Docker (for building container images)
- Azure subscription (for end-to-end testing)
- kubectl configured for your cluster

### Understanding the Codebase

Before contributing, familiarize yourself with:

1. **[Architecture](docs/developer/architecture.md)** - System design and component overview
2. **[ROADMAP](ROADMAP.md)** - Implementation history and current status
3. **[Testing Guide](docs/developer/TESTING_GUIDE.md)** - How to write tests

**Key Concepts:**
- Two-tier reconciliation architecture (Controller + Secret Watcher)
- Service account impersonation for Azure authentication
- Work queue pattern with exponential backoff
- Metadata synchronization from vault tags to Kubernetes Secrets

---

## Development Setup

### 1. Clone the Repository

```bash
git clone https://github.com/jeanhaley32/azure-keyvault-sync-controller.git
cd azure-keyvault-sync-controller
```

### 2. Install Dependencies

```bash
# Install Go dependencies
go mod download

# Verify dependencies
go mod verify
```

### 3. Run Tests Locally

```bash
# Run all tests
make test

# Run tests with coverage
make test-coverage

# Run tests in verbose mode
make test-verbose

# Run specific package tests
go test ./internal/cache -v
```

### 4. Run the Controller Locally

```bash
# Connect to your Kubernetes cluster
export KUBECONFIG=~/.kube/config

# Run the controller (uses local kubeconfig)
go run .

# Run with debug logging
LOG_LEVEL=DEBUG go run .
```

The controller will:
- Connect to your cluster using `~/.kube/config`
- Watch SecretProviderClass resources
- Log to stdout

---

## Making Changes

### Branch Naming

Use descriptive branch names following these patterns:

- `feature/description` - New features
- `fix/description` - Bug fixes
- `refactor/description` - Code refactoring
- `docs/description` - Documentation updates
- `test/description` - Test improvements

**Examples:**
- `feature/add-prometheus-metrics`
- `fix/label-removal-bug`
- `docs/update-architecture-diagram`

### Commit Messages

Write clear, descriptive commit messages:

**Format:**
```
<type>: <short summary>

<optional detailed description>

<optional footer>
```

**Types:**
- `feat:` - New feature
- `fix:` - Bug fix
- `docs:` - Documentation changes
- `test:` - Test additions or changes
- `refactor:` - Code refactoring
- `chore:` - Build process, dependencies, etc.

**Examples:**
```
feat: add Prometheus metrics endpoint

Add /metrics endpoint exposing:
- reconcile_duration_seconds histogram
- reconcile_total counter by result
- token_cache_hit_ratio gauge

Closes #42
```

```
fix: resolve label removal bug when vault tags deleted

Early return at controller.go:1272-1275 prevented metadata
removal logic from executing. Removed buggy check.

Fixes #43
```

### Code Style

**Go Code:**
- Follow [Effective Go](https://go.dev/doc/effective_go)
- Use `gofmt` for formatting (required)
- Use meaningful variable names
- Add comments for exported functions
- Keep functions focused and concise

**Example:**
```go
// ExtractClientID extracts the Azure client ID from SecretProviderClass parameters.
// Returns an error if the clientID field is missing or empty.
func ExtractClientID(obj *unstructured.Unstructured) (string, error) {
    clientID, found, err := unstructured.NestedString(obj.Object, "spec", "parameters", "clientID")
    if err != nil {
        return "", fmt.Errorf("error accessing spec.parameters.clientID: %w", err)
    }
    if !found || clientID == "" {
        return "", fmt.Errorf("clientID not found in spec.parameters")
    }
    return clientID, nil
}
```

**Project-Specific Conventions:**
- Use structured logging with `slog`
- Prefer explicit error handling over panic
- Use context for cancellation and timeouts
- Thread-safe operations for concurrent access

### Adding New Features

1. **Check existing issues** - Search for related discussions
2. **Open an issue first** - Discuss the feature before coding
3. **Follow architecture** - Maintain consistency with existing design
4. **Add tests** - All new code must have test coverage
5. **Update documentation** - Keep docs in sync with code

---

## Testing

### Test Requirements

All contributions must include appropriate tests:

**Unit Tests:**
- Test individual functions and methods
- Use fake clients for external dependencies
- Aim for 70%+ coverage for new code

**Integration Tests:**
- Test component interactions
- Use fake Kubernetes clients
- Mock Azure API responses

See [Testing Guide](docs/developer/TESTING_GUIDE.md) for detailed instructions.

### Writing Tests

**Test File Structure:**
```go
package update

import (
    "testing"
    "github.com/stretchr/testify/assert"
)

func TestPatchSecretProviderClass(t *testing.T) {
    t.Run("successfully patches objects", func(t *testing.T) {
        // Arrange
        fakeClient := spcfake.NewSimpleClientset()
        // ... setup

        // Act
        err := PatchSecretProviderClass(ctx, fakeClient, ...)

        // Assert
        assert.NoError(t, err)
        // ... verify behavior
    })

    t.Run("returns error when resource not found", func(t *testing.T) {
        // ...
    })
}
```

**Best Practices:**
- Use table-driven tests for multiple scenarios
- Use subtests (`t.Run`) for logical grouping
- Test both success and error paths
- Use descriptive test names
- Clean up resources in tests

### Running Tests

```bash
# Run all tests
make test

# Run with race detector
go test -race ./...

# Run with coverage
make test-coverage

# Run specific package
go test ./internal/azure -v

# Run specific test
go test ./internal/update -run TestPatchSecretProviderClass -v
```

---

## Documentation

### When to Update Documentation

**Always update when:**
- Adding new features or APIs
- Changing behavior or configuration
- Fixing bugs that affect usage
- Adding new command-line flags or environment variables

**Which docs to update:**
- **[README.md](README.md)** - User-facing changes
- **[docs/user/TESTING.md](docs/user/TESTING.md)** - Testing procedures
- **[docs/developer/architecture.md](docs/developer/architecture.md)** - Architecture changes
- **[CHANGELOG.md](CHANGELOG.md)** - All changes
- **[MIGRATION.md](MIGRATION.md)** - Breaking changes

### Documentation Standards

- Use markdown for all documentation
- Include code examples where applicable
- Add mermaid diagrams for complex flows
- Link to related documents
- Keep line length under 120 characters

See [docs/README.md](docs/README.md) for complete documentation standards.

---

## Submitting Changes

### Pull Request Process

1. **Create a feature branch** from `staging`
   ```bash
   git checkout staging
   git pull origin staging
   git checkout -b feature/my-feature
   ```

2. **Make your changes** following the guidelines above

3. **Run tests and formatting**
   ```bash
   make test
   gofmt -w .
   ```

4. **Commit your changes**
   ```bash
   git add .
   git commit -m "feat: add my feature"
   ```

5. **Push to your fork**
   ```bash
   git push origin feature/my-feature
   ```

6. **Open a Pull Request**
   - Target branch: `staging` (not `main`)
   - Fill out the PR template
   - Link related issues

### Pull Request Template

```markdown
## Summary
Brief description of what this PR does.

## Changes Made
- Change 1
- Change 2
- Change 3

## Testing
- [ ] Unit tests added/updated
- [ ] Integration tests added/updated
- [ ] Manual testing performed
- [ ] All tests passing

## Documentation
- [ ] README updated
- [ ] CHANGELOG updated
- [ ] Architecture docs updated (if applicable)
- [ ] Migration guide updated (if breaking change)

## Related Issues
Fixes #123
Relates to #456
```

### Pull Request Checklist

Before submitting, ensure:
- [ ] Tests pass (`make test`)
- [ ] Code is formatted (`gofmt -w .`)
- [ ] Documentation is updated
- [ ] CHANGELOG.md is updated
- [ ] Commit messages are clear
- [ ] PR targets `staging` branch
- [ ] PR description is complete

---

## Code Review Process

### What to Expect

**Review Timeline:**
- Initial review: 1-3 business days
- Follow-up reviews: 1-2 business days
- Approval required from at least one maintainer

**Review Focus:**
- Code correctness and clarity
- Test coverage and quality
- Documentation completeness
- Consistency with architecture
- Security considerations

### Responding to Feedback

- Be responsive to review comments
- Ask questions if feedback is unclear
- Make requested changes promptly
- Use GitHub suggestions for small changes
- Mark conversations as resolved after addressing

### After Approval

Once approved, your PR will be:
1. Merged to `staging` for integration testing
2. Tested in staging environment
3. Merged to `main` for production release
4. Tagged with semantic version (if applicable)

---

## Development Workflow

### Typical Development Cycle

```
1. Open issue or discuss feature
2. Create feature branch from staging
3. Develop with TDD (Test-Driven Development)
4. Run tests frequently (make test)
5. Update documentation
6. Submit PR to staging
7. Address code review feedback
8. PR merged to staging
9. Verify in staging environment
10. Merged to main for release
```

### Branch Strategy

```
main
  └── Latest stable release
      ↑
staging
  └── Integration testing
      ↑
feature/my-feature
  └── Development work
```

---

## Getting Help

### Resources

- **Architecture Questions:** [docs/developer/architecture.md](docs/developer/architecture.md)
- **Testing Help:** [docs/developer/TESTING_GUIDE.md](docs/developer/TESTING_GUIDE.md)
- **Bug Reports:** Open a GitHub issue
- **Feature Discussions:** Open a GitHub discussion

### Communication

- **GitHub Issues:** Bug reports, feature requests
- **GitHub Discussions:** General questions, ideas
- **Pull Request Comments:** Code-specific discussions
- **CHANGELOG:** Track project progress

---

## Additional Resources

- [Go Documentation](https://go.dev/doc/)
- [Kubernetes Client-Go](https://github.com/kubernetes/client-go)
- [Azure SDK for Go](https://github.com/Azure/azure-sdk-for-go)
- [Secrets Store CSI Driver](https://secrets-store-csi-driver.sigs.k8s.io/)

---

## License

By contributing, you agree that your contributions will be licensed under the MIT License.

---

**Thank you for contributing!** 🎉

Your contributions help make this project better for everyone.

**Last Updated:** 2025-11-02
