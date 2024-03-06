# Installing the CROWler

## Prerequisites

The crowler is designed to run from a Raspberry Pi 3B+ type of hardware all the way up to a full fledged server and/or cloud computing.

It has been tested on ARM64 and x86_64 architectures. It should work on ARM32 and x86_32 as well, but it has not been tested.

It has been tested on various Linux distributions, including OpenSUSE, Ubuntu, Debian, and Raspbian. It should work on other distributions as well, but it has not been tested.

It has also been tested on MacOS. It should work on Windows as well, but it has not been tested.

The following software is required:

- Docker
- Docker Compose

## Installation

The installation is done by cloning the repository and running the `docker-build.sh` or `docker-rebuild.sh` scripts.

The `docker-build.sh` script will build the Docker images and start the containers. The `docker-rebuild.sh` script will clean up everything and then rebuild the Docker images and start the containers.

Before you build your images:

- you MUST create a config.yaml file and place it in the root of the project. More details on how to write your config.yaml file can be found in the [Configuration](./config_yaml.md) section.
- you MUST define some environment variables in your shell. More details on how to define your environment variables can be found in the [Environment Variables](./env_vars.md) section.

That's it! When you've configured your ENV variables and written your config.yaml, just run:

```bash
./docker-build.sh
```

You should now have a running CROWler instance waiting to receive Sources to crawl.

Enjoy! :)
