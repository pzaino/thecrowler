# Usage

If you have installed The CROWler using the docker compose file, then you can
just use the API to control it. If you have built the CROWler from source, then
you can use the API or the command line tools.

## Crawling

TheCROWler uses the concept of "sources" to define a website "entry-point" from
where to start the crawling, scrapping and interaction process. More information
on sources can be found [here](./sources.md).

To crawl a site, you'll need to add it to the Sources list in the database. You
can do this by running the following command:

```bash
./addSource --url <url> --depth <depth>
```

This will add the site to the Sources list and the crawler will start crawling
it. The crawler will crawl the site to the specified depth and then stop.

For the actual crawling to take place ensure you have the Selenium Chrome
container running, the database container running and you have created a
config.yaml configuration to allow The CROWler to access both.

Finally, make sure that The CROWler is running.

## Removing a site

To remove a site from the Sources list, run the following command:

```bash
./removeSource --url <url>
```

Where URL is the URL of the site you want to remove and it's listed in the
Sources list.

## API

The CROWler provides an API to query the database. The API is a REST API and is
documented using Swagger. To access the Swagger documentation, go to
`http://localhost:8080/search?q=<your query>` with a RESTFUL client.

If you have built The CROWler from source, you can run the API by running the
API with the following command:

```bash
./bin/api
```

If you used docker compose to install The CROWler, then the API is already
up and running.
