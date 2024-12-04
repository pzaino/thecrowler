# The CROWler API

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

You can specify the max number of items to return by using the `limit` parameter.
You can browse on the results by using the `offset` parameter.

For example:

`/v1/webobject?q=example.com&offset=1`

This will return the second page of the results. The default limit is 10.

## Index administration via API

If you have enabled the console feature in your config.yaml, you can also
manage your sources via the API. The end-points added so far are:

* [GET] `/v1/addsource`: This end-point will add a new source to the database.
  The source should be provided in JSON format.
  [addsource](./api/addsource.md) detailed documentation.
* [GET] `/v1/removesource`: This end-point will remove a source from the
  database. The source should be provided in JSON format.

There are equivalent end-points in [POST] for all the above end-points.

You can also check what's going on with the crawler by checking the logs of the
CROWler engine and/or use the following console end-points:

* [GET] `/v1/get_all_source_status`: This end-point will return the status of the
  of all the crawling activities going on.
* [GET] `/v1/get_source_status`: This end-point will return the status of the
  crawling activity of a specific source.
