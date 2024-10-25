#!/bin/bash

# shellcheck disable=SC2124
pars="$@"

# Support functions

version_to_integer() {
    local version=$1
    local padded_version=""
    IFS='.' read -ra parts <<< "$version" # Split version by '.'
    for part in "${parts[@]}"; do
        # Pad each part with leading zero if necessary (e.g., 4 becomes 04)
        # Remove leading zero padding for integer comparison
        padded_version+=$(printf "%02d" "$part")
    done
    # Remove any leading zeros to prevent octal interpretation
    echo "$((10#$padded_version))"
}

# Check for mandatory settings
if [ -f config.sh ]; then
    source config.sh
else
    if [ -f .env ]; then
        source .env
    fi
    echo "config.sh or .env not found! Proceeding with checking if the user has defined the Environment variables manually."
fi

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
# Tested through 4.19.1
#export SELENIUM_VER_NUM="4.19.1"
#export SELENIUM_BUILDID="20240402"
# Tested through 4.20.0
#export SELENIUM_VER_NUM="4.20.0"
#export SELENIUM_BUILDID="20240425"
# Tested through 4.21.0
export SELENIUM_VER_NUM="4.21.0"
export SELENIUM_BUILDID="20240522"
# (Not yet working!) 4.23.1
#export SELENIUM_VER_NUM="4.23.1"
#export SELENIUM_BUILDID="20240813"
# (Not yet working!) 4.24.0
#export SELENIUM_VER_NUM="4.24.0"
#export SELENIUM_BUILDID="20240830"

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
    #SELENIUM_IMAGE="selenium/standalone-firefox:${SELENIUM_PROD_RELESE}"
    #SELENIUM_IMAGE="seleniarm/standalone-chromium:${SELENIUM_PROD_RELESE}"
    SELENIUM_IMAGE="selenium/standalone-chromium:${SELENIUM_PROD_RELESE}"
    SELENIUM_VER_NUM_INT=$(version_to_integer "$SELENIUM_VER_NUM")
    TARGET_VER_INT=$(version_to_integer "4.21.0")
    # shellcheck disable=SC2086
    if [ $SELENIUM_VER_NUM_INT -lt $TARGET_VER_INT ]; then
        SELENIUM_IMAGE="selenium/standalone-firefox:${SELENIUM_PROD_RELESE}"
    fi
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
    # Check if there is a Makefile patch
    if [ -f "../selenium-patches/${SELENIUM_VER_NUM}/Makefile-fixed.patch" ];
    then
        cp ../selenium-patches/${SELENIUM_VER_NUM}/Makefile-fixed.patch ./Makefile-fixed.patch
        if patch Makefile ./Makefile-fixed.patch; then
            echo "Makefile patch applied successfully."
        else
            echo "Failed to apply Makefile patch."
            exit 1
        fi
    fi

    # check if there is a `noarch` Base patch first:
    if [ -f "../selenium-patches/${SELENIUM_VER_NUM}/Dockerfile_Base_noarch_${SELENIUM_VER_NUM}.patch" ];
    then
        pushd ./Base || exit 1
        cp "../../selenium-patches/${SELENIUM_VER_NUM}/Dockerfile_Base_noarch_${SELENIUM_VER_NUM}.patch" "./Dockerfile_Base.patch"
        if patch Dockerfile ./Dockerfile_Base.patch; then
            echo "Base patch applied successfully."
        else
            echo "Failed to apply Base patch."
            exit 1
        fi
        popd || exit 1
    else
        # We have no noarch patch (for this version),
        # check if we have an architecture specific patch then
        if [ "$PLATFORM" == "linux/arm64/v8" ];
        then
            # We need to patch the Dockerfile_Base file for ARM64
            patch_file="Dockerfile_Base_ARM64_${SELENIUM_VER_NUM}.patch"
            echo "Patching Dockerfile in ./Base for ARM64: ${patch_file}"
            if [ -f "../selenium-patches/${SELENIUM_VER_NUM}/${patch_file}" ];
            then
                pushd ./Base || exit 1
                cp "../../selenium-patches/${SELENIUM_VER_NUM}/${patch_file}" "./${patch_file}"
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
    fi

    # Check if there are Chromium patches
    if [ "$PLATFORM" == "linux/arm64/v8" ];
    then
        # We need to patch the Dockerfile_Base file for ARM64
        patch_file="Dockerfile_Chromium_ARM64_${SELENIUM_VER_NUM}.patch"
        echo "Patching Dockerfile in ./NodeChromium for ARM64: ${patch_file}"
        if [ -f "../selenium-patches/${SELENIUM_VER_NUM}/${patch_file}" ];
        then
            pushd ./NodeChromium || exit 1
            cp "../../selenium-patches/${SELENIUM_VER_NUM}/${patch_file}" "./${patch_file}"
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
    SELENIUM_VER_NUM_INT=$(version_to_integer "$SELENIUM_VER_NUM")
    TARGET_VER_INT=$(version_to_integer "4.21.0")
    # shellcheck disable=SC2086
    if [ $SELENIUM_VER_NUM_INT -lt $TARGET_VER_INT ]; then
        make standalone_firefox
    else
        make standalone_chromium
    fi
else
    make standalone_chrome
fi

# Run Docker Compose
# shellcheck disable=SC2086
docker-compose ${pars}
rval=$?
if [ $rval -ne 0 ]; then
    echo "Failed to build the Docker containers"
    exit 1
fi
