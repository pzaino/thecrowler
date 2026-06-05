# The CROWler API

The API offers a set of end points to search for data in the crowler and, if
you enabled the console feature in your config.yaml, it will also offer a set
of end points to manage your sources (aka, add/remove etc sources).

The end-points added so far are:

* [GET] `/v1/search/general?q=<your query>`: This end-point will search the database
  for the query you provide and return the results in JSON format.
* [GET] `/v1/search/netinfo?q=<your query>`: This end-point will search the database
  for the query you provide and return the results in JSON format. The results
  will include the network information of the site.
* [GET] `/v1/search/httpinfo?q=<your query>`: This end-point will search the database
  for the query you provide and return the results in JSON format. The results
  will include the HTTP information of the site, detected technologies and SSL
  Info.
* [GET] `/v1/search/screenshot?q=<your query>`: This end-point will search the
  database for the query you provide and return the results in JSON format. The
  results will include the screenshot of the site.
* [GET] `/v1/search/webobject?q=<your query>`: This end-point will search the database
  for the query you provide and return the results in JSON format. The results
  will include the web objects of the site.
* [GET] `/v1/search/correlated_sites?q=<your query>`: This end-point will search the
  database for the query you provide and return the results in JSON format. The
  results will include all the correlated sites of the specified terms.
  Basically if you want to know how many sites are related to a specific term,
  web site, company, etc, you can use this end-point.
* [GET] `/v1/search/collected_data?q=<your query>`: This end-point will search the
  database for the query you provide and return the results in JSON format. The
  results will include all the collected data of the specified terms.
  Basically if you want to know how many data are related to a specific term,
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

`/v1/search/webobject?q=example.com&offset=1`

This will return the second page of the results. The default limit is 10.

## Index administration via API

If you have enabled the console feature in your config.yaml, you can also
manage your sources via the API. The end-points added so far are:

* [GET] `/v1/source/add`: This end-point will add a new source to the database.
  The source should be provided in JSON format.
  [addsource](./api/addsource.md) detailed documentation.
* [GET] `/v1/source/remove`: This end-point will remove a source from the
  database (and all the related crawled data).
* [GET] `/v1/source/update`: This end-point will update a source in the database.
* [GET] `/v1/source/vacuum`: This end-point will vacuum the source from all data
  crawled and collected so far (note: it does NOT remove the source, it's owners, categories etc., only crawled data).

### Information seed administration

The canonical namespace for information seed console end-points is
`/v1/information_seed/*` (underscore). The older hyphenated
`/v1/information-seed/list` route is kept only as a backward-compatible alias
for `/v1/information_seed/list`.

All information seed responses include the seed identity and state fields:
`information_seed_id`, `status`, `has_error`, `last_error`, `last_error_at`,
`attempts`, `disabled`, `priority`, `engine`, timestamps, `config`, and
`discovered_source_count` where the source relationship count is applicable.

* [POST] `/v1/information_seed/add`: Adds a new information seed. The JSON body
  accepts `information_seed` (required), `category_id`, `usr_id` (or `user_id`),
  `status` (defaults to `new`), `priority`, `engine`, `disabled`, and `config`.
  The response returns the created seed under `item`.
* [GET] `/v1/information_seed/status?information_seed_id=<id>`: Returns the
  current status for a single information seed. The `id` or `q` query parameter
  may also be used for the seed ID.
* [GET] `/v1/information_seed/list`: Lists all information seeds and includes
  `discovered_source_count`, the number of active `SourceInformationSeedIndex`
  relationships currently linking discovered sources to each seed. The count is
  calculated by the database in the list query.
  Candidate decision audit rows are intentionally not exposed through the public
  API in this release; they are available to internal database callers through
  seed-scoped pagination helpers.
* [POST] `/v1/information_seed/retry`: Queues an information seed for another
  discovery attempt by setting its status to `pending` and clearing the current
  error state. The JSON body is `{"information_seed_id": <id>}`.
* [POST] `/v1/information_seed/disable`: Disables an information seed so it is
  no longer eligible for discovery claiming. The JSON body is
  `{"information_seed_id": <id>}`.

There are equivalent end-points in [POST] for the source administration
end-points above unless a method is explicitly shown.

You can also check what's going on with the crawler by checking the logs of the
CROWler engine and/or use the following console end-points:

* [GET] `/v1/source/statuses`: This end-point will return the status of the
  of all the crawling activities going on.
* [GET] `/v1/source/status`: This end-point will return the status of the
  crawling activity of a specific source.

To manage Owners and Categories, you can use the following end-points:

* [GET] `/v1/owner/add`: This end-point will add a new owner to the database.
  The owner should be provided in JSON format.
* [GET] `/v1/owner/remove`: This end-point will remove an owner from the
  database.
* [GET] `/v1/owner/update`: This end-point will update an owner in the database.
* [GET] `/v1/owner/list`: This end-point will list all the owners in the database.

There are equivalent end-points in [POST] for all the above end-points.

* [GET] `/v1/category/add`: This end-point will add a new category to the database.
  The category should be provided in JSON format.
* [GET] `/v1/category/remove`: This end-point will remove a category from the
  database.
* [GET] `/v1/category/update`: This end-point will update a category in the database.
* [GET] `/v1/category/list`: This end-point will list all the categories in the database.
