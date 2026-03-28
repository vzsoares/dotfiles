---
name: approva-devops-standard
description: Setup and management of Approva's standardized DevOps infrastructure, including multi-environment Terraform (S3 backend, ECS on EC2 ARM64), standardized shell scripts for build/push/deploy, and Bitbucket Pipelines integration. Use when initializing a new project's infra or managing existing ECS services.
argument-hint: [action] [project-name] [environment]
allowed-tools: Read, Write, Edit, Glob, Grep, Bash, Agent
---

# Approva DevOps Standard

You are an expert at setting up and managing Approva's standardized infrastructure and deployment workflows. You generate and manage Terraform modules, shell scripts, Makefiles, and CI/CD pipelines following Approva's conventions.

## Workflow Summary

1. **Infrastructure Setup**: Implement the modular Terraform structure using S3 for state and DynamoDB for locking.
2. **Resource Discovery**: Use AWS SSM Parameter Store to discover shared resources (VPC, Cluster, Listeners).
3. **Build System**: Use Docker `buildx` with QEMU for ARM64 builds, injecting CodeArtifact tokens.
4. **Deployment**: Orchestrate via `Makefile` and `scripts/` to build, push, apply Terraform, and update ECS.
5. **CI/CD**: Integrate with Bitbucket Pipelines using ARM64 runners.

## Key Standards

- **Architecture**: `linux/arm64` on EC2 (Shared Cluster).
- **Environment Discovery**: 100% via SSM (`/${stage}/CORE_...`).
- **State Management**: S3 bucket `approva-${stage}-terraform-iac` with key `${project}/terraform.tfstate`.
- **Naming**: Use `${project_name}-${stage}` consistently across ECS, Target Groups, and IAM for environment isolation.

## Directory Structure

```
infra/
  services/main/       # Terraform module (main.tf, iam.tf, data.tf, variables.tf, outputs.tf)
  environments/
    dev/               # Dev entrypoint (main.tf, provider.tf, deploy.env)
    prod/              # Prod entrypoint (main.tf, provider.tf, deploy.env)
scripts/
  build.sh             # ARM64 Docker build with buildx + QEMU
  push.sh              # ECR login and push
  update_ecs.sh        # Force new ECS deployment and wait for stability
  run.sh               # Local container run
Makefile               # Orchestration (build, push, deploy, update-ecs)
bitbucket-pipelines.yml
```

## Terraform Patterns

### Core Service Module (`infra/services/main/`)

#### `main.tf` - Resource Definitions

Defines ECR repository (with lifecycle policy keeping last 3 images), ECS task definition (EC2 launch type, ARM64, bridge network mode), ALB target group with health check on `/health/`, listener rule routing by host header, and ECS service with binpack placement strategy.

```hcl
resource "aws_ecr_repository" "app" {
  name = var.project_name
  image_scanning_configuration {
    scan_on_push = true
  }
  tags = { Terraform = "true" }
}

resource "aws_ecr_lifecycle_policy" "app" {
  repository = aws_ecr_repository.app.name
  policy = jsonencode({
    rules = [{
      rulePriority = 1, description = "Keep last 3 images", selection = { tagStatus = "any", countType = "imageCountMoreThan", countNumber = 3 }, action = { type = "expire" }
    }]
  })
}

data "aws_ecr_image" "repo_image" {
  repository_name = aws_ecr_repository.app.name
  image_tag       = "latest"
}

resource "aws_ecs_task_definition" "app" {
  family                   = "${var.project_name}-${var.stage}"
  requires_compatibilities = ["EC2"]
  network_mode             = "bridge"
  execution_role_arn       = aws_iam_role.ecs_task_execution.arn
  task_role_arn            = aws_iam_role.ecs_task_execution.arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "ARM64"
  }

  container_definitions = jsonencode([{
    name              = var.project_name
    image             = data.aws_ecr_image.repo_image.image_uri
    essential         = true
    memoryReservation = var.stage == "prod" ? 1024 : 512
    cpu               = var.stage == "prod" ? 512 : 256
    portMappings = [
      { containerPort = 8000, hostPort = 0, appProtocol = "http", name = "${var.project_name}-port-mapping-8000" }
    ]
    environment = [
      { name  = "STAGE", value = var.stage },
      { name  = "PROJECT_NAME", value = var.project_name },
      { name  = "DJANGO_SECRET_KEY", value = data.aws_ssm_parameter.django_secret_key.value },
      { name  = "DJANGO_DEBUG", value = "False" }
    ]
    healthCheck = {
      command     = ["CMD-SHELL", "curl -f http://localhost:8000/health/ || exit 1"]
      interval    = 30, timeout = 5, retries = 3, startPeriod = 60
    }
    logConfiguration = {
      logDriver = "awslogs",
      options = {
        "awslogs-group"         = "/ecs/${var.project_name}-${var.stage}",
        "awslogs-region"        = var.aws_region,
        "awslogs-stream-prefix" = "ecs",
        "awslogs-create-group"  = "true"
      }
    }
  }])
  tags = { Terraform = "true" }
}

resource "aws_lb_target_group" "app" {
  name        = "${var.project_name}-${var.stage}-tg"
  port        = 80, protocol = "HTTP", vpc_id = data.aws_ssm_parameter.vpc_id.value, target_type = "instance"
  health_check {
    healthy_threshold = "3", interval = "30", protocol = "HTTP", matcher = "200-404", timeout = "25", path = "/health/", unhealthy_threshold = "2"
  }
  lifecycle { create_before_destroy = true }
  tags = { Terraform = "true" }
}

resource "aws_lb_listener_rule" "app" {
  listener_arn = data.aws_lb_listener.existing.arn
  priority     = var.listener_priority
  action { type = "forward", target_group_arn = aws_lb_target_group.app.arn }
  condition { host_header { values = [var.host_name] } }
  tags = { Terraform = "true" }
}

resource "aws_ecs_service" "app" {
  name            = "${var.project_name}-${var.stage}"
  cluster         = data.aws_ssm_parameter.cluster_name.value
  task_definition = aws_ecs_task_definition.app.arn
  desired_count   = 1
  availability_zone_rebalancing = "DISABLED"
  ordered_placement_strategy { type = "binpack", field = "cpu" }
  capacity_provider_strategy { capacity_provider = data.aws_ssm_parameter.capacity_provider_name.value, weight = 100 }
  load_balancer { target_group_arn = aws_lb_target_group.app.arn, container_name = var.project_name, container_port = 8000 }
  tags = { Terraform = "true" }
}
```

#### `iam.tf` - Permissions

Standard IAM roles for ECS task execution with policies for CloudWatch logs, SSM access, and S3 bucket access.

```hcl
resource "aws_iam_role" "ecs_task_execution" {
  name = "${var.project_name}-ecs-execution-${var.stage}"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{ Effect = "Allow", Principal = { Service = "ecs-tasks.amazonaws.com" }, Action = "sts:AssumeRole" }]
  })
  tags = { Terraform = "true" }
}

resource "aws_iam_role_policy_attachment" "ecs_task_execution_policy" {
  role       = aws_iam_role.ecs_task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

resource "aws_iam_policy" "create_log_group" {
  name        = "${var.project_name}-${var.stage}"
  description = "Allow creating CloudWatch log groups"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{ Effect = "Allow", Action = ["logs:CreateLogGroup"], Resource = "*" }]
  })
  tags = { Terraform = "true" }
}

resource "aws_iam_role_policy_attachment" "ecs_task_create_log_group_policy" {
  role       = aws_iam_role.ecs_task_execution.name
  policy_arn = aws_iam_policy.create_log_group.arn
}

resource "aws_iam_policy" "project_ssm_policy" {
  name        = "${var.project_name}-iam-policy-${var.stage}"
  description = "IAM policy for ${var.project_name} ${var.stage} to access SSM parameters"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      { Effect = "Allow", Action = ["ssm:GetParameters", "ssm:GetParameter", "ssm:StartSession", "ssm:TerminateSession", "ssm:ResumeSession", "ssm:DescribeSessions", "ssm:GetConnectionStatus", "ssm:ExecuteCommand", "ssmmessages:CreateControlChannel", "ssmmessages:CreateDataChannel", "ssmmessages:OpenControlChannel", "ssmmessages:OpenDataChannel"], Resource = "*" },
      { Effect = "Allow", Action = ["s3:ListBucket", "s3:GetObject", "s3:PutObject", "s3:DeleteObject"], Resource = "arn:aws:s3:::approva-structured-${var.stage}/${var.project_name}/*" }
    ]
  })
  tags = { Terraform = "true" }
}

resource "aws_iam_role_policy_attachment" "ecs_task_ssm_policy" {
  role       = aws_iam_role.ecs_task_execution.name
  policy_arn = aws_iam_policy.project_ssm_policy.arn
}
```

#### `data.tf` - Resource Discovery via SSM

```hcl
data "aws_ssm_parameter" "vpc_id" { name = "/${var.stage}/CORE_NETWORK_VPC_ID" }
data "aws_ssm_parameter" "core_alb_https_listener_arn" { name = "/${var.stage}/CORE_ALB_HTTPS_LISTENER_ARN" }
data "aws_lb_listener" "existing" { arn = data.aws_ssm_parameter.core_alb_https_listener_arn.value }
data "aws_ssm_parameter" "cluster_name" { name = "/${var.stage}/CORE_ECS_CLUSTER_NAME" }
data "aws_ssm_parameter" "capacity_provider_name" { name = "/${var.stage}/CORE_ECS_CAPACITY_PROVIDER_NAME" }
```

### Environment Entrypoints (`infra/environments/{dev,prod}/`)

#### `main.tf`

```hcl
locals {
  region       = "us-east-1"
  stage        = "dev" # or "prod"
  account_id   = "619356296005" # or prod account
  project_name = "example-app"
}

module "main-service" {
  source            = "../../services/main"
  stage             = local.stage
  project_name      = local.project_name
  listener_priority = 10
  host_name         = "app-dev.approvafacil.com.br"
  aws_region        = local.region
}
```

#### `provider.tf`

```hcl
terraform {
  backend "s3" {
    bucket         = "approva-dev-terraform-iac"
    key            = "example-app/terraform.tfstate"
    region         = "us-east-1"
    dynamodb_table = "terraform-iac-locks"
    encrypt        = true
  }
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.59" }
  }
  required_version = ">= 1.9.2"
}

provider "aws" {
  region              = local.region
  allowed_account_ids = [local.account_id]
}
```

## Shell Scripts

### `scripts/build.sh` - ARM64 Docker build with buildx + QEMU + CodeArtifact token

```bash
#!/bin/bash
set -e
if [ -z "$1" ]; then echo "Usage: $0 <environment>"; exit 1; fi
ENVIRONMENT=$1
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
source "$SCRIPT_DIR/../infra/environments/$ENVIRONMENT/deploy.env"

IMAGE_TAG="latest"
echo "Setting up Docker Buildx for ARM64..."
docker buildx use approva-builder || docker buildx create --name approva-builder --use

if [ "$(uname -m)" != "aarch64" ] && ! docker buildx inspect --bootstrap | grep -q "linux/arm64"; then
    docker run --privileged --rm tonistiigi/binfmt --install all
fi

TOKEN=$(aws codeartifact get-authorization-token --domain approva --domain-owner 619356296005 --region us-east-1 --query authorizationToken --output text | tr -d '\n')
export CODEARTIFACT_AUTH_TOKEN=$TOKEN

docker buildx build --secret id=aws-codeartifactenv,env=CODEARTIFACT_AUTH_TOKEN --platform linux/arm64 -t $ECR_REPOSITORY_URL/$APP_NAME:$IMAGE_TAG . --load
docker tag $ECR_REPOSITORY_URL/$APP_NAME:$IMAGE_TAG $APP_NAME:latest
```

### `scripts/push.sh` - ECR login and push

```bash
#!/bin/bash
set -e
if [ -z "$1" ]; then echo "Usage: $0 <environment>"; exit 1; fi
ENVIRONMENT=$1
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
source "$SCRIPT_DIR/../infra/environments/$ENVIRONMENT/deploy.env"

IMAGE_TAG="latest"
aws ecr get-login-password --region $AWS_REGION | docker login --username AWS --password-stdin $AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com
docker push $ECR_REPOSITORY_URL/$APP_NAME:$IMAGE_TAG
```

### `scripts/update_ecs.sh` - Force new ECS deployment

```bash
#!/bin/bash
set -e
if [ -z "$1" ]; then echo "Usage: $0 <stage>"; exit 1; fi
STAGE=$1
SCRIPT_DIR=$(dirname "$0")
source "$SCRIPT_DIR/../infra/environments/$STAGE/deploy.env"
SERVICE_NAME=$APP_NAME

aws ecs update-service --cluster "$ECS_CLUSTER_NAME" --service "$SERVICE_NAME" --force-new-deployment --region "$AWS_REGION" > /dev/null
echo "Waiting for service to be stable..."
aws ecs wait services-stable --cluster "$ECS_CLUSTER_NAME" --services "$SERVICE_NAME" --region "$AWS_REGION"
```

### `scripts/run.sh` - Local container run

```bash
#!/bin/bash
set -e
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
docker run -d -p 8000:8000 --env-file "$SCRIPT_DIR/../.env" -e AWS_PROFILE -v ~/.aws:/root/.aws --name $APP_NAME $APP_NAME
```

## Makefile

```makefile
-include .env
export

.RECIPEPREFIX := >
.DEFAULT_GOAL := help

STAGE ?= dev
TERRAFORM ?= terraform
WORK_DIR_BASE=./infra/environments

help: ## Show this help
> @awk -F':.*?##' '/^[a-zA-Z_-]+:.*?##/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build Docker image
> @./scripts/build.sh $(STAGE)

push: ## Push Docker image
> @./scripts/push.sh $(STAGE)

deploy: ## Build, Push, Terraform Apply, Update ECS
> @$(MAKE) build
> @$(MAKE) push
> @(cd $(WORK_DIR_BASE)/$(STAGE) && $(TERRAFORM) init && $(TERRAFORM) apply -auto-approve)
> @$(MAKE) update-ecs

update-ecs: ## Force ECS deployment
> @./scripts/update_ecs.sh $(STAGE)
```

## Bitbucket Pipelines

```yaml
image:
    name: 910927858441.dkr.ecr.us-east-1.amazonaws.com/approva-ci-runner:5-arm
    aws:
        access-key: $ECR_AWS_ACCESS_KEY_ID
        secret-key: $ECR_AWS_SECRET_ACCESS_KEY
options:
    runtime:
        cloud:
            version: '3'
            arch: arm

definitions:
    steps:
        - step: &deploy
              name: Deploy
              services: [docker]
              size: 2x
              options: { docker: true }
              script:
                  - aws configure set aws_access_key_id $AWS_ACCESS_KEY_ID --profile $AWS_PROFILE
                  - aws configure set aws_secret_access_key $AWS_SECRET_ACCESS_KEY --profile $AWS_PROFILE
                  - export STAGE=$STAGE
                  - make deploy

pipelines:
    branches:
        prod:
            - step:
                  <<: *deploy
                  deployment: prod
        dev:
            - step:
                  <<: *deploy
                  deployment: dev
```

## Instructions

When the user asks you to set up infrastructure for a new project:

1. Ask for the **project name**, **host names** (dev/prod), **listener priorities**, and **AWS account IDs** if not provided.
2. Generate the full directory structure with all Terraform files, scripts, Makefile, and pipeline config.
3. Create a `deploy.env` file per environment with: `APP_NAME`, `AWS_REGION`, `AWS_ACCOUNT_ID`, `ECR_REPOSITORY_URL`, `ECS_CLUSTER_NAME`.
4. Adapt container definitions (ports, health check path, environment variables) to the specific project needs.

When modifying existing infrastructure:

1. Read the current Terraform state and scripts before making changes.
2. Preserve the naming convention `${project_name}-${stage}` across all resources.
3. Always use SSM for shared resource discovery -- never hardcode ARNs or IDs.
