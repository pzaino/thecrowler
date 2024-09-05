#!/bin/bash

# Function to find the daemon.json file
find_docker_daemon_json() {
    # Look for daemon.json in standard locations or custom paths
    possible_locations=("/etc/docker/daemon.json" "/usr/local/docker/daemon.json" "/etc/default/docker/daemon.json" "$HOME/.docker/daemon.json")

    for location in "${possible_locations[@]}"; do
        if [ -f "$location" ]; then
            echo "$location"
            return
        fi
    done

    # If no file found, return default location to create a new one
    echo "/etc/docker/daemon.json"
}

# Get the daemon.json file location
DOCKER_DAEMON_CONFIG=$(find_docker_daemon_json)

# Check if daemon.json exists in the found or default location
enabled=0
if [ ! -f "$DOCKER_DAEMON_CONFIG" ]; then
    # If the file doesn't exist, create it with containerd enabled
    enabled=1
    echo "Creating Docker daemon configuration file with containerd enabled at $DOCKER_DAEMON_CONFIG..."
    sudo mkdir -p $(dirname "$DOCKER_DAEMON_CONFIG")  # Ensure the directory exists
    sudo bash -c "cat > $DOCKER_DAEMON_CONFIG" <<EOL
{
  "features": {
    "containerd": true,
    "buildkit": false
  },
  "storage-driver": "overlay2",
  "experimental": true
}
EOL
else
    # If the file exists, check if containerd is already enabled
    CONTAINERD_ENABLED=$(grep -Po '"containerd":\s*\Ktrue' "$DOCKER_DAEMON_CONFIG")

    if [ "$CONTAINERD_ENABLED" == "true" ]; then
        echo "Containerd is already enabled in $DOCKER_DAEMON_CONFIG."
    else
        # If containerd is not enabled, modify the file to enable it
        echo "Enabling containerd in Docker configuration at $DOCKER_DAEMON_CONFIG..."
        enabled=1
        # Modify the daemon.json to enable containerd
        sudo jq '.features.containerd = true' "$DOCKER_DAEMON_CONFIG" | sudo tee "$DOCKER_DAEMON_CONFIG" > /dev/null
    fi
fi

# Restart Docker to apply the changes if any
if [ $enabled -eq 1 ]; then
    echo "Restarting Docker service..."
    sudo systemctl restart docker
fi

# Verify that Docker restarted successfully
if [ $? -eq 0 ]; then
    echo "Docker has been restarted successfully, and containerd is enabled."
else
    echo "Failed to restart Docker. Please check the Docker service."
fi
