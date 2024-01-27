#!/bin/bash


if [ "${DOCKER_DB_PORT}" = "" ]; then
    export DOCKER_DB_PORT=5432
fi

# shellcheck disable=SC2153
if [ "${DOCKER_DB_HOST}" = "" ]; then
    echo "DOCKER_DB_HOST is not set"
    exit 1
fi

if [ "${DOCKER_POSTGRES_DB_USER}" = "" ]; then
    export DOCKER_POSTGRES_DB_USER=postgres
fi

if [ "${DOCKER_POSTGRES_DB_PASSWORD}" = "" ]; then
    export DOCKER_POSTGRES_DB_PASSWORD=postgres
fi

if [ "${DOCKER_POSTGRES_DB_NAME}" = "" ]; then
    export DOCKER_POSTGRES_DB_NAME=SitesIndex
fi

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
