# TheCROWler Sources

TheCROWler use the concept of "sources" to define a website "entry-point" from where to
start the crawling, scrapping and interaction process.

A source is a combination of:

- a URL
- a crawling scope
- a flagset
- a configuration file (which is expressed in YAML format and has a well defined schema).

The configuration file is used to define the rulesets to be used for the source and the interactions to be performed.

The flagset is used to define the flags to be used for the source.

The URL is the entry-point of the source.

A source can also be "enabled" or "disabled". If a source is "enabled", then it will be used by TheCROWler. If a source is "disabled", then it will be ignored.

The crawling scope is used to define the scope of the crawling. The crawling scope has 4 possible values:

- `page`: The crawling will be limited to the current page only (aka just the source entry-point).
- `FQDN`: The crawling will be limited to the current FQDN only (aka all the pages of the current FQDN, which includes the hostname, for example "www.example.com").
- `domain`: The crawling will be limited to the current domain only (aka all the pages of the current domain, which includes ALL found hostnames within the domain, for example "example.com").
- `l1 domain`: The crawling will be limited to the current l1 domain only (aka all the pages of the current l1 domain, which includes ALL found hostnames and ALL found subdomains within the l1 domain, for example ".com").
- `global`: The crawling will be global (aka all the pages of the current source and everything else on the entire internet that is linked from the source and then recursively crawled as well).
