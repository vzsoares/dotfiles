# CI/CD and Orchestration for Approva DevOps

## `Makefile`
Orchestrates development and deployment tasks.

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

## `bitbucket-pipelines.yml`
Bitbucket Pipelines configuration for automated deployments using ARM64 runners.

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
