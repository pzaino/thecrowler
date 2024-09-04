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

# Set the Selenium version and build ID
# Get latest version from https://github.com/SeleniumHQ/docker-selenium/
# Tested through 4.18.1
#export SELENIUM_VER_NUM="4.18.1"
#export SELENIUM_BUILDID="20240124"
# Latest version:
export SELENIUM_VER_NUM="4.24.0"
export SELENIUM_BUILDID="20240830"

export SELENIUM_RELEASE="${SELENIUM_VER_NUM}-${SELENIUM_BUILDID}"

# Generate a Docker image name for today's build (that we are about to do)
CURRENT_DATE=$(date +%Y%m%d)
export SELENIUM_PROD_RELESE="${SELENIUM_VER_NUM}-${CURRENT_DATE}"

# Set the Selenium default port
if [ "$SELENIUM_PORT" == "" ];
then
    export SELENIUM_PORT=4444
fi

# Set the platform for the host architecture
PLATFORM="linux/amd64"
SELENIUM_IMAGE="selenium/standalone-chrome:${SELENIUM_PROD_RELESE}"
if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    PLATFORM="linux/arm64/v8"
    POSTGRES_IMAGE="arm64v8/"
    #SELENIUM_IMAGE="seleniarm/standalone-chromium:${SELENIUM_PROD_RELESE}"
    SELENIUM_IMAGE="selenium/standalone-firefox:${SELENIUM_PROD_RELESE}"
fi
export PLATFORMS=$PLATFORM

# Export platform as an environment variable
export DOCKER_DEFAULT_PLATFORM=$PLATFORM
export DOCKER_POSTRGESS_IMAGE=$POSTGRES_IMAGE
export DOCKER_SELENIUM_IMAGE=$SELENIUM_IMAGE

# Build custom Selenium image
echo "Building the selenium container"
rm -rf docker-selenium
git clone https://github.com/SeleniumHQ/docker-selenium.git ./docker-selenium
rval=$?
if [ $rval -ne 0 ]; then
    echo "Failed to clone the Selenium repository"
    exit 1
fi
rm -rf Rbee
git clone https://github.com/pzaino/RBee.git ./Rbee
rval=$?
if [ $rval -ne 0 ]; then
    echo "Failed to clone the RBee repository"
    exit 1
fi
mkdir -p ./Rbee/pkg

# Prepare Sources
cd ./docker-selenium || exit 1
git checkout "${SELENIUM_RELEASE}"
git pull origin "${SELENIUM_RELEASE}"
# Check if we have patches for this Selenium image version
if [ -d "../selenium-patches/${SELENIUM_VER_NUM}" ];
then
    echo "Found patches for Selenium version ${SELENIUM_VER_NUM}"
    # patch Selenium Dockefile and start-selenium-standalone.sh files:
    cp ../selenium-patches/${SELENIUM_VER_NUM}/Dockerfile ./Standalone/Dockerfile
    if [ "$PLATFORM" == "linux/arm64/v8" ];
    then
        # We need to patch the Dockerfile_Base file for ARM64
        patch_file="Dockerfile_Base_ARM64_${SELENIUM_VER_NUM}.patch"
        echo "Patching Dockerfile in ./Base for ARM64: ${patch_file}"
        if [ -f "../selenium-patches/${SELENIUM_VER_NUM}/${patch_file}" ];
        then
            pushd ./Base || exit 1
            cp "../../selenium-patches/${patch_file}" "./${patch_file}"
            if patch Dockerfile ./${patch_file}; then
                echo "Patch applied successfully."
            else
                echo "Failed to apply patch."
                exit 1
            fi
            popd || exit 1
        else
            echo "No architecture patch file found for ${patch_file}, skipping patching."
        fi
    fi
else
    echo "No patches found for Selenium version ${SELENIUM_VER_NUM}, skipping..."
fi

# Add Rbee to the Selenium image
cp -r ../Rbee ./Standalone/
cp ../selenium-patches/browserAutomation.conf ./Standalone/Rbee/browserAutomation.conf

# build Selenium image
if [ "${ARCH}" == "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    # Google chrome is not yet supported officially on ARM64
    make standalone_firefox
else
    make standalone_chrome
fi

# Run Docker Compose
docker-compose up --build
