#!/bin/bash

# Detect host architecture
ARCH=$(uname -m)
PLATFORM="linux/amd64"
POSTGRES_IMAGE=""
SELENIUM_IMAGE="selenium/standalone-chrome"
if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    PLATFORM="linux/arm64/v8"
    POSTGRES_IMAGE="arm64v8/"
    SELENIUM_IMAGE="seleniarm/standalone-chromium"
fi

# Export platform as an environment variable
export DOCKER_DEFAULT_PLATFORM=$PLATFORM
export DOCKER_POSTRGESS_IMAGE=$POSTGRES_IMAGE
export DOCKER_SELENIUM_IMAGE=$SELENIUM_IMAGE

# Run Docker Compose
docker-compose up --build
