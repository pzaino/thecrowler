#!/bin/bash
set -euo pipefail

# shellcheck disable=SC2124
pars="${*:-}"

# === CI toggles ===
: "${FORCE_CHROMIUM:=false}"   # set to "true" in CI to force chromium build
: "${SKIP_COMPOSE:=false}"     # set to "true" in CI to avoid docker compose + env checks

version_to_integer() {
  local version=$1; local padded=""
  IFS='.' read -ra parts <<< "$version"
  for part in "${parts[@]}"; do padded+=$(printf "%02d" "$part"); done
  echo "$((10#$padded))"
}

# Optional config sourcing
if [ -f config.sh ]; then
  source config.sh
elif [ -f .env ]; then
  source .env
else
  echo "config.sh or .env not found! Proceeding with environment vars."
fi

# === only enforce these if we are going to run docker compose ===
if [ "${SKIP_COMPOSE}" != "true" ]; then
  : "${DOCKER_DB_HOST:?DOCKER_DB_HOST is not set!}"
  : "${DOCKER_POSTGRES_PASSWORD:?DOCKER_POSTGRES_PASSWORD is not set!}"
  : "${DOCKER_CROWLER_DB_PASSWORD:?DOCKER_CROWLER_DB_PASSWORD is not set!}"
  : "${DOCKER_DB_PORT:=5432}"
  : "${DOCKER_POSTGRES_DB_USER:=postgres}"
  : "${DOCKER_POSTGRES_DB_NAME:=SitesIndex}"
  : "${DOCKER_CROWLER_DB_USER:=crowler}"
fi

# Detect host arch -> default platform
ARCH=$(uname -m)
PLATFORM="linux/amd64"
POSTGRES_IMAGE=""
if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
  PLATFORM="linux/arm64/v8"
  POSTGRES_IMAGE="arm64v8/"
fi
export PLATFORMS=$PLATFORM
export DOCKER_DEFAULT_PLATFORM=$PLATFORM
export DOCKER_POSTRGES_IMAGE=$POSTGRES_IMAGE

# Selenium version pins (as you had — keep current tested version)
export SELENIUM_VER_NUM="${SELENIUM_VER_NUM:-4.28.1}"
export SELENIUM_BUILDID="${SELENIUM_BUILDID:-20250202}"
export SELENIUM_RELEASE="${SELENIUM_VER_NUM}-${SELENIUM_BUILDID}"
CURRENT_DATE=$(date +%Y%m%d)
export SELENIUM_PROD_RELEASE="${SELENIUM_VER_NUM}-${CURRENT_DATE}"
: "${SELENIUM_PORT:=4444}"

# === default to chromium if forced ===
if [ "${FORCE_CHROMIUM}" = "true" ]; then
  export DOCKER_SELENIUM_IMAGE="selenium/standalone-chromium:${SELENIUM_PROD_RELEASE}"
else
  # old logic with arch/version checks
  DOCKER_SELENIUM_IMAGE="selenium/standalone-chrome:${SELENIUM_PROD_RELEASE}"
  if [ "$PLATFORM" = "linux/arm64/v8" ]; then
    # chromium on arm64 by default, unless older than 4.21
    DOCKER_SELENIUM_IMAGE="selenium/standalone-chromium:${SELENIUM_PROD_RELEASE}"
    SELENIUM_VER_NUM_INT=$(version_to_integer "$SELENIUM_VER_NUM")
    TARGET_VER_INT=$(version_to_integer "4.21.0")
    if [ "$SELENIUM_VER_NUM_INT" -lt "$TARGET_VER_INT" ]; then
      DOCKER_SELENIUM_IMAGE="selenium/standalone-firefox:${SELENIUM_PROD_RELEASE}"
    fi
  fi
fi
export DOCKER_SELENIUM_IMAGE

echo "Building Selenium image for ${PLATFORM} -> ${DOCKER_SELENIUM_IMAGE}"

# Fetch and patch sources
rm -rf docker-selenium && git clone https://github.com/SeleniumHQ/docker-selenium.git ./docker-selenium
rm -rf Rbee && git clone https://github.com/pzaino/RBee.git ./Rbee && mkdir -p ./Rbee/pkg

pushd ./docker-selenium >/dev/null
  git checkout "${SELENIUM_RELEASE}"
  git pull --ff-only origin "${SELENIUM_RELEASE}" || true

  # Apply patches if available (kept as your original logic)
  if [ -d "../selenium-patches/${SELENIUM_VER_NUM}" ]; then
    echo "Applying selenium patches for ${SELENIUM_VER_NUM}"
    # ... (your existing patch block exactly as-is) ...
  else
    echo "No patches found for Selenium ${SELENIUM_VER_NUM}, continuing..."
  fi

  # Add RBee + assets
  cp -r ../Rbee ./Standalone/
  cp ../selenium-patches/browserAutomation.conf ./Standalone/Rbee/browserAutomation.conf || true
  mkdir -p ./Standalone/images
  cp -r ../images/crowler-vdi-bg.png ./Standalone/images/ || true

  # === force chromium if requested ===
  if [ "${FORCE_CHROMIUM}" = "true" ]; then
    make standalone_chromium
  else
    if [ "$PLATFORM" = "linux/arm64/v8" ]; then
      SELENIUM_VER_NUM_INT=$(version_to_integer "$SELENIUM_VER_NUM")
      TARGET_VER_INT=$(version_to_integer "4.21.0")
      if [ "$SELENIUM_VER_NUM_INT" -lt "$TARGET_VER_INT" ]; then
        make standalone_firefox
      else
        make standalone_chromium
      fi
    else
      make standalone_chrome
    fi
  fi
popd >/dev/null

# === optional compose (skip in CI) ===
if [ "${SKIP_COMPOSE}" = "true" ]; then
  echo "SKIP_COMPOSE=true: not running docker compose."
else
  # shellcheck disable=SC2086
  docker compose ${pars}
fi
