#!/bin/bash

# Set your variables
AWS_ACCOUNT_ID="600499021027"
AWS_REGION="eu-central-1"
ECR_REPO="small-cnd/node"

# Authenticate Docker to ECR
IMAGE_URI="${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com/${ECR_REPO}"

aws ecr get-login-password --region "$AWS_REGION" \
  | docker login --username AWS --password-stdin "${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"

# Ensure buildx builder exists and is selected
docker buildx create --use --name xbuilder >/dev/null 2>&1 || docker buildx use xbuilder

# Build + push an amd64 image (this guarantees the manifest exists in ECR)
docker buildx build \
  --platform linux/amd64 \
  -t "${IMAGE_URI}:latest" \
  --push .