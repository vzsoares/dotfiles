---
name: terraform-init-approva
description: Initializes a Terraform infrastructure for a project following the Approva multi-environment pattern (infra/environments and infra/services). Use when a project needs to set up AWS infrastructure with S3 backend and environment-specific configurations.
---

# Terraform Initialization (Approva Pattern)

This skill scaffolds a Terraform project using the "Approva" pattern: environment-specific folders that call a shared orchestrator module.

## Workflow

1.  **Gather Information**: Ask the user for:
    *   `PROJECT_NAME`: The name of the project (e.g., `core-api`).
    *   `REGION`: AWS Region (default: `us-east-1`).
    *   `DEV_ACCOUNT_ID`: AWS Account ID for Development.
    *   `PROD_ACCOUNT_ID`: AWS Account ID for Production.
2.  **Scaffold Directories**: Create the following structure:
    ```
    infra/
    ├── environments/
    │   ├── dev/
    │   └── prod/
    └── services/
        └── main/
    ```
3.  **Generate Files**: Use [terraform-boilerplate.md](references/terraform-boilerplate.md) to create:
    *   `infra/environments/dev/provider.tf`
    *   `infra/environments/dev/main.tf`
    *   `infra/environments/prod/provider.tf`
    *   `infra/environments/prod/main.tf`
    *   `infra/services/main/main.tf`
    *   `infra/services/main/variables.tf`
    *   `infra/services/main/outputs.tf` (empty)
4.  **Makefile (Optional)**: If no `Makefile` exists, use [makefile-boilerplate.md](references/makefile-boilerplate.md) to create one in the root directory.

## Best Practices

*   **SSM Discovery**: Encourage the use of AWS SSM Parameter Store for cross-service resource discovery instead of hardcoding ARNs.
*   **Modularization**: Keep service-specific logic in `infra/services/[name]` and orchestrate them in `infra/services/main/main.tf`.
*   **ARM64**: Prefer `arm64` for compute resources (ECS, Lambda) when possible for better price/performance.
