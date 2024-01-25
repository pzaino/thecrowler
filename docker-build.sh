#!/bin/bash

# Detect host architecture
ARCH=$(uname -m)
PLATFORM="linux/amd64"
if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    PLATFORM="linux/arm64/v8"
fi

# Export platform as an environment variable
export DOCKER_DEFAULT_PLATFORM=$PLATFORM

# Run Docker Compose
docker-compose up --build
