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
`http://localhost:8080/v1/search?q=<your query>` with a RESTFUL client.

If you have built The CROWler from source, you can run the API by running the
API with the following command:

```bash
./bin/api
```

If you used docker compose to install The CROWler, then the API is already
up and running.

The API offers a set of end points to search for data in the crowler and, if
you enabled the console feature in your config.yaml, it will also offer a set
of end points to manage your sources (aka, add/remove etc sources).

The end-points added so far are:

- [GET] `/v1/search?q=<your query>`: This end-point will search the database
  for the query you provide and return the results in JSON format.
- [GET] `/v1/netinfo?q=<your query>`: This end-point will search the database
  for the query you provide and return the results in JSON format. The results
  will include the network information of the site.
- [GET] `/v1/httpinfo?q=<your query>`: This end-point will search the database
  for the query you provide and return the results in JSON format. The results
  will include the HTTP information of the site, detected technologies and SSL
  Info.
- [GET] `/v1/screenshot?q=<your query>`: This end-point will search the
  database for the query you provide and return the results in JSON format. The
  results will include the screenshot of the site.

There are equivalent end-points in [POST] for all the above end-points.
Those accept a JSON document with more options than the GET end-points.

The q parameter supports dorking operators. For example, you can search for
`title:admin` to search for sites with the word "admin" in the title.
And they also support logical operators. For example, you can search for
`title:admin||administrator` to search for sites with the word "admin" OR
the word "administrator" in the title.
