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


# Prepare a CA bundle in the Base build context before switching APT sources
# from HTTP to HTTPS. Minimal Ubuntu base images may not yet contain the
# ca-certificates package, so HTTPS cannot bootstrap itself without this file.
prepare_base_ca_bundle() {
  local destination="$1"
  local candidate

  for candidate in \
    "${SSL_CERT_FILE:-}" \
    /etc/ssl/certs/ca-certificates.crt \
    /etc/ssl/cert.pem \
    /etc/pki/tls/certs/ca-bundle.crt; do
    [ -n "$candidate" ] || continue
    [ -s "$candidate" ] || continue

    cp "$candidate" "$destination"
    break
  done

  if [ ! -s "$destination" ]; then
    if command -v curl >/dev/null 2>&1; then
      curl --fail --location --silent --show-error \
        --retry 5 --retry-delay 2 \
        https://curl.se/ca/cacert.pem \
        --output "$destination"
    elif command -v wget >/dev/null 2>&1; then
      wget --tries=5 --output-document="$destination" \
        https://curl.se/ca/cacert.pem
    else
      echo "Unable to obtain a CA bundle for the Ubuntu Base image" >&2
      exit 1
    fi
  fi

  if ! grep -q 'BEGIN CERTIFICATE' "$destination"; then
    echo "Invalid CA bundle written to $destination" >&2
    exit 1
  fi

  chmod 0644 "$destination"
  echo "Prepared Base CA bundle: $destination"
}

# Rewrite Ubuntu package sources to HTTPS before the first apt operation. This
# avoids transparent HTTP interception while retaining Ubuntu Noble packages.
patch_base_apt_transport() {
  local dockerfile="$1"
  local insertion_line
  local temporary_file

  if grep -q 'CROWLER_APT_HTTPS' "$dockerfile"; then
    return 0
  fi

  insertion_line="$(
    awk '
      /^RUN[[:space:]]/ {
        run_line = NR
      }

      /apt-get[[:space:]]+(-[^[:space:]]+[[:space:]]+)*update/ {
        if (run_line > 0) {
          print run_line
          exit
        }
      }
    ' "$dockerfile"
  )"

  if [ -z "$insertion_line" ]; then
    echo "Unable to locate the first Base apt operation in $dockerfile" >&2
    exit 1
  fi

  temporary_file="${dockerfile}.tmp.$$"

  {
    if [ "$insertion_line" -gt 1 ]; then
      sed -n "1,$((insertion_line - 1))p" "$dockerfile"
    fi

    cat <<'DOCKERFILE'
# CROWLER_APT_HTTPS
COPY crowler-ca-certificates.crt /tmp/crowler-ca-certificates.crt
RUN set -eux; \
    mkdir -p /etc/ssl/certs; \
    cp /tmp/crowler-ca-certificates.crt /etc/ssl/certs/ca-certificates.crt; \
    chmod 0644 /etc/ssl/certs/ca-certificates.crt; \
    rm -f /tmp/crowler-ca-certificates.crt; \
    if [ -f /etc/apt/sources.list ]; then \
      sed -i \
        -e 's#http://archive.ubuntu.com#https://archive.ubuntu.com#g' \
        -e 's#http://security.ubuntu.com#https://security.ubuntu.com#g' \
        -e 's#http://ports.ubuntu.com#https://ports.ubuntu.com#g' \
        /etc/apt/sources.list; \
    fi; \
    if [ -d /etc/apt/sources.list.d ]; then \
      find /etc/apt/sources.list.d -type f \
        \( -name '*.list' -o -name '*.sources' \) \
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
      > /etc/apt/apt.conf.d/80crowler-network
DOCKERFILE

    sed -n "${insertion_line},\$p" "$dockerfile"
  } > "$temporary_file"

  mv "$temporary_file" "$dockerfile"
}

# Install packages needed by the final image while the package set is still
# pure Ubuntu. NodeChromium later installs Chromium from Debian Sid, so the
# Standalone stage must not run apt afterward.
patch_base_runtime_packages() {
  local dockerfile="$1"
  local temporary_file

  if grep -Eq '^[[:space:]]+python3-pkg-resources[[:space:]]+\\[[:space:]]*$' "$dockerfile" \
      && grep -Eq '^[[:space:]]+feh[[:space:]]+\\[[:space:]]*$' "$dockerfile"; then
    return 0
  fi

  temporary_file="${dockerfile}.tmp.$$"

  if ! awk '
    /^[[:space:]]+supervisor[[:space:]]+\\[[:space:]]*$/ && !inserted {
      print
      print "    python3-pkg-resources \\"
      print "    feh \\"
      inserted=1
      next
    }
    { print }
    END {
      if (!inserted) {
        exit 42
      }
    }
  ' "$dockerfile" > "$temporary_file"; then
    rm -f "$temporary_file"
    echo "Unable to add runtime packages to $dockerfile" >&2
    exit 1
  fi

  mv "$temporary_file" "$dockerfile"
}

# Modify the repository patch before applying it. The RBee builder must inherit
# the clean Selenium Base image, not node-chromium with Debian Sid libc. The
# final stage also must not call apt to install feh. Keep every replacement
# one-for-one because this file is a normal-diff patch with explicit hunk sizes.
prepare_standalone_patch() {
  local patch_file="$1"
  local temporary_file="${patch_file}.tmp.$$"

  if grep -q 'FROM ${NAMESPACE}/base:${VERSION} AS builder' "$patch_file" \
      && grep -q '^> RUN command -v feh >/dev/null$' "$patch_file"; then
    return 0
  fi

  awk '
    $0 == "> FROM ${NAMESPACE}/${BASE}:${VERSION} AS builder" {
      print "> FROM ${NAMESPACE}/base:${VERSION} AS builder"
      builder=1
      next
    }

    $0 == "> LABEL authors=${AUTHORS}" {
      print "> LABEL authors=\"ZFPSystems\""
      authors=1
      next
    }

    $0 == "> RUN sudo apt-get update && apt-get install -y feh" {
      print "> RUN command -v feh >/dev/null"
      feh=1
      next
    }

    { print }

    END {
      if (!builder || !authors || !feh) {
        exit 42
      }
    }
  ' "$patch_file" > "$temporary_file" || {
    rm -f "$temporary_file"
    echo "Unable to prepare Standalone patch: $patch_file" >&2
    exit 1
  }

  mv "$temporary_file" "$patch_file"
}

# Chromium installation from Debian Sid can remove Ubuntu's
# python3-pkg-resources package from the node-chromium lineage. The clean RBee
# builder still contains the pure-Python pkg_resources module, so copy it into
# the final Standalone stage without running apt in the mixed package image.
restore_pkg_resources_from_builder() {
  local dockerfile="$1"
  local temporary_file

  if grep -Fq \
      'COPY --from=builder /usr/lib/python3/dist-packages/pkg_resources /usr/lib/python3/dist-packages/pkg_resources' \
      "$dockerfile"; then
    return 0
  fi

  temporary_file="${dockerfile}.tmp.$$"

  if ! awk '
    {
      print
    }

    /^[[:space:]]*RUN command -v feh >\/dev\/null[[:space:]]*$/ && !inserted {
      print "COPY --from=builder /usr/lib/python3/dist-packages/pkg_resources /usr/lib/python3/dist-packages/pkg_resources"
      inserted=1
    }

    END {
      if (!inserted) {
        exit 42
      }
    }
  ' "$dockerfile" > "$temporary_file"; then
    rm -f "$temporary_file"
    echo "Unable to restore pkg_resources in $dockerfile" >&2
    exit 1
  fi

  mv "$temporary_file" "$dockerfile"
}

# Use HTTPS for the temporary Debian Chromium repository.
patch_node_chromium_apt_transport() {
  local dockerfile="$1"

  sed_in_place \
    's#ARG CHROMIUM_DEB_SITE="http://deb.debian.org/debian"#ARG CHROMIUM_DEB_SITE="https://deb.debian.org/debian"#' \
    "$dockerfile"
}

# Remove the temporary Debian Sid repository once Chromium is installed. This
# prevents accidental package mixing in later derived stages.
append_node_chromium_repo_cleanup() {
  local dockerfile="$1"

  if grep -q 'CROWLER_REMOVE_DEBIAN_SID' "$dockerfile"; then
    return 0
  fi

  cat >> "$dockerfile" <<'DOCKERFILE'

# CROWLER_REMOVE_DEBIAN_SID
USER root
RUN if [ -f /etc/apt/sources.list ]; then \
      sed -i '/[[:space:]]sid[[:space:]]main[[:space:]]*$/d' /etc/apt/sources.list; \
    fi \
  && if [ -d /etc/apt/sources.list.d ]; then \
      find /etc/apt/sources.list.d -type f \
        \( -name '*.list' -o -name '*.sources' \) \
        -exec sed -i '/[[:space:]]sid[[:space:]]main[[:space:]]*$/d' {} +; \
    fi \
  && rm -f \
      /etc/apt/trusted.gpg.d/debian-archive-keyring.gpg \
      /etc/apt/trusted.gpg.d/debian-archive-security-keyring.gpg \
  && rm -rf /var/lib/apt/lists/* /var/cache/apt/*
USER ${SEL_UID}
DOCKERFILE
}

# Validate the runtime in the final Standalone stage without running apt there.
append_standalone_runtime_guard() {
  local dockerfile="$1"

  if grep -q 'CROWLER_SUPERVISOR_RUNTIME' "$dockerfile"; then
    return 0
  fi

  cat >> "$dockerfile" <<'DOCKERFILE'

# CROWLER_SUPERVISOR_RUNTIME
USER root
RUN command -v feh >/dev/null \
  && /usr/bin/python3 -c 'import pkg_resources; print(pkg_resources.__file__)' \
  && /usr/bin/supervisord --version
USER ${SEL_UID}:${SEL_GID}
DOCKERFILE
}

append_standalone_vdi_ports() {
  local dockerfile="$1"

  if grep -Eq \
      '^[[:space:]]*EXPOSE[[:space:]]+4444[[:space:]]+5900[[:space:]]+7900[[:space:]]+9222[[:space:]]*$' \
      "$dockerfile"; then
    return 0
  fi

  cat >> "$dockerfile" <<'DOCKERFILE'

# CROWLER_VDI_PORTS
# CROWler VDI service contract.
EXPOSE 4444 5900 7900 9222
# Reserved for the future RBee API.
EXPOSE 3000
DOCKERFILE
}

verify_generated_dockerfiles() {
  local standalone_dockerfile="$1"

  grep -F 'FROM ${NAMESPACE}/base:${VERSION} AS builder' \
    "$standalone_dockerfile" >/dev/null || {
      echo "Standalone RBee builder is not using the clean Selenium Base image" >&2
      sed -n '1,130p' "$standalone_dockerfile" >&2
      exit 1
    }

  if grep -F 'RUN sudo apt-get update && apt-get install -y feh' \
      "$standalone_dockerfile" >/dev/null; then
    echo "Standalone still installs feh after Debian Sid was introduced" >&2
    exit 1
  fi

  grep -F 'COPY --from=builder /usr/lib/python3/dist-packages/pkg_resources /usr/lib/python3/dist-packages/pkg_resources' \
    "$standalone_dockerfile" >/dev/null || {
      echo "Standalone does not restore pkg_resources from the clean builder" >&2
      exit 1
    }

  grep -F '# CROWLER_SUPERVISOR_RUNTIME' \
    "$standalone_dockerfile" >/dev/null || {
      echo "Standalone runtime validation was not added" >&2
      exit 1
    }

  # This must be declared by the final Standalone Dockerfile itself. Inherited
  # EXPOSE metadata is deliberately insufficient for the CROWler VDI contract.
  grep -Eq '^[[:space:]]*EXPOSE[[:space:]]+4444[[:space:]]+5900[[:space:]]+7900[[:space:]]+9222[[:space:]]*$' \
    "$standalone_dockerfile" || {
      echo "Standalone does not explicitly expose the CROWler VDI service ports" >&2
      exit 1
    }

  grep -Eq '^[[:space:]]*EXPOSE[[:space:]]+3000[[:space:]]*$' \
    "$standalone_dockerfile" || {
      echo "Standalone does not reserve port 3000 for the future RBee API" >&2
      exit 1
    }

}

# Test the completed image before the workflow tags or publishes it.
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
      command -v feh
      /usr/bin/python3 -c "import pkg_resources; print(pkg_resources.__file__)"
      /usr/bin/supervisord --version
    '
}

print_vdi_diagnostics() {
  local container="$1"

  echo "===== docker inspect =====" >&2
  docker inspect --format \
    'status={{.State.Status}} running={{.State.Running}} restarting={{.State.Restarting}} restart_count={{.RestartCount}} exit={{.State.ExitCode}} error={{.State.Error}}' \
    "$container" >&2 2>/dev/null || true
  echo "===== docker logs =====" >&2
  docker logs "$container" >&2 2>/dev/null || true
  echo "===== supervisor status =====" >&2
  timeout 10 docker exec "$container" supervisorctl status >&2 2>/dev/null || true
  echo "===== listeners =====" >&2
  timeout 10 docker exec "$container" /bin/bash -lc \
    'command -v ss >/dev/null && ss -lntp || (command -v netstat >/dev/null && netstat -lntp) || true' \
    >&2 2>/dev/null || true
}

wait_for_supervisor() {
  local container="$1"
  local attempt

  for ((attempt = 1; attempt <= 60; attempt++)); do
    if [ "$(docker inspect --format '{{.State.Running}}' "$container" 2>/dev/null || true)" != "true" ]; then
      echo "Container stopped while waiting for Supervisor" >&2
      return 1
    fi
    if timeout 5 docker exec "$container" supervisorctl pid >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  echo "Supervisor did not become responsive within 60 seconds" >&2
  return 1
}

wait_for_supervisor_service() {
  local container="$1"
  local service="$2"
  local attempt status

  for ((attempt = 1; attempt <= 60; attempt++)); do
    status="$(timeout 5 docker exec "$container" supervisorctl status "$service" 2>/dev/null || true)"
    if grep -Eq '(^|[[:space:]])RUNNING([[:space:]]|$)' <<<"$status"; then
      echo "$status"
      return 0
    fi
    sleep 1
  done

  echo "Supervisor service did not become RUNNING within 60 seconds: $service" >&2
  [ -z "$status" ] || echo "Last status: $status" >&2
  return 1
}

wait_for_tcp() {
  local container="$1"
  local host="$2"
  local port="$3"
  local attempt

  for ((attempt = 1; attempt <= 60; attempt++)); do
    if timeout 5 docker exec "$container" /bin/bash -lc \
        "</dev/tcp/${host}/${port}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  echo "TCP endpoint did not accept a connection within 60 seconds: ${host}:${port}" >&2
  return 1
}

wait_for_http() {
  local container="$1"
  local url="$2"
  local jq_filter="${3:-}"
  local attempt

  for ((attempt = 1; attempt <= 60; attempt++)); do
    if [ -n "$jq_filter" ]; then
      if timeout 5 docker exec "$container" curl -fsS "$url" 2>/dev/null \
          | jq -e "$jq_filter" >/dev/null 2>&1; then
        return 0
      fi
    elif timeout 5 docker exec "$container" curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  echo "HTTP endpoint did not become ready within 60 seconds: $url" >&2
  return 1
}

run_vdi_smoke_checks() {
  local container="$1"
  local attempt service session_response session_id
  local cdp_ready=false

  wait_for_supervisor "$container" || return 1
  for service in xvfb vnc novnc selenium-standalone browserAutomation dbus; do
    wait_for_supervisor_service "$container" "$service" || return 1
  done

  # RUNNING means the processes survived Supervisor's start interval, not that
  # their sockets are already bound. Poll every public endpoint independently.
  wait_for_http "$container" http://127.0.0.1:4444/status '.value.ready == true' || return 1
  wait_for_tcp "$container" 127.0.0.1 5900 || return 1
  wait_for_http "$container" http://127.0.0.1:7900/ || return 1

  # Chromium is launched lazily by Selenium. Start a real WebDriver session so
  # the fixed direct CDP endpoint is exercised rather than merely checking its
  # EXPOSE metadata.
  session_response="$(timeout 30 docker exec "$container" curl -fsS \
    -H 'Content-Type: application/json' \
    -d '{"capabilities":{"alwaysMatch":{"browserName":"chrome","goog:chromeOptions":{"args":["--no-first-run","--remote-debugging-port=9222","--remote-debugging-address=0.0.0.0"]}}}}' \
    http://127.0.0.1:4444/session)" || return 1
  session_id="$(printf '%s' "$session_response" | jq -er '.value.sessionId')" || {
    printf 'Unexpected WebDriver response: %s\n' "$session_response" >&2
    return 1
  }

  for ((attempt = 1; attempt <= 30; attempt++)); do
    if timeout 5 docker exec "$container" curl -fsS http://127.0.0.1:9222/json/version \
        | jq -e '.Browser and .webSocketDebuggerUrl' >/dev/null 2>&1; then
      cdp_ready=true
      break
    fi
    sleep 1
  done

  timeout 5 docker exec "$container" curl -fsS -X DELETE \
    "http://127.0.0.1:4444/session/${session_id}" >/dev/null || true
  [ "$cdp_ready" = true ] || return 1

  [ "$(docker inspect --format '{{.State.Running}}' "$container")" = "true" ] || return 1
  [ "$(docker inspect --format '{{.RestartCount}}' "$container")" = "0" ] || return 1
  timeout 5 docker exec "$container" supervisorctl pid >/dev/null
}

# Exercise the image's normal entrypoint and all currently active VDI services.
verify_vdi_runtime() {
  local image="$1"
  local platform="$2"
  local container="crowler-vdi-smoke-$$"
  local result=0

  echo "Running VDI entrypoint smoke test in ${image} for ${platform}"
  docker run --detach --name "$container" --pull=never --platform "$platform" \
    --shm-size=2g "$image" >/dev/null

  run_vdi_smoke_checks "$container" || result=$?
  if [ "$result" -ne 0 ]; then
    echo "VDI runtime smoke test failed for ${image} (${platform})" >&2
    print_vdi_diagnostics "$container"
  fi

  docker rm --force "$container" >/dev/null 2>&1 || true
  return "$result"
}

resolve_chromium_version() {
  # Explicit override always wins.
  if [ -n "${CHROMIUM_VERSION:-}" ]; then
    echo "Using explicitly requested Chromium version: ${CHROMIUM_VERSION}"
    export CHROMIUM_VERSION
    return 0
  fi

  # chromium version must be compatible with the selenium version.
  # also the chromium version we use is xxx.yyy.zzz.aaa.
  case "${SELENIUM_VER_NUM}" in
    4.27.0)
      # Replace with the exact full version from the known-good 135.0 image.
      CHROMIUM_VERSION="131.0.6778.85"
      CHROMIUM_DEB_SITE="https://snapshot.debian.org/archive/debian/20241204T204112Z"
      ;;

    4.28.1)
      # Replace with the exact full version from the known-good 138.0 image.
      CHROMIUM_VERSION="132.0.6834.159"
      CHROMIUM_DEB_SITE="https://snapshot.debian.org/archive/debian/20250202T205652Z"
      ;;

    *)
      echo "No pinned Chromium version is defined for Selenium ${SELENIUM_VER_NUM}" >&2
      echo "Set CHROMIUM_VERSION explicitly or add this Selenium release to the compatibility map." >&2
      return 1
      ;;
  esac

  export CHROMIUM_VERSION
  export CHROMIUM_DEB_SITE

  echo "Resolved Chromium ${CHROMIUM_VERSION} for Selenium ${SELENIUM_VER_NUM}"
}


##############################
# "MAIN" SCRIPT STARTS HERE
##############################

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
export DOCKER_POSTGRES_IMAGE="$POSTGRES_IMAGE"

# Selenium version pins
export SELENIUM_VER_NUM="${SELENIUM_VER_NUM:-4.28.1}"
export SELENIUM_BUILDID="${SELENIUM_BUILDID:-20250202}"
export SELENIUM_RELEASE="${SELENIUM_VER_NUM}-${SELENIUM_BUILDID}"

resolve_chromium_version

echo "Selenium release : ${SELENIUM_RELEASE}"
echo "Chromium version : ${CHROMIUM_VERSION}"

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
        prepare_standalone_patch "./Dockerfile_Standalone.patch"
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

  # Keep Ubuntu package installation in Base, before NodeChromium introduces
  # Debian Sid packages. The Standalone patch has already been rewritten so its
  # RBee builder uses this clean Base image.
  prepare_base_ca_bundle "./Base/crowler-ca-certificates.crt"
  patch_base_apt_transport "./Base/Dockerfile"
  patch_base_runtime_packages "./Base/Dockerfile"
  patch_node_chromium_apt_transport "./NodeChromium/Dockerfile"
  restore_pkg_resources_from_builder "./Standalone/Dockerfile"
  append_node_chromium_repo_cleanup "./NodeChromium/Dockerfile"
  append_standalone_runtime_guard "./Standalone/Dockerfile"
  append_standalone_vdi_ports "./Standalone/Dockerfile"
  verify_generated_dockerfiles "./Standalone/Dockerfile"

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

  # ==== Build the final image ====
  rval=0
  make standalone_chromium || rval=$?

popd >/dev/null

# If rval is 0 then the build succeeded, otherwise it failed. Exit with the same code.
if [ "$rval" -eq 0 ]; then
  verify_supervisor_runtime "$DOCKER_SELENIUM_IMAGE" "$PLATFORM"
  verify_vdi_runtime "$DOCKER_SELENIUM_IMAGE" "$PLATFORM"
  echo "Selenium image build and runtime verification succeeded for ${PLATFORM} -> ${DOCKER_SELENIUM_IMAGE}"
else
  echo "Selenium image build failed for ${PLATFORM} -> ${DOCKER_SELENIUM_IMAGE}" >&2
fi

exit "$rval"
