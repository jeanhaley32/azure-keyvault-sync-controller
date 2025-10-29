# Azure Key Vault Sync Controller Documentation

This directory contains all documentation for the Azure Key Vault Sync Controller project.

## Documentation Structure

### Root Documentation
- [Main README](../README.md) - Project overview, installation, and usage
- [CHANGELOG](../CHANGELOG.md) - Version history and release notes
- [ROADMAP](../ROADMAP.md) - Future development plans
- [LICENSE](../LICENSE) - Apache 2.0 license

### Planning Documents (`planning/`)
Historical planning and implementation documents:
- [architecture-improvements.md](planning/architecture-improvements.md) - Architectural enhancement proposals
- [azure-token-exchange.md](planning/azure-token-exchange.md) - Azure AD token exchange implementation
- [keyvault-integration.md](planning/keyvault-integration.md) - Key Vault integration design
- [namespace-scoping.md](planning/namespace-scoping.md) - Namespace-scoped deployment planning
- [rate-limiting-implementation.md](planning/rate-limiting-implementation.md) - Rate limiting design and implementation
- [secretproviderclass-updates.md](planning/secretproviderclass-updates.md) - SecretProviderClass update handling
- [token-acquisition-implementation.md](planning/token-acquisition-implementation.md) - Token acquisition flow
- [token-acquisition.md](planning/token-acquisition.md) - Token management design
- [workflow-blindspot-fixes.md](planning/workflow-blindspot-fixes.md) - Workflow improvement proposals

### Design Documents (`design/`)
Architecture and design decisions:
- [presentation.md](design/presentation.md) - Project presentation and overview
- [rate-limiting.md](design/rate-limiting.md) - Rate limiting design details
- [security-analysis.md](design/security-analysis.md) - Security architecture analysis

### Session Notes (`sessions/`)
Development session summaries and notes:
- [session-2025-10-25.md](sessions/session-2025-10-25.md) - Development session from October 25, 2025

### Examples (`../examples/`)
Example configurations and usage patterns:
- [examples/README.md](../examples/README.md) - Example SecretProviderClass configurations

## Documentation Guidelines

### Creating New Documents
- **Planning docs**: Use `docs/planning/` for feature planning and proposals
- **Design docs**: Use `docs/design/` for architectural decisions and analyses
- **Session notes**: Use `docs/sessions/` for development session summaries
- **Examples**: Use `examples/` for configuration examples

### Naming Conventions
- Use lowercase with hyphens: `feature-name.md`
- Be descriptive: `rate-limiting-implementation.md` not `rl.md`
- Include dates for session notes: `session-YYYY-MM-DD.md`

### Markdown Standards
- Use ATX-style headers (`#` not underlines)
- Include table of contents for documents > 200 lines
- Use code blocks with language specifications
- Link to related documents when relevant

## Contributing Documentation

When adding new features:
1. Update relevant planning documents or create new ones
2. Update the main README if user-facing changes
3. Add examples to `examples/` if applicable
4. Update CHANGELOG with your changes
5. Create session notes if significant architectural decisions were made

## Quick Links

### For Operators
- [Installation Guide](../README.md#installation)
- [Configuration Reference](../README.md#configuration)
- [Examples](../examples/README.md)

### For Developers
- [Architecture Overview](design/security-analysis.md)
- [Testing Guide](../README.md#testing)
- [Contributing Guidelines](../README.md#contributing)

### For Decision Makers
- [Project Roadmap](../ROADMAP.md)
- [Security Analysis](design/security-analysis.md)
- [Changelog](../CHANGELOG.md)
