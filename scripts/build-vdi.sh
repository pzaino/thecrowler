#!/bin/bash
set -euo pipefail

# shellcheck disable=SC2124
pars="${*:-}"

# === CI toggles ===
: "${SKIP_COMPOSE:=false}"     # set to "true" in CI to avoid docker compose + env checks
export DOCKER_BUILDKIT="${DOCKER_BUILDKIT:-1}"

# Return an absolute path without depending on GNU realpath.
absolute_path() {
  case "$1" in
    /*)
      printf '%s\n' "$1"
      ;;
    *)
      printf '%s/%s\n' \
        "$(cd "$(dirname "$1")" && pwd -P)" \
        "$(basename "$1")"
      ;;
  esac
}

# BSD sed requires an explicit empty backup suffix for in-place editing.
sed_in_place() {
  if [ "$(uname -s)" = "Darwin" ]; then
    sed -i '' "$@"
  else
    sed -i "$@"
  fi
}


# Ubuntu 24.04 stores its repositories in deb822 .sources files and uses plain
# HTTP by default. Some routers and captive portals transparently rewrite those
# requests to local endpoints such as dsldevice/httpi.lp. Insert an HTTPS and
# retry guard before the first apt-get update in Selenium's Base image.
inject_ubuntu_https_sources_guard() {
  local dockerfile="$1"
  local guard_file="${dockerfile}.https-guard.$$"
  local temporary_file="${dockerfile}.tmp.$$"

  if grep -q 'CROWLER_FORCE_UBUNTU_HTTPS' "$dockerfile"; then
    return 0
  fi

  cat > "$guard_file" <<'DOCKERFILE'
# CROWLER_FORCE_UBUNTU_HTTPS
# Avoid transparent HTTP interception of Ubuntu package downloads and make
# transient package-network failures retry automatically.
RUN set -eux; \
    if [ -f /etc/apt/sources.list ]; then \
      sed -i \
        -e 's#http://archive.ubuntu.com#https://archive.ubuntu.com#g' \
        -e 's#http://security.ubuntu.com#https://security.ubuntu.com#g' \
        -e 's#http://ports.ubuntu.com#https://ports.ubuntu.com#g' \
        /etc/apt/sources.list; \
    fi; \
    if [ -d /etc/apt/sources.list.d ]; then \
      find /etc/apt/sources.list.d -type f \( -name '*.list' -o -name '*.sources' \) \
        -exec sed -i \
          -e 's#http://archive.ubuntu.com#https://archive.ubuntu.com#g' \
          -e 's#http://security.ubuntu.com#https://security.ubuntu.com#g' \
          -e 's#http://ports.ubuntu.com#https://ports.ubuntu.com#g' \
          {} +; \
    fi; \
    printf '%s\n' \
      'Acquire::Retries "5";' \
      'Acquire::http::Timeout "30";' \
      'Acquire::https::Timeout "30";' \
      > /etc/apt/apt.conf.d/80-crowler-network
DOCKERFILE

  if awk -v guard_file="$guard_file" '
    !inserted && /^[[:space:]]*RUN[[:space:]]+apt-get[[:space:]]+-qqy[[:space:]]+update/ {
      while ((getline guard_line < guard_file) > 0) {
        print guard_line
      }
      close(guard_file)
      inserted = 1
    }

    {
      print
    }

    END {
      if (!inserted) {
        exit 42
      }
    }
  ' "$dockerfile" > "$temporary_file"; then
    mv "$temporary_file" "$dockerfile"
    rm -f "$guard_file"
  else
    local rc=$?
    rm -f "$guard_file" "$temporary_file"
    echo "Unable to insert the Ubuntu HTTPS guard into ${dockerfile}" >&2
    return "$rc"
  fi
}

# Package repositories inside Dockerfiles must also use HTTPS. The Ubuntu base
# source configuration is handled at image-build time by the guard above, while
# this function fixes repository URLs declared directly in Dockerfiles.
rewrite_dockerfile_package_urls() {
  local dockerfile

  for dockerfile in ./Dockerfile* ./*/Dockerfile*; do
    [ -f "$dockerfile" ] || continue

    sed_in_place \
      -e 's#http://archive.ubuntu.com#https://archive.ubuntu.com#g' \
      -e 's#http://security.ubuntu.com#https://security.ubuntu.com#g' \
      -e 's#http://ports.ubuntu.com#https://ports.ubuntu.com#g' \
      -e 's#http://deb.debian.org#https://deb.debian.org#g' \
      -e 's#http://ftp.debian.org#https://ftp.debian.org#g' \
      "$dockerfile"
  done
}

# Remove the temporary Debian Sid repository from the NodeChromium image after
# Chromium has been installed. Leaving Sid enabled allows later apt operations
# in derived images to mix Debian packages into the Ubuntu base image.
append_node_chromium_repo_cleanup() {
  local dockerfile="$1"

  if grep -q 'CROWLER_REMOVE_DEBIAN_SID' "$dockerfile"; then
    return 0
  fi

  cat >> "$dockerfile" <<'DOCKERFILE'

# CROWLER_REMOVE_DEBIAN_SID
# Chromium is already installed. Restore the Ubuntu-only package sources before
# downstream images run apt-get again.
USER root
RUN if [ -f /etc/apt/sources.list ]; then \
      sed -i '/[[:space:]]sid[[:space:]]main[[:space:]]*$/d' /etc/apt/sources.list; \
    fi \
  && if [ -d /etc/apt/sources.list.d ]; then \
      find /etc/apt/sources.list.d -type f -name '*.list' \
        -exec sed -i '/[[:space:]]sid[[:space:]]main[[:space:]]*$/d' {} +; \
    fi \
  && rm -f \
      /etc/apt/trusted.gpg.d/debian-archive-keyring.gpg \
      /etc/apt/trusted.gpg.d/debian-archive-security-keyring.gpg \
  && rm -rf /var/lib/apt/lists/* /var/cache/apt/*
USER ${SEL_UID}
DOCKERFILE
}

# Supervisor 4.2.5 imports pkg_resources at startup. On Ubuntu Noble that module
# is provided by python3-pkg-resources. Install it explicitly in the final image
# and validate Supervisor while the image is still being built.
append_supervisor_runtime_guard() {
  local dockerfile="$1"

  if grep -q 'CROWLER_SUPERVISOR_RUNTIME' "$dockerfile"; then
    return 0
  fi

  cat >> "$dockerfile" <<'DOCKERFILE'

# CROWLER_SUPERVISOR_RUNTIME
USER root
RUN apt-get -o Acquire::Retries=5 update \
  && apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
      python3-pkg-resources \
  && /usr/bin/python3 -c 'import pkg_resources; print(pkg_resources.__file__)' \
  && /usr/bin/supervisord --version \
  && rm -rf /var/lib/apt/lists/* /var/cache/apt/*
USER ${SEL_UID}:${SEL_GID}
DOCKERFILE
}

# Test the runtime from the completed local image before the workflow tags or
# pushes it. This catches missing Python modules and broken Supervisor installs.
verify_supervisor_runtime() {
  local image="$1"
  local platform="$2"

  echo "Verifying Supervisor runtime in ${image} for ${platform}"

  docker run --rm \
    --pull=never \
    --platform "$platform" \
    --entrypoint /bin/bash \
    "$image" \
    -lc '
      set -e
      /usr/bin/python3 -c "import pkg_resources; print(pkg_resources.__file__)"
      /usr/bin/supervisord --version
    '
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

# Honour an explicitly requested target when cross-building under QEMU. Falling
# back to the host architecture keeps the command-line behaviour unchanged.
REQUESTED_PLATFORM="${TARGET_PLATFORM:-${DOCKER_DEFAULT_PLATFORM:-}}"
if [ -z "$REQUESTED_PLATFORM" ]; then
  case "$(uname -m)" in
    aarch64|arm64) REQUESTED_PLATFORM="linux/arm64/v8" ;;
    x86_64|amd64) REQUESTED_PLATFORM="linux/amd64" ;;
    *) echo "Unsupported host architecture: $(uname -m)" >&2; exit 1 ;;
  esac
fi

case "$REQUESTED_PLATFORM" in
  linux/amd64) PLATFORM="linux/amd64"; POSTGRES_IMAGE="" ;;
  linux/arm64|linux/arm64/v8) PLATFORM="linux/arm64/v8"; POSTGRES_IMAGE="arm64v8/" ;;
  *) echo "Unsupported target platform: $REQUESTED_PLATFORM" >&2; exit 1 ;;
esac
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

# Chromium is available on both supported architectures; unlike Google Chrome,
# it also gives the two builds a consistent browser and image repository.
export DOCKER_SELENIUM_IMAGE="selenium/standalone-chromium:${SELENIUM_PROD_RELEASE}"

if [ -n "${VDI_IMAGE_OUTPUT_FILE:-}" ]; then
  printf '%s\n' "$DOCKER_SELENIUM_IMAGE" > "$VDI_IMAGE_OUTPUT_FILE"
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

    # A multi-platform NodeChromium patch applies to amd64 as well as arm64.
    # Only fall back to an ARM-specific patch when no shared patch exists.
    chromium_patch="../selenium-patches/${SELENIUM_VER_NUM}/Dockerfile_Chromium_multi_${SELENIUM_VER_NUM}.patch"
    if [ ! -f "$chromium_patch" ] && [ "$PLATFORM" = "linux/arm64/v8" ]; then
      chromium_patch="../selenium-patches/${SELENIUM_VER_NUM}/Dockerfile_Chromium_ARM64_${SELENIUM_VER_NUM}.patch"
    fi
    if [ -f "$chromium_patch" ]; then
      chromium_patch=$(absolute_path "$chromium_patch")
      echo "Applying NodeChromium patch for ${PLATFORM}: ${chromium_patch}"
      pushd ./NodeChromium >/dev/null
        patch Dockerfile "$chromium_patch" || { echo "Failed to apply NodeChromium patch: ${chromium_patch}"; exit 1; }
      popd >/dev/null
    else
      echo "No NodeChromium patch found for Selenium ${SELENIUM_VER_NUM} on ${PLATFORM}; checking built-in compatibility guardrails."
    fi
  else
    echo "No patches found for Selenium ${SELENIUM_VER_NUM}, continuing…"
  fi

  # Force package-manager traffic to HTTPS before any Selenium image stage is
  # built. This prevents transparent HTTP proxying by routers/captive portals.
  inject_ubuntu_https_sources_guard "./Base/Dockerfile"
  rewrite_dockerfile_package_urls

  # Older docker-selenium releases install Chromium from Debian sid on top of
  # Ubuntu. Debian base-files 14's merged-/usr diversions conflict with the
  # diversions already present in Ubuntu unless they are removed first. Keep
  # this as a version-independent guardrail: local .env files may select a
  # release for which the repository has no dedicated NodeChromium patch.
  if grep -q 'deb ${CHROMIUM_DEB_SITE}/ sid main' ./NodeChromium/Dockerfile \
      && ! grep -q 'dpkg-divert --package base-files --no-rename --remove' ./NodeChromium/Dockerfile; then
    echo "Applying built-in NodeChromium merged-/usr compatibility fix for ${PLATFORM}"
    dockerfile="./NodeChromium/Dockerfile"
    temporary_file="${dockerfile}.tmp.$$"

    awk '
      {
        print
      }

      index($0, "archive-key-12-security.asc") &&
      index($0, "gpg --dearmor") {
        print "  && for d in bin lib lib32 lib64 libo32 libx32 sbin; do dpkg-divert --package base-files --no-rename --remove /$d; done \\"
      }
    ' "$dockerfile" > "$temporary_file"

    mv "$temporary_file" "$dockerfile"
  fi

  if grep -q 'deb ${CHROMIUM_DEB_SITE}/ sid main' ./NodeChromium/Dockerfile \
      && ! grep -q 'dpkg-divert --package base-files --no-rename --remove' ./NodeChromium/Dockerfile; then
    echo "Failed to install the Chromium merged-/usr compatibility fix" >&2
    exit 1
  fi
  if grep -q 'deb ${CHROMIUM_DEB_SITE}/ sid main' ./NodeChromium/Dockerfile; then
    echo "Verified NodeChromium merged-/usr compatibility fix for ${PLATFORM}"
  fi

  # Chromium is installed from Debian Sid by this Selenium release. Remove the
  # temporary repository before Standalone performs any later apt operation.
  append_node_chromium_repo_cleanup "./NodeChromium/Dockerfile"

  # Guarantee that Supervisor has the pkg_resources runtime it imports.
  append_supervisor_runtime_guard "./Standalone/Dockerfile"

  # RBee + assets
  cp -r ../Rbee ./Standalone/
  cp ../selenium-patches/browserAutomation.conf ./Standalone/Rbee/browserAutomation.conf || true
  mkdir -p ./Standalone/images
  cp -r ../images/crowler-vdi-bg.png ./Standalone/images/ || true

  # --- Guardrail: if Makefile still contains --attest/--sbom, strip them for docker driver ---
  if grep -q -- '--attest' Makefile || grep -Eq -- '--sbom(=| )' Makefile; then
    echo "Stripping --attest/--sbom flags from Selenium Makefile for docker driver compatibility…"
    sed_in_place 's/--attest[^ ]*//g' Makefile
    sed_in_place 's/--sbom[= ][^ ]*//g' Makefile
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
  # Store tags as a newline-delimited value. This works with Bash 3.2 and avoids
  # mapfile/readarray, which are only available in newer Bash releases.
  UBUNTU_TAGS="$(
    grep -RhoE '^FROM[[:space:]]+ubuntu:([^[:space:]]+)' \
      Base Standalone Node* 2>/dev/null \
      | awk '{print $2}' \
      | cut -d: -f2 \
      | sort -u \
      || true
  )"

  if [ -n "$UBUNTU_TAGS" ]; then
    printf 'Ubuntu tags referenced: %s\n' \
      "$(printf '%s\n' "$UBUNTU_TAGS" | paste -sd ' ' -)"
  fi

  # Rewrite FROM lines to use the mirror (safer than retagging because BuildKit may still HEAD the registry)
  # Examples: FROM ubuntu:noble-20241118.1  ->  FROM public.ecr.aws/docker/library/ubuntu:noble-20241118.1
  # Portable equivalent of find -maxdepth 2. Unmatched globs are skipped by the
  # regular-file check.
  for dockerfile in ./Dockerfile* ./*/Dockerfile*; do
    [ -f "$dockerfile" ] || continue

    sed_in_place -E \
      "s#^FROM[[:space:]]+ubuntu:#FROM ${LIB_MIRROR}/ubuntu:#" \
      "$dockerfile"
  done

  # Prime cache: pull each required ubuntu tag from the mirror with retries
  while IFS= read -r tag; do
    [ -n "$tag" ] || continue

    echo "Pulling ${LIB_MIRROR}/ubuntu:${tag}"
    n=0
    until [ "$n" -ge 5 ]; do
      if docker pull "${LIB_MIRROR}/ubuntu:${tag}"; then
        break
      fi

      n=$((n + 1))
      sleep $((2 ** n))
    done
  done < <(printf '%s\n' "$UBUNTU_TAGS")
  # ===== END mirror fallback =====

  rval=0
  make standalone_chromium || rval=$?

popd >/dev/null

# If rval is 0 then the build succeeded, otherwise it failed. Exit with the same code.
if [ "$rval" -eq 0 ]; then
  verify_supervisor_runtime "$DOCKER_SELENIUM_IMAGE" "$PLATFORM"
  echo "Selenium image build and runtime verification succeeded for ${PLATFORM} -> ${DOCKER_SELENIUM_IMAGE}"
else
  echo "Selenium image build failed for ${PLATFORM} -> ${DOCKER_SELENIUM_IMAGE}" >&2
fi

exit "$rval"
