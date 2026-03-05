# Makefile Boilerplate

```makefile
-include .env
export

.SHELL := /bin/bash -e
.RECIPEPREFIX := >
.DEFAULT_GOAL := help

STAGE ?= dev
TERRAFORM ?= terraform

WORK_DIR_BASE=./infra/environments

AWS_ACCOUNT_ID := $(if $(filter prod,$(STAGE)),{{PROD_ACCOUNT_ID}},{{DEV_ACCOUNT_ID}})
export AWS_ACCOUNT_ID

##@
##@ Deploy
##@

deploy: ##@ Deploy to current STAGE (terraform apply)
> @echo "Deploying for stage: $(STAGE)"
> @echo "Running terraform apply in $(WORK_DIR_BASE)/$(STAGE)"
> @(cd $(WORK_DIR_BASE)/$(STAGE) && $(TERRAFORM) init && $(TERRAFORM) apply -auto-approve)

help: ##@ (Default) This help menu
> @printf "\nUsage: STAGE=dev make <command>\n"
> @grep -F -h "##@" $(MAKEFILE_LIST) | grep -F -v grep -F | sed -e 's/\\$$//' | awk 'BEGIN {FS = ":*[[:space:]]*##@[[:space:]]*"}; \
	{ \
		if($$2 == "") \
			pass; \
		else if($$0 ~ /^#/) \
			printf "\n%s\n", $$2; \
		else if($$1 == "") \
			printf "     %-20s%s\n", "", $$2; \
		else \
      printf "\n    \033[38;2;156;207;216m%-20s\033[0m %s\n", $$1, $$2; \
	}'
```
