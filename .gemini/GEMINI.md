# Gemini Global Memory

## Coding Patterns & Domain Knowledge
- **Mongoose:** Never use `model.save()`. Prefer atomic update operations (`updateOne`, `findOneAndUpdate`) for concurrency and performance.
- **Data Models:** The `identificador` field in `DadosExtraidosSchema` is a polymorphic reference object `{ type: string, value: string }` used to link records to specific entities (like a proponente ID or CPF) within a client's scope.

## Architectural Standards & Deployment Protocols

### 1. ECS Infrastructure (ARM64 Shared)
- **Architecture:** `linux/arm64`
- **Launch Type:** EC2 (Shared Cluster) - *Do not use Fargate.*
- **Resource Discovery:** 100% via SSM Parameters. Do not hardcode ARNs or resource names.
  - Cluster: `/${stage}/CORE_ECS_CLUSTER_NAME`
  - Capacity: `/${stage}/CORE_ECS_CAPACITY_PROVIDER_NAME`
  - Network: `/${stage}/CORE_NETWORK_VPC_ID`
  - ALB: `/${stage}/CORE_ALB_HTTPS_LISTENER_ARN` & `CORE_ALB_HTTP_LISTENER_ARN`

### 2. Build System (Multi-Arch)
- **Tool:** Docker Buildx with QEMU (`tonistiigi/binfmt`).
- **Platform:** `--platform linux/arm64`.
- **Auth:** AWS CodeArtifact token retrieval & injection via `--secret`.
- **Loading:** Use `--load` to move the image to the local Docker daemon after the buildx step.

### 3. Project Structure
- **Makefile:** Standard targets required: `build`, `push`, `deploy`, `update-ecs`.
- **Terraform:** Modular approach. Specifically, isolate IAM permissions in `iam.tf`.

### 4. CI/CD (Bitbucket)
- **Runtime:** `options: runtime: cloud: version: "3"`.
- **Engine:** `DOCKER_BUILDKIT=1`.
- **Flow:** Pipeline steps should invoke `make deploy`.

## Infrastructure Guide

This project uses a modular Terraform structure designed for multi-environment deployments (dev, prod) on AWS. It leverages a shared infrastructure model where services deploy into a pre-existing "Core" ECS Cluster and Network.

### 1. Directory Structure

The `infra/` directory is organized into two main levels: Environments (configurations) and Services (logic).

- **`infra/environments/`**: Entry points for each stage (`dev`, `prod`).
    - `main.tf`: Instantiates the "main" service module.
    - `provider.tf`: Backend & Provider config.
- **`infra/services/`**: Reusable Terraform modules.
    - `main/`: Orchestrator module (calls sub-modules).
    - `ecs-service/`: ECS Service, Task Def, ALB Rules.
    - `gateway/`: API Gateway (HTTP).
    - `iam/`: IAM Roles & Policies.

### 2. Environment Configuration

Each environment directory (e.g., `infra/environments/dev`) contains the specific configuration for that stage.

- **`provider.tf`**:
    - **Backend**: S3 with DynamoDB for state locking.
    - **Key Pattern**: `{domain}/{project}/terraform.tfstate` (e.g., `core-api/terraform.tfstate`).
    - **Bucket naming**: `approva-{stage}-terraform-iac` (e.g. `approva-dev-terraform-iac`, `approva-prod-terraform-iac`).
- **`main.tf`**:
    - Acts as the "root" module. Instantiates the `services/main` module, passing in environment-specific variables like `stage`, `account_id`, and `bucket_documentos`. The "main" service is used to reduce code duplication between environments.

### 3. Services & Modules

- **`services/main/`**: The primary orchestrator. Defines locally scoped variables, calls sub-modules (`iam`, `gateway`, `ecs-service`), and reads shared configuration via SSM.
- **`services/ecs-service/`**: Core compute module.
    - **Discovery**: Uses `aws_ssm_parameter` to find shared resources (`/STAGE/CORE_ECS_CLUSTER_NAME`, etc.).
    - **Routing**: Creates ALB Listener Rule based on Host header.
    - **Compute**: ECS on EC2 (Launch Type) with Capacity Provider strategy.
- **`services/iam/`**: Centralizes IAM roles and policies.

### 4. Key Implementation Patterns

- **Shared Infrastructure via SSM**: Services look up environment details via AWS SSM Parameter Store to decouple from core network creation.
- **API Gateway -> VPC Link -> ALB -> ECS**: Strict traffic flow: Public API Gateway -> VPC Link -> Internal ALB -> ECS Service.
- **Secrets Management**:
    - **Build Time**: Secrets injected.
    - **Runtime**: Read from AWS Secrets Manager or SSM Parameter Store.

## Gemini Added Memories
- The user prefers that I avoid using 'model.save()' in Mongoose. I should use atomic operations like 'Model.create()' or 'Model.findOneAndUpdate()' instead.
- When testing with Vitest and Mongoose, avoid type assertions (like 'as any' or 'as unknown') for mocking database documents. Instead, use 'vi.mocked(Module, true)' for type-safe mocks and instantiate real Mongoose documents using a local model with the project's schema to ensure full type compatibility with 'HydratedDocument'.
