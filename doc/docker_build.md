# Installation of the CROWler using Docker

The manual build of the CROWler can be a complex activity, as it requires the
installation of several dependencies and the configuration of the environment.
To simplify this process, the `docker-build.sh` script was created. This script
is responsible for patching dependencies, building the CROWler docker images,
and running them in separate containers.

docker-build takes care of building the CROWler docker images using the
config.yaml file and determine the platform (x86_64 or ARM64) you want to use
and configuring the build appropriately.

## Before you start

- **Step A** Make sure you have `git`, `make`, `docker` and `docker-compose`
  are installed on your machine, these are ALL required to build the images.

  As for which releases of the above:
  - Docker >= 26.0
  - Docker-compose >= 2.27.0
  - Git >= 2.20.0
  - GNU Make >= 4.2.1

  As for the minimal requirements for the machine you are building the CROWler:
  - 4GB of RAM
  - 2 CPU cores
  - 20GB of disk space

  **Note**: The above are the minimal requirements, you can run the CROWler on
  a machine with less resources, but you might experience performance issues.

- **Step B** There are a bunch of ENV variables you can set to
  customize the CROWler deployment. These ENV vars allow you to set up your
  username and password for the database, the database name, the port the API
  will listen on, etc.

  To see the full list of ENV vars you can set, see [here](doc/env_vars.md).

  There are 4 ENV vars **you must set**, otherwise the CROWler won't build or
  work:

  - `DOCKER_CROWLER_DB_PASSWORD`, this is the password for the CROWler user in
    the database (non-admin level).
  - `DOCKER_POSTGRES_PASSWORD`, this is the password for the postgres user in
    the database (admin level).
  - `DOCKER_DB_HOST`, this is the hostname, IP or FQDN of the Postgres database.
    You normally set this one with the IP of the host where you're running the
    Postgres container.
  - `DOCKER_SELENIUM_IMAGE`, this should always be set to:
  `"selenium/standalone-chromium:4.27.0-<date-you-build-vdi-image>"`. For
  example:
  `"DOCKER_SELENIUM_IMAGE="selenium/standalone-chromium:4.27.0-20241223"`.
  The default value is `"selenium/standalone-chromium:4.27.0-(today)"`.
  Where (today) is the date (in yyyyMMdd format) of the day you generated the
  docker-compose.yml file.

  If you are building the CROWler on an CPU architecture that is not `x86_64`,
   then you **must** add the following ENV var:

  - `DOCKER_DEFAULT_PLATFORM`, this is the architecture of the CPU you are
   building the CROWler on. For example `"linux/arm64"` for ARM64 and
   `"linux/amd64"` for x86_64.

  **Note**: If you rebuild the CROWler docker images often, you might want to
  set these ENV vars in your shell file called config.sh, and then source it
  before you execute the [Build the CROWler docker images](#build-the-crowler-docker-images)
  procedure. This way you don't have to set them every time you rebuild the
  CROWler docker images. Why config.sh? Because it's a file name already ignored
  by git (in the .gitignore file), so you can safely put your ENV vars in there
  without worrying about accidentally committing them to the repository.

Once you've completed the steps above, follow the procedure below:

## Build the CROWler docker images

- **Step 1**: Before you build, make sure the docker daemon is running on your machine.

- **Step 2**: If you haven't yet, clone TheCrowler repository on your build
 machine and `cd` into the root directory of the repository

- **Step 3**: If you haven't yet, configure the ENV variables as described
 above.

- **Step 4**: Run the following command to generate your specific
 Docker-compose file:

  ```bash
  ./scripts/generate-docker-compose.sh 1 1
  ```

  This script will generate a docker-compose file that will be used to build
  the CROWler docker images. The two 1 1 arguments are used to specify that
  you want 1 single engine and 1 single VDI.
  If you need to scale further use the appropriate number of engines and VDIs
  (for example, 2 2 etc).

  **Please Note**: If you are planning to scale up using tools like Kubernetes
  then you won't need to generate your docker-compose file using multiple VDIs
  and engines. You can simply generate a single engine and VDI and then scale
  up using Kubernetes.

  For more options for this script please check the source code of the script.

- **Step 5**: Generate your **config.yaml** file based on your desired
  configuration. You can simply start from renaming config.default to
  `config.yaml` and then modify it according to your needs. If you are building
  multiple VDI's you will need to add the appropriate number of VDI's to the
  `config.yaml` and their reachable ports (you can check your docker-compose for
  the ports). If you need more info on the available options and how to
  configure them please click [here](./config_yaml.md) for more details.

  **Please Note**: If you decided to spin up multiple VDIs, you will need to
  modify the `config.yaml` file to reflect the number of VDIs you want to
  spin up. You will need to add an entry for each of them with the port
  number that you want to use for each VDI (check your Docker-compose file
  for the port numbers), and the container name for each VDI.

- **Step 6**: To build the CROWler docker images, run the following command:

  ```bash
  ./docker-build.sh build
  ```

  **Please note (1):** that the `docker-build` script accepts the docker-compose
  arguments, so it's a special wrapper that will ensure that the images are
  configured and generated correctly and then call docker-compose.

  **Please note (2):** On some Linux Distros (or if you do not have your user
  in the docker group), you may need to run the above command with `sudo`.

  **Please note (3):** If you are using BuildKit and it has a build container
  running using the 'docker-container' or 'cloud' engines to perform docker
  container builds, you may need to add the argument `--load` to the command
  above to load the images into the docker daemon. (thanks @mort666 for the
  tip and testing the build on that environment).

  `docker-build` will build the CROWler docker images and tag them with the
   `crowler` prefix.

  The following images will be built:

  - `crowler/crowler-engine`: The base image for the CROWler docker images.
  - `crowler/crowler-api`: The CROWler API image.
  - `crowler/crowler-vdi`: The CROWler Virtual Desktop Image.

  Plus a set of "support" images, that are used to build the main images.

## Build the CROWler docker images and run them

To build the CROWler docker images and run them at once, follow the previous
procedure (it's the same), and replace Step 6 with executing the following
command instead of the previous syntax for `docker-build`:

```bash
./docker-build.sh up --build
```

This command will build the CROWler docker images and then run them all into 3
separate containers.

## Build the CROWler docker images and run them in detached mode

To build the CROWler docker images and run them in detached mode, follow the
[Build the CROWler images](#build-the-crowler-docker-images) procedure, it's
the same, and instead of Step 6, run the following command:

```bash
./docker-build.sh up --build -d
```

This command will build the CROWler docker images and tag them with the
`crowler` prefix, and then run them all into 3 separate containers in
detached mode.

## Stop the CROWler docker containers

To stop the CROWler docker containers, run the following command:

```bash
./docker-compose down
```

This command will stop and remove the CROWler docker containers.

## Rebuild the CROWler docker images from clean

To rebuild (for example after you've downloaded a new version of the CROWler
code) the CROWler docker images from scratch, run the following command:

```bash
./docker_rebuild.sh build --no-cache
```

Please note: `docker_rebuild` instead of `docker-build` is used to ensure that
the images are built from scratch.
