#!/bin/bash

# This script is used by theCROWler build process to enable containerd in Docker.
# Containerd is recommended, as it is a lightweight container runtime that is used by Docker.

# Define Docker daemon configuration file location
DOCKER_DAEMON_CONFIG="/etc/docker/daemon.json"

# Check if the Docker daemon.json file exists
if [ ! -f "$DOCKER_DAEMON_CONFIG" ]; then
    # If the file doesn't exist, create it with containerd enabled
    echo "Creating Docker daemon configuration file with containerd enabled..."
    sudo bash -c "cat > $DOCKER_DAEMON_CONFIG" <<EOL
{
  "features": {
    "containerd": true
  }
}
EOL
else
    # If the file exists, check if containerd is already enabled
    CONTAINERD_ENABLED=$(grep -Po '"containerd":\s*\Ktrue' $DOCKER_DAEMON_CONFIG)

    if [ "$CONTAINERD_ENABLED" == "true" ]; then
        echo "Containerd is already enabled in $DOCKER_DAEMON_CONFIG."
    else
        # If containerd is not enabled, modify the file to enable it
        echo "Enabling containerd in Docker configuration..."

        # Modify the daemon.json to enable containerd
        sudo jq '.features.containerd = true' $DOCKER_DAEMON_CONFIG | sudo tee $DOCKER_DAEMON_CONFIG > /dev/null
    fi
fi

# Restart Docker to apply the changes
echo "Restarting Docker service..."
sudo systemctl restart docker

# Verify that Docker restarted successfully
rval=$?
if [ $rval -eq 0 ]; then
    echo "Docker has been restarted successfully, and containerd is enabled."
else
    echo "Failed to restart Docker. Please check the Docker service."
fi
