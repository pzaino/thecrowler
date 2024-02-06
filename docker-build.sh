#!/bin/bash

# Check for mandatory settings

# shellcheck disable=SC2153
if [ "${DOCKER_DB_HOST}" = "" ]; then
    echo "DOCKER_DB_HOST is not set!"
    exit 1
fi

if [ "${DOCKER_POSTGRES_PASSWORD}" = "" ]; then
    echo "DOCKER_POSTGRES_PASSWORD is not set!"
    exit 1
fi

if [ "${DOCKER_CROWLER_DB_PASSWORD}" = "" ]; then
    echo "DOCKER_CROWLER_DB_PASSWORD is not set!"
    exit 1
fi

# Check for optional settings

if [ "${DOCKER_DB_PORT}" = "" ]; then
    export DOCKER_DB_PORT=5432
fi

if [ "${DOCKER_POSTGRES_DB_USER}" = "" ]; then
    export DOCKER_POSTGRES_DB_USER=postgres
fi

if [ "${DOCKER_POSTGRES_DB_NAME}" = "" ]; then
    export DOCKER_POSTGRES_DB_NAME=SitesIndex
fi

if [ "${DOCKER_CROWLER_DB_USER}" = "" ]; then
    export DOCKER_CROWLER_DB_USER=crowler
fi

# Detect host architecture
ARCH=$(uname -m)
PLATFORM="linux/amd64"
POSTGRES_IMAGE=""
SELENIUM_IMAGE="selenium/standalone-chrome:4.17.0-20240123"
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
