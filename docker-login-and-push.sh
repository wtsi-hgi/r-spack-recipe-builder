#!/bin/zsh

set -e

# Check if DOCKER_TOKEN is set
if [ -z "$DOCKER_TOKEN" ]; then
    echo "Error: DOCKER_TOKEN environment variable is not set"
    echo "Please set it with: export DOCKER_TOKEN='your-docker-token-here'"
    exit 1
fi

echo "Logging in to Docker..."
echo "$DOCKER_TOKEN" | docker login -u mercury --password-stdin

echo "Tagging image..."
docker tag spack-with-uv:latest mercury/softpack-build:0.21.1.3

echo "Pushing image to registry..."
docker push mercury/softpack-build:0.21.1.3

echo "Successfully pushed mercury/softpack-build:0.21.1.3"
