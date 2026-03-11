# Shell Scripts Boilerplate for Approva DevOps

## `scripts/build.sh`
Builds an ARM64 Docker image using `buildx` and `QEMU`.

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

## `scripts/push.sh`
Logs into ECR and pushes the image.

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

## `scripts/update_ecs.sh`
Forces a new deployment in ECS and waits for stability.

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

## `scripts/run.sh`
Runs the container locally for testing.

```bash
#!/bin/bash
set -e
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
docker run -d -p 8000:8000 --env-file "$SCRIPT_DIR/../.env" -e AWS_PROFILE -v ~/.aws:/root/.aws --name $APP_NAME $APP_NAME
```
