#!/bin/bash
set -euo pipefail

# shellcheck disable=SC2124
pars="${*:-}"

# === CI toggles ===
: "${FORCE_CHROMIUM:=false}"   # set to "true" in CI to force chromium build
: "${SKIP_COMPOSE:=false}"     # set to "true" in CI to avoid docker compose + env checks
export DOCKER_BUILDKIT="${DOCKER_BUILDKIT:-1}"

version_to_integer() {
  local version=$1; local padded_version=""
  IFS='.' read -ra parts <<< "$version"
  for part in "${parts[@]}"; do padded_version+=$(printf "%02d" "$part"); done
  echo "$((10#$padded_version))"
}

# Optional config sourcing
if [ -f config.sh ]; then
  source config.sh
elif [ -f .env ]; then
  source .env
else
  echo "config.sh or .env not found! Proceeding with environment vars."
fi

# enforce DB envs only if we will run compose
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
export PLATFORMS="$PLATFORM"
export DOCKER_DEFAULT_PLATFORM="$PLATFORM"
export DOCKER_POSTRGES_IMAGE="$POSTGRES_IMAGE"

# Selenium version pins
export SELENIUM_VER_NUM="${SELENIUM_VER_NUM:-4.28.1}"
export SELENIUM_BUILDID="${SELENIUM_BUILDID:-20250202}"
export SELENIUM_RELEASE="${SELENIUM_VER_NUM}-${SELENIUM_BUILDID}"

CURRENT_DATE=$(date +%Y%m%d)
export SELENIUM_PROD_RELEASE="${SELENIUM_VER_NUM}-${CURRENT_DATE}"
: "${SELENIUM_PORT:=4444}"

# Decide target image name used inside Selenium build
if [ "${FORCE_CHROMIUM}" = "true" ]; then
  export DOCKER_SELENIUM_IMAGE="selenium/standalone-chromium:${SELENIUM_PROD_RELEASE}"
else
  DOCKER_SELENIUM_IMAGE="selenium/standalone-chrome:${SELENIUM_PROD_RELEASE}"
  if [ "$PLATFORM" = "linux/arm64/v8" ]; then
    DOCKER_SELENIUM_IMAGE="selenium/standalone-chromium:${SELENIUM_PROD_RELEASE}"
    SELENIUM_VER_NUM_INT=$(version_to_integer "$SELENIUM_VER_NUM")
    TARGET_VER_INT=$(version_to_integer "4.21.0")
    if [ "$SELENIUM_VER_NUM_INT" -lt "$TARGET_VER_INT" ]; then
      DOCKER_SELENIUM_IMAGE="selenium/standalone-firefox:${SELENIUM_PROD_RELEASE}"
    fi
  fi
  export DOCKER_SELENIUM_IMAGE
fi

echo "Building Selenium image for ${PLATFORM} -> ${DOCKER_SELENIUM_IMAGE}"

# Fetch sources
rm -rf docker-selenium
git clone https://github.com/SeleniumHQ/docker-selenium.git ./docker-selenium
rm -rf Rbee
git clone https://github.com/pzaino/RBee.git ./Rbee
mkdir -p ./Rbee/pkg

pushd ./docker-selenium >/dev/null
  git checkout "${SELENIUM_RELEASE}"
  git pull --ff-only origin "${SELENIUM_RELEASE}" || true

  # === Your patch logic (restored) ===
  if [ -d "../selenium-patches/${SELENIUM_VER_NUM}" ]; then
    echo "Applying selenium patches for ${SELENIUM_VER_NUM}"

    # Optional Dockerfile override for Standalone
    if [ -f "../selenium-patches/${SELENIUM_VER_NUM}/Dockerfile" ]; then
      cp "../selenium-patches/${SELENIUM_VER_NUM}/Dockerfile" "./Standalone/Dockerfile"
    fi

    # Makefile patch
    if [ -f "../selenium-patches/${SELENIUM_VER_NUM}/Makefile-fixed.patch" ]; then
      cp "../selenium-patches/${SELENIUM_VER_NUM}/Makefile-fixed.patch" "./Makefile-fixed.patch"
      if patch Makefile ./Makefile-fixed.patch; then
        echo "Makefile patch applied successfully."
      else
        echo "Failed to apply Makefile patch."; exit 1
      fi
    fi

    # Base Dockerfile patch (noarch preferred)
    if [ -f "../selenium-patches/${SELENIUM_VER_NUM}/Dockerfile_Base_noarch_${SELENIUM_VER_NUM}.patch" ]; then
      pushd ./Base >/dev/null
        cp "../../selenium-patches/${SELENIUM_VER_NUM}/Dockerfile_Base_noarch_${SELENIUM_VER_NUM}.patch" "./Dockerfile_Base.patch"
        patch Dockerfile ./Dockerfile_Base.patch || { echo "Failed to apply Base noarch patch"; exit 1; }
      popd >/dev/null
    else
      if [ "$PLATFORM" = "linux/arm64/v8" ]; then
        patch_file="Dockerfile_Base_ARM64_${SELENIUM_VER_NUM}.patch"
        if [ -f "../selenium-patches/${SELENIUM_VER_NUM}/${patch_file}" ]; then
          pushd ./Base >/dev/null
            cp "../../selenium-patches/${SELENIUM_VER_NUM}/${patch_file}" "./${patch_file}"
            patch Dockerfile "./${patch_file}" || { echo "Failed to apply Base ARM64 patch"; exit 1; }
          popd >/dev/null
        else
          echo "No Base ARM64 patch found for ${SELENIUM_VER_NUM}, skipping."
        fi
      fi
    fi

    # Standalone multi-platform patch
    if [ -f "../selenium-patches/${SELENIUM_VER_NUM}/Dockerfile_Standalone_multi_${SELENIUM_VER_NUM}.patch" ]; then
      pushd ./Standalone >/dev/null
        cp "../../selenium-patches/${SELENIUM_VER_NUM}/Dockerfile_Standalone_multi_${SELENIUM_VER_NUM}.patch" "./Dockerfile_Standalone.patch"
        patch Dockerfile ./Dockerfile_Standalone.patch || { echo "Failed to apply Standalone multi patch"; exit 1; }
      popd >/dev/null
    else
      if [ "$PLATFORM" = "linux/arm64/v8" ]; then
        patch_file="Dockerfile_Base_ARM64_${SELENIUM_VER_NUM}.patch"
        if [ -f "../selenium-patches/${SELENIUM_VER_NUM}/${patch_file}" ]; then
          pushd ./Base >/dev/null
            cp "../../selenium-patches/${SELENIUM_VER_NUM}/${patch_file}" "./${patch_file}"
            patch Dockerfile "./${patch_file}" || { echo "Failed to apply duplicate Base ARM64 patch"; exit 1; }
          popd >/dev/null
        fi
      fi
    fi

    # Chromium (NodeChromium) patch for ARM64 or multi
    if [ "$PLATFORM" = "linux/arm64/v8" ]; then
      patch_file="Dockerfile_Chromium_ARM64_${SELENIUM_VER_NUM}.patch"
      if [ ! -f "../selenium-patches/${SELENIUM_VER_NUM}/${patch_file}" ]; then
        patch_file="Dockerfile_Chromium_multi_${SELENIUM_VER_NUM}.patch"
      fi
      if [ -f "../selenium-patches/${SELENIUM_VER_NUM}/${patch_file}" ]; then
        pushd ./NodeChromium >/dev/null
          cp "../../selenium-patches/${SELENIUM_VER_NUM}/${patch_file}" "./${patch_file}"
          patch Dockerfile "./${patch_file}" || { echo "Failed to apply Chromium patch"; exit 1; }
        popd >/dev/null
      else
        echo "No Chromium patch file found for ${SELENIUM_VER_NUM}, skipping."
      fi
    fi
  else
    echo "No patches found for Selenium ${SELENIUM_VER_NUM}, continuing…"
  fi

  # RBee + assets
  cp -r ../Rbee ./Standalone/
  cp ../selenium-patches/browserAutomation.conf ./Standalone/Rbee/browserAutomation.conf || true
  mkdir -p ./Standalone/images
  cp -r ../images/crowler-vdi-bg.png ./Standalone/images/ || true

  # --- Guardrail: if Makefile still contains --attest/--sbom, strip them for docker driver ---
  if grep -q -- '--attest' Makefile || grep -Eq -- '--sbom(=| )' Makefile; then
    echo "Stripping --attest/--sbom flags from Selenium Makefile for docker driver compatibility…"
    sed -i 's/--attest[^ ]*//g' Makefile
    sed -i 's/--sbom[= ][^ ]*//g' Makefile
  fi

  # ===== Docker Hub mirror fallback for library images (ubuntu:*) =====
  pick_lib_mirror() {
    # prefer AWS Public ECR mirror, then Google mirror, then Docker Hub
    for pfx in "public.ecr.aws/docker/library" "mirror.gcr.io/library" "docker.io/library"; do
      if docker buildx imagetools inspect "${pfx}/ubuntu:latest" >/dev/null 2>&1; then
        echo "${pfx}"; return 0
      fi
    done
    # last resort
    echo "docker.io/library"
  }

  LIB_MIRROR="${LIB_MIRROR_OVERRIDE:-$(pick_lib_mirror)}"
  echo "Using library mirror prefix: ${LIB_MIRROR}"

  # Collect ubuntu tags used across Selenium Dockerfiles (Base, Standalone, Node*)
  mapfile -t UBUNTU_TAGS < <(grep -RhoE '^FROM[[:space:]]+ubuntu:([^[:space:]]+)' \
      Base Standalone Node* 2>/dev/null | awk '{print $2}' | cut -d: -f2 | sort -u)

  if [ "${#UBUNTU_TAGS[@]}" -gt 0 ]; then
    echo "Ubuntu tags referenced: ${UBUNTU_TAGS[*]}"
  fi

  # Rewrite FROM lines to use the mirror (safer than retagging because BuildKit may still HEAD the registry)
  # Examples: FROM ubuntu:noble-20241118.1  ->  FROM public.ecr.aws/docker/library/ubuntu:noble-20241118.1
  find . -maxdepth 2 -type f -name 'Dockerfile*' -print0 | xargs -0 sed -i -E \
    "s#^FROM[[:space:]]+ubuntu:#FROM ${LIB_MIRROR}/ubuntu:#"

  # Prime cache: pull each required ubuntu tag from the mirror with retries
  for tag in "${UBUNTU_TAGS[@]}"; do
    echo "Pulling ${LIB_MIRROR}/ubuntu:${tag}"
    n=0; until [ $n -ge 5 ]; do
      if docker pull "${LIB_MIRROR}/ubuntu:${tag}"; then break; fi
      n=$((n+1)); sleep $((2**n))
    done
  done
  # ===== END mirror fallback =====

  # Build (Chromium forced in CI if requested)
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

# optional compose
if [ "${SKIP_COMPOSE}" = "true" ]; then
  echo "SKIP_COMPOSE=true: not running docker compose."
else
  # shellcheck disable=SC2086
  docker compose ${pars}
fi
