# The CROWler

## What is it?

The CROWler is a web crawler that uses Selenium and Google's Chrome browser to crawl and index web pages as specified by the user.

It also provides a bunch of command line utilities to add and remove sites from the Sources list in its database and an API to query the database.

## How to use it?

### Prerequisites

The CROWler is designed to be micro-services based, so you'll need to install the following:

- [Docker](https://docs.docker.com/install/)
- [Docker Compose](https://docs.docker.com/compose/install/)
- [PostgreSQL Container](https://hub.docker.com/_/postgres)
- [Selenium Chrome Container](https://hub.docker.com/r/selenium/standalone-chrome)

### Build from source

To check which buildable targets are available, run the following command:

```bash
go build -v ./...
```

This will list all the buildable targets. To build a target, run the following command:

```bash
TheCrow/cmd/removeSite
TheCrow/cmd/addSite
TheCrow/services/api
TheCrow
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

## DB Maintenance

The CROWler default configuration uses PostgreSQL as its database. The database is stored in a Docker volume and is persistent.

The DB should need no maintenance, The CROWler will take care of that. Any time there is no crawling activity and it's passed 1 hours from the previous maintenance activity, The CROWler will clean up the database and optimize the indexes.
