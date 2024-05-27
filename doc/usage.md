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
./addSource -url <url> -restrict <restrict>
```

This will add the site to the Sources list and the crawler will start crawling
it. The crawler will crawl the site using the specified restriction level and
then stop.

The restriction level is a number between 0 and 3. The higher the number, the
more the crawler will crawl. The restriction level is used to limit the depth
of the crawling. For example, set the restriction level to:

* 0 = for "fully restricted" crawling (just this URL, nothing else)
* 1 = for l3 domain restricted (everything within this URL l3 domain)
* 2 = for l2 domain restricted
* 3 = for l1 domain restricted
* 4 = for no restrictions, basically crawl everything on the sites and all
  sites linked to it.

For more info on the addSOurce syntax, type:

```bash
./addSource --help
```

For the actual crawling to take place ensure you have the CROWler VDI running,
the CROWler db container running and you have created a config.yaml
configuration to allow The CROWler to access both.

Finally, make sure that The CROWler engine is running.

### Bulk inserting sites

You can also bulk insert sites by providing a CSV file with a list of URLs.
The file should have the following format:

```csv
URL, Category ID, UsrID, Restricted, Flags, ConfigFileName
```

Where:

* URL: The URL of the site.
* Category ID: The ID of the category the site belongs to. (optional)
* UsrID: The ID of the user that added the site. (optional)
* Restricted: The restriction level of the site. (optional)
* Flags: The flags of the site. (optional)
* ConfigFileName: The name of the Source configuration file to use
  for the site. (optional)

## Removing a site

To remove a site from the Sources list, run the following command:

```bash
./removeSource -url <url>
```

Where URL is the URL of the site you want to remove and it's listed in the
Sources list.

For more info on the removeSource syntax, type:

```bash
./removeSource --help
```

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

* [GET] `/v1/search?q=<your query>`: This end-point will search the database
  for the query you provide and return the results in JSON format.
* [GET] `/v1/netinfo?q=<your query>`: This end-point will search the database
  for the query you provide and return the results in JSON format. The results
  will include the network information of the site.
* [GET] `/v1/httpinfo?q=<your query>`: This end-point will search the database
  for the query you provide and return the results in JSON format. The results
  will include the HTTP information of the site, detected technologies and SSL
  Info.
* [GET] `/v1/screenshot?q=<your query>`: This end-point will search the
  database for the query you provide and return the results in JSON format. The
  results will include the screenshot of the site.
* [GET] `/v1/webobject?q=<your query>`: This end-point will search the database
  for the query you provide and return the results in JSON format. The results
  will include the web objects of the site.
* [GET] `/v1/correlated_sites?q=<your query>`: This end-point will search the
  database for the query you provide and return the results in JSON format. The
  results will include all the correlated sites of the specified terms.
  Basically if you want to know how many sites are related to a specific term,
  web site, company, etc, you can use this end-point.

There are equivalent end-points in [POST] for all the above end-points.
Those accept a JSON document with more options than the GET end-points.

The q parameter supports dorking operators. For example, you can search for
`title:admin` to search for sites with the word "admin" in the title.
And they also support logical operators. For example, you can search for
`title:admin||administrator` to search for sites with the word "admin" OR
the word "administrator" in the title.

### Index administration via API

If you have enabled the console feature in your config.yaml, you can also
manage your sources via the API. The end-points added so far are:

* [GET] `/v1/addsource`: This end-point will add a new source to the database.
  The source should be provided in JSON format.
* [GET] `/v1/removesource`: This end-point will remove a source from the
  database. The source should be provided in JSON format.

There are equivalent end-points in [POST] for all the above end-points.

You can also check what's going on with the crawler by checking the logs of the
CROWler engine and/or use the following console end-points:

* [GET] `/v1/get_all_source_status`: This end-point will return the status of the
  of all the crawling activities going on.
* [GET] `/v1/get_source_status`: This end-point will return the status of the
  crawling activity of a specific source.
