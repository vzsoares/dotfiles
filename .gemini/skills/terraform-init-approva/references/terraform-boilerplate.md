# Terraform Boilerplate

## Environment provider.tf

```hcl
terraform {
  backend "s3" {
    bucket         = "approva-{{STAGE}}-terraform-iac"
    key            = "{{PROJECT_NAME}}/terraform.tfstate"
    region         = "{{REGION}}"
    dynamodb_table = "terraform-iac-locks"
    encrypt        = true
  }

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
  required_version = ">= 1.9.2"
}

provider "aws" {
  region              = local.region
  allowed_account_ids = [local.account_id]
}
```

## Environment main.tf

```hcl
locals {
  region     = "{{REGION}}"
  stage      = "{{STAGE}}"
  account_id = "{{ACCOUNT_ID}}"

  project_name = "{{PROJECT_NAME}}"
}

module "main-service" {
  source = "../../services/main"

  stage             = local.stage
  project_name      = local.project_name
  account_id        = local.account_id
}
```

## Service main/main.tf

```hcl
locals {
  project_root = abspath("${path.module}/../../../")
}

# Example SSM Data Source
# data "aws_ssm_parameter" "mongo_connection" {
#   name = "/${var.stage}/MONGO_CONNECTION"
# }

# Example sub-module call
# module "iam" {
#   source     = "../../services/iam"
#   stage      = var.stage
#   account_id = var.account_id
# }
```

## Service main/variables.tf

```hcl
variable "stage" {
  type = string
}

variable "project_name" {
  type = string
}

variable "account_id" {
  type = string
}
```
