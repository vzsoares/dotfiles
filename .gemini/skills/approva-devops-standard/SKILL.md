---
name: approva-devops-standard
description: Setup and management of Approva's standardized DevOps infrastructure, including multi-environment Terraform (S3 backend, ECS on EC2 ARM64), standardized shell scripts for build/push/deploy, and Bitbucket Pipelines integration. Use when initializing a new project's infra or managing existing ECS services.
---

# Approva DevOps Standard

This skill provides the procedural knowledge and boilerplate for Approva's standardized infrastructure and deployment workflows.

## Workflow Summary

1. **Infrastructure Setup**: Implement the modular Terraform structure using S3 for state and DynamoDB for locking.
2. **Resource Discovery**: Use AWS SSM Parameter Store to discover shared resources (VPC, Cluster, Listeners).
3. **Build System**: Use Docker `buildx` with QEMU for ARM64 builds, injecting CodeArtifact tokens.
4. **Deployment**: Orchestrate via `Makefile` and `scripts/` to build, push, apply Terraform, and update ECS.
5. **CI/CD**: Integrate with Bitbucket Pipelines using ARM64 runners.

## Reference Guides

- **Terraform Patterns**: See [references/terraform-patterns.md](references/terraform-patterns.md) for modular service and environment configurations.
- **Shell Scripts**: See [references/scripts-boilerplate.md](references/scripts-boilerplate.md) for standard build/push/update scripts.
- **CI/CD Configuration**: See [references/ci-cd-config.md](references/ci-cd-config.md) for `Makefile` and `bitbucket-pipelines.yml` boilerplate.

## Key Standards

- **Architecture**: `linux/arm64` on EC2 (Shared Cluster).
- **Environment Discovery**: 100% via SSM (`/${stage}/CORE_...`).
- **State Management**: S3 bucket `approva-${stage}-terraform-iac` with key `${project}/terraform.tfstate`.
- **Naming**: Use `${project_name}-${stage}` consistently across ECS, Target Groups, and IAM for environment isolation.
