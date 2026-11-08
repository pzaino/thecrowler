# Adding a new Source to the list of entities to crawl

To add a new entity to the list of entities, the CROWler requires such an entity to be presented with a RESTFul request to the following end-point:

For email-specific create and update payloads, connector URL schemes, authentication references, listener modes, status fields, validation failures, and defaults, see [Email source API payloads](email-sources.md).

```http
http(s)://<crowler-api-ip>:8080/v1/source/add
```

The GET request should be in the following format:

```http
GET /v1/source/add?url=https://example.com&category_id=1&usr_id=1&status=string&restricted=1&disabled=false&flags=0&config={"format_version":"1.0.0","source_name":"Example","custom":{"crawler":{"browsing_mode":"recursive","max_depth":2,"max_links":15}}}
```

Given that the above may be a bit hard to handle in a GET request, the CROWler also accepts POST requests to the same end-point.

The POST request should be in the following format:

```json
{
  "url": "https://example.com",                     // (required) The URL to crawl (which is also the starting point, hence it's called "source")
  "category_id": "uint64",                          // (optional) This is the category_id (a reference to the categories table)
  "usr_id": "uint64",                               // (ignored when API authentication is enabled; the authenticated user owns the source)
  "status": "string",                               // (optional) This field can be used to specify an initial status for the source, normally it's not used
  "restricted": "int",                              // (optional) This field contains: (default value is "1")
                                                    //  0 - When we want to specify that the CROWler should only crawl the specific source and no other related and found links
                                                    //  1 - When we want to specify that the CROWler should crawl the source and all the discovered sub-links (aka http://example.com/* for a source that is http://example.com)
                                                    //  2 - When we want to specify that the CROWler should crawl the source and all the discovered links within the source domain (aka http://*.example.com/* for a source that is http://example.com)
                                                    //  3 - When we want to specify that the CROWler should crawl every possible link discovered for the provided source within the TLD domain (for example *.com/* for a source that is http://example.com)
                                                    //  4 - When we want to specify that the CROWler should crawl every possible link discovered without any boundaries
  "disabled": "bool"                                // (optional) true is we want to add the source as disabled (so do nothing about it) or false if the CROWler should consider it for crawling
  "flags": "uint32"                                 // (optional) specific flags that can be used by plugins to enable/disable things (user-defined)
  "config": {                                       // (optional) This is the configuration
    "format_version": "1.0.0",                      //            This is the version of the configuration format (1.0.0 is currently the only one supported)
    "source_name": "Example",                       //            This is a general label, for example "https://example.com" or just "Example"
    "custom": {                                     //            This object is where the custom crawling is specified
      "crawler": {                                  //
        "browsing_mode": "recursive",               //            Browsing mode, so far recursive seems to work best for most sources
        "max_depth": 2,                             //            Maximum depth of crawling for this source
        "max_links": 15                             //            Maximum number of total links to collect and crawl for this source
                                                    // ... more options, check the "crawler" documentation for more details
      }
    }
  }
}
```

## Ownership and authentication

When `api.auth.enabled` is true, source mutations require a valid bearer token
and a local `Users.user_id` mapping:

- The source owner is always the authenticated user's numeric `Users.user_id`.
  A body or query parameter named `usr_id` is ignored.
- External token subjects that cannot be mapped to a numeric local user ID are
  rejected. CROWler does not infer an identity mapping.
- Disabled or deleted users are rejected even when the token itself is valid.
- Adding a URL that already belongs to another user is rejected. Adding an
  existing URL owned by the caller (or by an administrator) updates non-owner
  fields but never transfers ownership.
- `/v1/source/update`, `/v1/source/remove`, and `/v1/source/vacuum` require the
  caller to be the source owner or hold the `admin`/`superadmin` role. A zero or
  unknown `Sources.usr_id` fails closed for ordinary users.
- Source ownership cannot be changed through add or update. A future explicit
  administrative ownership-transfer workflow is required for reassignment.

When API authentication is disabled, the legacy unauthenticated console
workflow is preserved and a newly created source may still carry the
request-supplied `usr_id`. That value is **not** considered an authenticated
principal for agent-originated scheduling. An agent scheduling principal is
only produced from an authenticated user; historical or caller-supplied
`usr_id` values therefore do not automatically become eligible for agent
mutations. Deployments that enable agents with mutation capability must enable
API authentication and populate source ownership accordingly.

Additional status codes for authenticated source mutations:

- **401** — missing, invalid, disabled, deleted or externally-unmapped identity.
- **403** — the authenticated user is not the source owner and lacks an
  administrative role.
- **503** — the authoritative identity or ownership lookup could not be
  completed.

Upon success (or failure) insertion the CROWler will return a status JSON document:

```json
{
  "message": "website inserted successfully"
}
```

(or equivalent for the failed case)

The returned JSON document is returned also with an HTTP Status:

**201** - HTTP Created Successfully (when insertion completes correctly)

**400** - HTTP Bad Request in case of problems with the request

**500** - HTTP Internal Server error in case of issues processing the request
