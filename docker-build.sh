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
SELENIUM_IMAGE="selenium/standalone-chrome:4.18.1-20240224"
if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    PLATFORM="linux/arm64/v8"
    POSTGRES_IMAGE="arm64v8/"
    SELENIUM_IMAGE="seleniarm/standalone-chromium"
fi
if [ "$SELENIUM_RELEASE" == "" ];
then
    #SELENIUM_RELEASE=4.18.1-20240224
    SELENIUM_RELEASE=4.20.0-20240428
fi
if [ "$SELENIUM_PORT" == "" ];
then
    SELENIUM_PORT=4444
fi

# Export platform as an environment variable
export DOCKER_DEFAULT_PLATFORM=$PLATFORM
export DOCKER_POSTRGESS_IMAGE=$POSTGRES_IMAGE
export DOCKER_SELENIUM_IMAGE=$SELENIUM_IMAGE

# Build custom Selenium image
echo "Building the selenium container"
rm -rf docker-selenium
git clone git@github.com:SeleniumHQ/docker-selenium.git ./docker-selenium
rval=$?
if [ $rval -ne 0 ]; then
    echo "Failed to clone the Selenium repository"
    exit 1
fi
# Prepare Sources
cd ./docker-selenium || exit 1
git checkout "${SELENIUM_RELEASE}"
git pull origin "${SELENIUM_RELEASE}"
# patch Selenium Dockefile and start-selenium-standalone.sh files:
cp ../selenium-patches/Dockerfile ./Standalone/Dockerfile
cp ../selenium-patches/start-selenium-standalone.sh ./Standalone/start-selenium-standalone.sh
mkdir -p ./Standalone/Rbee
cp -r ../cmd ./Standalone/Rbee/cmd
cp -r ../pkg ./Standalone/Rbee/pkg
cp -r ../services ./Standalone/Rbee/services
cp -r ../schemas ./Standalone/Rbee/schemas
cp -r ../go.mod ./Standalone/Rbee/go.mod
cp -r ../go.sum ./Standalone/Rbee/go.sum
cp -r ../config.sh ./Standalone/Rbee/config.yaml
cp ../main.go ./Standalone/Rbee/main.go
cp ../selenium-patches/browserAutomation.conf ./Standalone/Rbee/browserAutomation.conf
cp ../autobuild.sh ./Standalone/Rbee/autobuild.sh

# build Selenium image
make standalone_chrome

# Run Docker Compose
docker-compose up --build
