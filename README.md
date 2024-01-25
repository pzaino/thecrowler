# The CROWler

<img align="right" width="320" height="280"
 src="/images/TheCROWler_v1JPG.jpg" alt="TheCROWLer Logo">

![Go build: ](https://github.com/pzaino/TheCROWler/actions/workflows/go.yml/badge.svg)
![CodeQL: ](https://github.com/pzaino/TheCROWler/actions/workflows/github-code-scanning/codeql/badge.svg)
![Scorecard supply-chain security: ](https://github.com/pzaino/TheCROWler/actions/workflows/scorecard.yml/badge.svg)
<!-- ![Docker build: ]() -->
![Go Report Card: ](https://goreportcard.com/badge/github.com/pzaino/TheCROWler)
![License: ](https://img.shields.io/github/license/pzaino/TheCROWler)

## What is it?

The CROWler is a specialized web crawler developed to efficiently navigate and
index web pages. This tool leverages the robust capabilities of Selenium and
Google Chrome (to covertly crawl a site), offering a reliable and precise
crawling experience. It is designed with user customization in mind, allowing
users to specify the scope and targets of their crawling tasks.

To enhance its functionality, CROWler includes a suite of command-line
utilities. These utilities facilitate seamless management of the crawler's
database, enabling users to effortlessly add or remove websites from the
Sources list. Additionally, the system is equipped with an API, providing a
streamlined interface for database queries. This feature ensures easy integration
and access to indexed data for various applications.

## How to use it?

### Prerequisites

The CROWler is designed to be micro-services based, so you'll need to install the
following:

- [Docker](https://docs.docker.com/install/)
- [Docker Compose](https://docs.docker.com/compose/install/)

For a docker compose based installation, that's all you need.

#### If you're planning to install it manually

If you're planning to install the CROWler manually, you'll need to install the
following Docker containers:

- [PostgreSQL Container](https://hub.docker.com/_/postgres)
- [Selenium Chrome Container](https://hub.docker.com/r/selenium/standalone-chrome)

### Build from source

If you'll use the docker compose then everything will build automatically,
all you'll need to do is follow the instructions in the Installation section.

If, instead you want to build locally on your machine, then follow the instructions
in this section.

To build the CROWler from source, you'll need to install the following:

- [Go](https://golang.org/doc/install)

Then you'll need to clone the repository and build the targets you need.

To build everything at once run the following command:

```bash
./autobuild.sh
```

To build individual targets:

First, check which targets can be built and are available, run the following
command:

```bash
go build -v ./...
```

This will list all the targets that can be built and are available. To build a target,
run the following command:

```bash
TheCrowler/cmd/removeSite
TheCrowler/cmd/addSite
TheCrowler/services/api
TheCrowler
```

Build them as you need them, or run the `autobuild.sh` script to build
them all.

Optionally you can build the Docker image, to do so run the following command:

```bash
docker build -t <image name> .
```

### Installation

1. Clone the repository
2. Create your config.yaml file (see [here](doc/config_yaml.md) for more info)
3. Run `./docker-build.sh` to build the with Docker compose and the right
platform (see [here](doc/docker_build.md) for more info)

### Usage

For instruction on how to use it see [here](doc/usage.md).

#### Configuration

To configure both the API and the Crawler, you'll need to create a config.yaml
file in the root directory of the repository.

See [here](doc/config_yaml.md) for more info.

## Production

If you want to use the CROWler in production, I recommend to use the docker
compose installation. It's the easiest way to install it and it's the most
secure one.

For better security I strongly recommend to deploy the API in a separate container
than the CROWler one. Also, there is no need to expose the CROWler container to the
outside world, it will need internet access thought.

## DB Maintenance

The CROWler default configuration uses PostgreSQL as its database. The database is
stored in a Docker volume and is persistent.

The DB should need no maintenance, The CROWler will take care of that. Any time there
is no crawling activity and it's passed 1 hours from the previous maintenance activity,
The CROWler will clean up the database and optimize the indexes.
