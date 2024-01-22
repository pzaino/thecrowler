# The CROWler

<img align="right" width="320" height="280" src="/images/TheCROWler_v1JPG.jpg">

![Go build: ](https://github.com/pzaino/TheCROWler/actions/workflows/go.yml/badge.svg)
![CodeQL: ](https://github.com/pzaino/TheCROWler/actions/workflows/github-code-scanning/codeql/badge.svg)
![Scorecard supply-chain security: ](https://github.com/pzaino/TheCROWler/actions/workflows/scorecard.yml/badge.svg)
<!-- ![Docker build: ]() -->
![Go Report Card: ](https://goreportcard.com/badge/github.com/pzaino/TheCROWler)
![License: ](https://img.shields.io/github/license/pzaino/TheCROWler)

## What is it?

The CROWler is a specialized web crawler developed to efficiently navigate and index web pages. This tool leverages the robust capabilities of Selenium and Google Chrome (to covertly crawl a site), offering a reliable and precise crawling experience. It is designed with user customization in mind, allowing users to specify the scope and targets of their crawling tasks.

To enhance its functionality, CROWler includes a suite of command-line utilities. These utilities facilitate seamless management of the crawler's database, enabling users to effortlessly add or remove websites from the Sources list. Additionally, the system is equipped with an API, providing a streamlined interface for database queries. This feature ensures easy integration and access to indexed data for various applications.

## How to use it?

### Prerequisites

The CROWler is designed to be micro-services based, so you'll need to install the following:

- [Docker](https://docs.docker.com/install/)
- [Docker Compose](https://docs.docker.com/compose/install/)
- [PostgreSQL Container](https://hub.docker.com/_/postgres)
- [Selenium Chrome Container](https://hub.docker.com/r/selenium/standalone-chrome)

### Build from source

To build the CROWler from source, you'll need to install the following:

- [Go](https://golang.org/doc/install)

Then you'll need to clone the repository and build the targets you need.

To build everything at once run the following command:

```bash
./autobuild.sh
```

To build individual targets:

First, check which targets can be built and are available, run the following command:

```bash
go build -v ./...
```

This will list all the targets that can be built and are available. To build a target, run the following command:

```bash
TheCrowler/cmd/removeSite
TheCrowler/cmd/addSite
TheCrowler/services/api
TheCrowler
```

Build them as you need them.

### Installation

1. Clone the repository
2. Run `docker-compose up` in the root directory of the repository

### Usage

#### Crawling

To crawl a site, you'll need to add it to the Sources list in the database. You can do this by running the following command:

```bash
./addSite --url <url> --depth <depth>
```

This will add the site to the Sources list and the crawler will start crawling it. The crawler will crawl the site to the specified depth and then stop.

For the actual crawling to take place ensure you have the Selenium Chrome container running, the database container running and you have created a config.yaml configuration to allow The CROWler to access both.

Finally, make sure that The CROWler is running.

#### Removing a site

To remove a site from the Sources list, run the following command:

```bash
./removeSite --url <url>
```

Where URL is the URL of the site you want to remove and it's listed in the Sources list.

#### API

The CROWler provides an API to query the database. The API is a REST API and is documented using Swagger. To access the Swagger documentation, go to `http://localhost:8080/search?q=<your query>` with a RESTFUL client.

To startup the API, run the following command:

```bash
./api
```

#### Configuration

To configure both the API and the Crawler, you'll need to create a config.yaml file in the root directory of the repository. The config.yaml file should look like this:

```yaml
database:
  host: <database host>
  port: <database port>
  user: <database user>
  password: <database password>
  dbname: <database name>
api:
  port: <api port>
crawler:
  workers: <max workers number>
selenium:
  type: chrome
  port: 4444
  headless: true
  host: localhost
```

Keep in mind that every section of the config.yaml file tells the CROWler how to configure a specific component (which is required to be installed in a container).

The sections are:

- The database section configures the database
- The crawler section configures the number of workers the CROWler will use
- The selenium section configures the Selenium Chrome container
- The API section configures the API

## Production

If you want to use the CROWler in production, you'll need to build the following targets:

- The CROWler
- The API
- The addSite command
- The removeSite command

You'll also need to create a config.yaml file and configure it as described in the Configuration section.

Finally, you'll need to create a Dockerfile to build a Docker image for the CROWler. The Dockerfile should look like this:

```Dockerfile
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY config.yaml .
COPY TheCrowler .
COPY cmd/addSite .
COPY cmd/removeSite .
COPY services/api .
CMD ["./TheCrowler"]
```

Then, build the Docker image using the following command:

```bash
docker build -t <image name> .
```

Finally, run the Docker image using the following command:

```bash
docker run -d -p 8080:8080 <image name>
```

For better security I strongly recommend to deploy the API in a separate container than the CROWler one. Also, there is no need to expose the CROWler container to the outside world, it will need internet access thought.

## DB Maintenance

The CROWler default configuration uses PostgreSQL as its database. The database is stored in a Docker volume and is persistent.

The DB should need no maintenance, The CROWler will take care of that. Any time there is no crawling activity and it's passed 1 hours from the previous maintenance activity, The CROWler will clean up the database and optimize the indexes.
