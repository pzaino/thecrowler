#!/bin/bash

compose_file="docker-compose.yml"

echo "[*] Scanning $compose_file for service resource limits..."

# Read the file line by line
current_service=""
container_name=""
cpus=""
mem_limit=""

apply_limits() {
  if [ -n "$current_service" ] && [ -n "$container_name" ] && [ -n "$cpus" ] && [ -n "$mem_limit" ]; then
    # Check if container is running
    if docker ps --format '{{.Names}}' | grep -q "^$container_name$"; then
      echo "→ Updating container: $container_name (service: $current_service) with CPU: $cpus, Memory: $mem_limit"
      docker update --cpus="$cpus" --memory="$mem_limit" "$container_name"
    else
      echo "⚠️  Container '$container_name' (service: $current_service) not running. Skipping."
    fi
  fi

  # Reset vars
  container_name=""
  cpus=""
  mem_limit=""
}

while IFS= read -r line; do
  # Detect a new service
  if [[ "$line" =~ ^[[:space:]]{2}([a-zA-Z0-9_-]+):[[:space:]]*$ ]]; then
    # Apply limits for the previous service if any
    apply_limits
    current_service="${BASH_REMATCH[1]}"
  fi

  # Extract container_name
  if [[ "$line" =~ container_name:[[:space:]]*\"?([^\"]+)\"? ]]; then
    container_name="${BASH_REMATCH[1]}"
  fi

  # Extract cpus
  if [[ "$line" =~ cpus:[[:space:]]*\"?([0-9.]+)\"? ]]; then
    cpus="${BASH_REMATCH[1]}"
  fi

  # Extract mem_limit
  if [[ "$line" =~ mem_limit:[[:space:]]*\"?([0-9]+[gmGM])\"? ]]; then
    mem_limit="${BASH_REMATCH[1]}"
  fi
done < "$compose_file"

# Apply for the last parsed service
apply_limits

echo "✅ Done."
