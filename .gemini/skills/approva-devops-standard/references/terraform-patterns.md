# Terraform Patterns for Approva DevOps

## Core Service Module (`infra/services/main/`)

### `main.tf` - Resource Definitions
This file defines the primary resources for an ECS service, including ECR, Task Definition, Target Group, and ALB Listener Rules.

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
  family                   = var.project_name
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
  name        = "${var.project_name}-tg"
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
  name            = var.project_name
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

### `iam.tf` - Permissions
Standard IAM roles for ECS task execution and resource access.

```hcl
resource "aws_iam_role" "ecs_task_execution" {
  name = "${var.project_name}-ecs-execution"
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

### `data.tf` - Resource Discovery
Reads shared infrastructure configuration via SSM.

```hcl
data "aws_ssm_parameter" "vpc_id" { name = "/${var.stage}/CORE_NETWORK_VPC_ID" }
data "aws_ssm_parameter" "core_alb_https_listener_arn" { name = "/${var.stage}/CORE_ALB_HTTPS_LISTENER_ARN" }
data "aws_lb_listener" "existing" { arn = data.aws_ssm_parameter.core_alb_https_listener_arn.value }
data "aws_ssm_parameter" "cluster_name" { name = "/${var.stage}/CORE_ECS_CLUSTER_NAME" }
data "aws_ssm_parameter" "capacity_provider_name" { name = "/${var.stage}/CORE_ECS_CAPACITY_PROVIDER_NAME" }
```

## Environment Entrypoints (`infra/environments/{dev,prod}/`)

### `main.tf`
Instantiates the main-service module.

```hcl
locals {
  region     = "us-east-1"
  stage      = "dev" # or "prod"
  account_id = "619356296005" # or prod account
  project_name = "example-app"
}

module "main-service" {
  source = "../../services/main"
  stage        = local.stage
  project_name = local.project_name
  listener_priority = 10
  host_name        = "app-dev.approvafacil.com.br"
  aws_region       = local.region
}
```

### `provider.tf`
Configures backend (S3 with DynamoDB lock) and provider.

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
