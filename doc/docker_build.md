# docker_build

The manual build of the CROWler can be a complex activity, as it requires the installation of several dependencies and the configuration of the environment. To simplify this process, the `docker_build` script was created. This script is responsible for building the CROWler docker images and running them in separate containers.

docker_build takes care of building the CROWler docker images using the config.yml file and determine the platform (x86_64 or ARM64) you want to use and configuring the build appropriately.

## Build the CROWler docker images

To build the CROWler docker images, run the following command:

```bash
./docker_build
```

This command will build the CROWler docker images and tag them with the `crowler` prefix.

The following images will be built:

- `crowler/crowler-engine`: The base image for the CROWler docker images.
- `crowler/crowler-api`: The CROWler API image.
- `crowler/crowler-vdi`: The CROWler Virtual Desktop Image.

Plus a set of "support" images, that are used to build the main images.

## Build the CROWler docker images and run them

To build the CROWler docker images and run them, run the following command:

```bash
./docker_build up
```

This command will build the CROWler docker images and tag them with the `crowler` prefix, and then run them all into 3 separate containers.
