# Azure Key Vault Sync Controller Documentation

Complete documentation index for the Azure Key Vault Sync Controller project.

## Documentation Structure

```
docs/
├── user/          # User-facing documentation (operators, end-users)
├── developer/     # Developer documentation (contributors, maintainers)
├── design/        # Design documents and presentations
└── archive/       # Historical planning documents (Phases 1-6)
```

---

## For Operators & Users

### Getting Started
- **[Main README](../README.md)** - Project overview, installation, and quick start
- **[Installation Guide](../README.md#quick-start)** - Deploy to your cluster
- **[Configuration Reference](../README.md#configuration)** - Environment variables and settings
- **[Examples](../examples/README.md)** - Sample configurations

### User Guides
- **[Testing Guide](user/TESTING.md)** - Test controller on staging cluster
- **[Tag Filtering Guide](user/tag-filtering-decision-tree.md)** - Understand tag filtering logic
- **[Migration Guide](../MIGRATION.md)** - Upgrade from v1.x to v2.0 (breaking changes)
- **[Namespace-Scoped Deployment](../examples/namespace-scoped/README.md)** - Secure production deployment

### Reference
- **[ROADMAP](../ROADMAP.md)** - Implementation history and future plans
- **[CHANGELOG](../CHANGELOG.md)** - Version history and release notes

---

## For Developers & Contributors

### Architecture & Design
- **[System Architecture](developer/architecture.md)** - Complete system design (Phase 6 complete)
  - Two-tier reconciliation architecture
  - Authentication flow (K8s → Azure AD → Key Vault)
  - Metadata synchronization pipeline
  - Component details and data flow
- **[Security Analysis](developer/security-analysis.md)** - Security architecture and threat model
- **[Rate Limiting Design](developer/rate-limiting.md)** - API protection and throttling

### Development Guides
- **[Contributing Guide](../CONTRIBUTING.md)** - How to contribute to the project
- **[Testing Guide](developer/TESTING_GUIDE.md)** - Writing tests with fake clients

### Design Documents
- **[Project Presentation](design/presentation.md)** - Overview and pitch deck

---

## Historical Documentation

### Archived Planning Documents
All implementation phases (1-6) are now complete. Historical planning documents preserved for reference:

- **[Archive README](archive/README.md)** - Overview of archived documents
- **[archive/planning/](archive/planning/)** - Feature planning from Oct 2024 - Nov 2025
  - Phase 1: Foundation
  - Phase 2: Token Acquisition (K8s + Azure)
  - Phase 3: Key Vault Integration
  - Phase 4: SecretProviderClass Updates
  - Phase 6: Secret Metadata Synchronization (CRD + Two-Tier Architecture)

**Note:** Phase 5 (Production Enhancements) was integrated throughout development rather than as a separate phase.

---

## Quick Navigation by Role

### 🚀 I'm an Operator (deploying/managing)
1. Start: [Main README](../README.md)
2. Install: [Quick Start](../README.md#quick-start)
3. Configure: [Configuration Reference](../README.md#configuration)
4. Test: [Testing Guide](user/TESTING.md)
5. Troubleshoot: [README Troubleshooting](../README.md#troubleshooting)

### 👨‍💻 I'm a Developer (contributing)
1. Start: [Contributing Guide](../CONTRIBUTING.md)
2. Understand: [System Architecture](developer/architecture.md)
3. Code: [Testing Guide](developer/TESTING_GUIDE.md)
4. Review: [Security Analysis](developer/security-analysis.md)

### 🧑‍💼 I'm a Decision Maker (evaluating)
1. Overview: [Main README](../README.md)
2. Features: [ROADMAP](../ROADMAP.md)
3. Security: [Security Analysis](developer/security-analysis.md)
4. History: [CHANGELOG](../CHANGELOG.md)

---

## Documentation Standards

### Creating New Documents

**Location Guidelines:**
- **User-facing docs** → `docs/user/` (operators, testing, configuration)
- **Developer docs** → `docs/developer/` (architecture, design, testing)
- **Design docs** → `docs/design/` (presentations, proposals)
- **Examples** → `examples/` (configuration samples)
- **Root docs** → Project root for high-visibility files (README, CHANGELOG, etc.)

### Naming Conventions
- Use lowercase with hyphens: `feature-name.md`
- Be descriptive: `rate-limiting-implementation.md` not `rl.md`
- Include version/date for time-sensitive docs: `migration-v2.md`

### Markdown Standards
- Use ATX-style headers (`#` not underlines)
- Include table of contents for documents > 200 lines
- Use code blocks with language specifications (```yaml, ```bash, etc.)
- Link to related documents when relevant
- Include status/version in document header when applicable

### When to Update Documentation

**User-facing changes:**
- Update main [README.md](../README.md)
- Add examples to [examples/](../examples/)
- Update [CHANGELOG.md](../CHANGELOG.md)
- Update [user/TESTING.md](user/TESTING.md) if testing procedures change

**Architecture changes:**
- Update [developer/architecture.md](developer/architecture.md)
- Update [developer/security-analysis.md](developer/security-analysis.md) if security impact
- Document design decisions in [design/](design/)

**Breaking changes:**
- Update [MIGRATION.md](../MIGRATION.md)
- Increment version in [CHANGELOG.md](../CHANGELOG.md)
- Add migration examples to [examples/](../examples/)

---

## Project Status

**Current Version:** 2.0 (Phase 6 Complete)
**Status:** Production Ready ✅

All planned implementation phases (1-6) are complete:
- ✅ Phase 1: Foundation (watch, cache, work queue)
- ✅ Phase 2: Token Acquisition (K8s + Azure Workload Identity)
- ✅ Phase 3: Azure Key Vault Integration
- ✅ Phase 4: SecretProviderClass Updates (objects + secretObjects)
- ✅ Phase 6: Secret Metadata Synchronization (annotations, labels, CRD)

See [ROADMAP.md](../ROADMAP.md) for complete implementation history.

---

## Need Help?

- **Usage Questions:** See [README.md](../README.md) and [user/TESTING.md](user/TESTING.md)
- **Bug Reports:** Open an issue on GitHub
- **Feature Requests:** Check [ROADMAP.md](../ROADMAP.md) then open an issue
- **Security Issues:** See [developer/security-analysis.md](developer/security-analysis.md)

---

**Last Updated:** 2025-11-02
**Documentation Version:** 2.0
