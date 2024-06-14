# The CROWler config.yaml reference

## Properties

- **`remote`** *(object)*: (optional) This is the configuration section to tell the CROWler its actual configuration has to be fetched remotely from a distribution server. If you use this section, then do not populate the other configuration sections as they will be ignored. The CROWler will fetch its configuration from the remote server and use it to start the engine.
  - **`host`** *(string)*: This is the host that the CROWler will use to fetch its configuration.
  - **`path`** *(string)*: This is the path that the CROWler will use to fetch its configuration.
  - **`port`** *(integer)*: This is the port that the CROWler will use to fetch its configuration.
  - **`region`** *(string)*: This is the region that the CROWler will use to fetch its configuration. For example in case the distribution server is on an AWS S3 bucket, you can specify the region here.
  - **`token`** *(string)*: This is the token that the CROWler will use to connect to the distribution server to fetch its configuration.
  - **`secret`** *(string)*: This is the secret that the CROWler will use to connect to the distribution server to fetch its configuration.
  - **`timeout`** *(integer)*: This is the timeout for the CROWler to fetch its configuration.
  - **`type`** *(string)*: This is the type of the distribution server that the CROWler will use to fetch its configuration. For example, s3 or http.
  - **`sslmode`** *(string)*: This is the sslmode that the CROWler will use to connect to the distribution server to fetch its configuration.
- **`database`** *(object)*: This is the configuration for the database that the CROWler will use to store data.
  - **`type`** *(string)*
  - **`host`** *(string)*
  - **`port`** *(integer)*
  - **`user`** *(string)*
  - **`password`** *(string)*
  - **`dbname`** *(string)*
  - **`retry_time`** *(integer)*
  - **`ping_time`** *(integer)*
  - **`sslmode`** *(string)*
  - **`optimize_for`** *(string)*: This option allows the user to optimize the database for a specific use case. For example, if the user is doing more write operations than query, then use the value "write". If the user is doing more query operations than write, then use the value "query". If unsure leave it empty.
- **`crawler`** *(object)*
  - **`workers`** *(integer)*: This is the number of workers that the CROWler will use to crawl websites. Minimum number is 3 per each Source if you have network discovery enabled or 1 per each source if you are doing crawling only. Increase the number of workers to scale up the CROWler engine vertically.
  - **`interval`** *(string)*: This is the interval at which the CROWler will crawl websites. It is the interval at which the CROWler will crawl websites, values are in seconds, e.g. '3' means 3 seconds. For the interval you can also use the CROWler exprterpreter to generate delay values at runtime, e.g., 'random(1, 3)' or 'random(random(1,3), random(5,8))'.
  - **`timeout`** *(integer)*: This is the timeout for the CROWler. It is the maximum amount of time that the CROWler will wait for a website to respond.
  - **`maintenance`** *(integer)*: This is the maintenance interval for the CROWler. It is the interval at which the CROWler will perform automatic maintenance tasks.
  - **`source_screenshot`** *(boolean)*: This is a flag that tells the CROWler to take a screenshot of the source website. This is useful for debugging purposes.
  - **`full_site_screenshot`** *(boolean)*: This is a flag that tells the CROWler to take a screenshot of the full website. This is useful for debugging purposes.
  - **`max_depth`** *(integer)*: This is the maximum depth that the CROWler will crawl websites.
  - **`max_sources`** *(integer)*: This is the maximum number of sources that a single instance of the CROWler's engine will fetch atomically to enqueue and crawl.
  - **`delay`** *(string)*: This is the delay between requests that the CROWler will use to crawl websites. It is the delay between requests that the CROWler will use to crawl websites. For delay you can also use the CROWler exprterpreter to generate delay values at runtime, e.g., 'random(1, 3)' or 'random(random(1,3), random(5,8))'.
  - **`browsing_mode`** *(string)*: This is the browsing mode that the CROWler will use to crawl websites. For example, recursive, human, or fuzzing.
  - **`max_retries`** *(integer)*: This is the maximum number of times that the CROWler will retry a request to a website. If the CROWler is unable to fetch a website after this number of retries, it will move on to the next website.
  - **`max_requests`** *(integer)*: This is the maximum number of requests that the CROWler will send to a website. If the CROWler sends this number of requests to a website and is unable to fetch the website, it will move on to the next website.
  - **`collect_html`** *(boolean)*: This is a flag that tells the CROWler to collect the HTML of a website. This is useful for debugging purposes.
  - **`collect_images`** *(boolean)*: This is a flag that tells the CROWler to collect images from a website. This is useful for debugging purposes.
  - **`collect_files`** *(boolean)*: This is a flag that tells the CROWler to collect files from a website. This is useful for debugging purposes.
  - **`collect_content`** *(boolean)*: This is a flag that tells the CROWler to collect the text content of a website. This is useful for AI datasets creation and knowledge bases.
  - **`collect_keywords`** *(boolean)*: This is a flag that tells the CROWler to collect the keywords of a website. This is useful for AI datasets creation and knowledge bases.
  - **`collect_metatags`** *(boolean)*: This is a flag that tells the CROWler to collect the metatags of a website. This is useful for AI datasets creation and knowledge bases.
- **`api`** *(object)*: This is the configuration for the API (has no effect on the engine). It is the configuration for the API that the CROWler will use to communicate with the outside world.
  - **`host`** *(string)*: This is the host that the API will use to communicate with the outside world. Use 0.0.0.0 to make the API accessible from any IP address.
  - **`port`** *(integer)*: This is the port that the API will use to communicate with the outside world.
  - **`timeout`** *(integer)*: This is the timeout for the API. It is the maximum amount of time that the CROWler will wait for the API to respond.
  - **`content_search`** *(boolean)*: This is a flag that tells the CROWler to search also in the content field of a web object in the search results. This is useful for searching for every possible details of a web object, however will reduce performance quite a bit.
  - **`return_content`** *(boolean)*: This is a flag that tells the CROWler to return the web object content of a page in the search results. To improve performance, you can disable this option.
  - **`sslmode`** *(string)*: This is the sslmode switch for the API. Use 'enable' to make the API use HTTPS.
  - **`cert_file`** *(string)*: This is the certificate file for the API HTTPS protocol.
  - **`key_file`** *(string)*: This is the key file for the API HTTPS certificates.
  - **`rate_limit`** *(string)*: This is the rate limit for the API. It is the maximum number of requests that the CROWler will accept per second. You can use the ExprTerpreter language to set the rate limit.
  - **`enable_console`** *(boolean)*: This is a flag that tells the CROWler to enable the admin console via the API. In other words, you'll get more endpoints to manage the CROWler via the Search API instead of local commands.
  - **`return_404`** *(boolean)*: This is a flag that tells the CROWler to return 404 status code if a query has no results.
- **`selenium`** *(array)*
  - **Items** *(object)*: This is the configuration for the selenium driver. It is the configuration for the selenium driver that the CROWler will use to crawl websites. To scale the CROWler web crawling capabilities, you can add multiple selenium drivers in the array. Cannot contain additional properties.
    - **`name`** *(string)*: This is the name of the VDI image.
    - **`location`** *(string)*: This is the location of the VDI image.
    - **`path`** *(string)*: This is the path to the selenium driver (IF LOCAL). It is the path to the selenium driver that the CROWler will use to crawl websites.
    - **`driver_path`** *(string)*: This is the path to the selenium driver (IF REMOTE). It is the path to the selenium driver that the CROWler will use to crawl websites.
    - **`type`** *(string)*: This is the type of selenium driver that the CROWler will use to crawl websites. For example, chrome or firefox.
    - **`port`** *(integer)*: This is the port that the selenium driver will use to connect to the CROWler. It is the port that the selenium driver will use to connect to the CROWler.
    - **`host`** *(string)*: This is the host that the selenium driver will use to connect to the CROWler. It is the host that the selenium driver will use to connect to the CROWler. For example, localhost. This is also the recommended way to use the Selenium driver with the CROWler.
    - **`headless`** *(boolean)*: This is a flag that tells the selenium driver to run in headless mode. This is useful for running the selenium driver in a headless environment. It's generally NOT recommended to enable headless mode for the selenium driver.
    - **`use_service`** *(boolean)*: This is a flag that tells the CROWler to access Selenium as service.
    - **`sslmode`** *(string)*: This is the sslmode that the selenium driver will use to connect to the CROWler. It is the sslmode that the selenium driver will use to connect to the CROWler.
    - **`download_path`** *(string)*: This is the download path for the selenium driver. It is the path where the selenium driver will download files. This is useful for downloading files from websites. The CROWler will use this path to store the downloaded files.
- **`image_storage`** *(object)*: This is the configuration for the image storage. It is the configuration for the storage that the CROWler will use to store images.
  - **`host`** *(string)*
  - **`path`** *(string)*
  - **`port`** *(integer)*
  - **`region`** *(string)*
  - **`token`** *(string)*
  - **`secret`** *(string)*
  - **`timeout`** *(integer)*
  - **`type`** *(string)*
  - **`sslmode`** *(string)*
- **`file_storage`** *(object)*: This is the configuration for the file storage. File storage will be used for web object content storage.
  - **`host`** *(string)*
  - **`path`** *(string)*
  - **`port`** *(integer)*
  - **`region`** *(string)*
  - **`token`** *(string)*
  - **`secret`** *(string)*
  - **`timeout`** *(integer)*
  - **`type`** *(string)*
  - **`sslmode`** *(string)*
- **`network_info`** *(object)*: This is the configuration for the network information collection.
  - **`dns`** *(object)*
    - **`enabled`** *(boolean)*: This is a flag that tells the CROWler to use DNS techniques. This is useful for detecting the IP address of a domain.
    - **`timeout`** *(integer)*: This is the timeout for the DNS database. It is the maximum amount of time that the CROWler will wait for the DNS database to respond.
    - **`rate_limit`** *(string)*: This is the rate limit for the DNS database. It is the maximum number of requests that the CROWler will send to the DNS database per second. You can use the ExprTerpreter language to set the rate limit.
  - **`whois`** *(object)*
    - **`enabled`** *(boolean)*: This is a flag that tells the CROWler to use whois techniques. This is useful for detecting the owner of a domain.
    - **`timeout`** *(integer)*: This is the timeout for the whois database. It is the maximum amount of time that the CROWler will wait for the whois database to respond.
    - **`rate_limit`** *(string)*: This is the rate limit for the whois database. It is the maximum number of requests that the CROWler will send to the whois database per second. You can use the ExprTerpreter language to set the rate limit.
  - **`netlookup`** *(object)*
    - **`enabled`** *(boolean)*: This is a flag that tells the CROWler to use netlookup techniques. This is useful for detecting the network information of a host.
    - **`timeout`** *(integer)*: This is the timeout for the netlookup database. It is the maximum amount of time that the CROWler will wait for the netlookup database to respond.
    - **`rate_limit`** *(string)*: This is the rate limit for the netlookup database. It is the maximum number of requests that the CROWler will send to the netlookup database per second. You can use the ExprTerpreter language to set the rate limit.
  - **`geo_localization`** *(object)*
    - **`enabled`** *(boolean)*: This is a flag that tells the CROWler to use geolocation techniques. This is useful for detecting the location of a host.
    - **`path`** *(string)*: This is the path to the geolocation database. It is the path to the database that the CROWler will use to determine the location of a host.
    - **`type`** *(string)*: This is the type of geolocation database that the CROWler will use. It is the type of database that the CROWler will use to determine the location of a host. For example maxmind or ip2location.
    - **`timeout`** *(integer)*: This is the timeout for the geolocation database. It is the maximum amount of time that the CROWler will wait for the geolocation database to respond.
    - **`api_key`** *(string)*: This is the API key for the geolocation database. It is the API key that the CROWler will use to connect to the geolocation database.
    - **`sslmode`** *(string)*: This is the sslmode for the geolocation database. It is the sslmode that the CROWler will use to connect to the geolocation database.
  - **`service_scout`** *(object)*
    - **`enabled`** *(boolean)*: This is a flag that tells the CROWler to use service scanning techniques. This is useful for detecting services that are running on a host.
    - **`timeout`** *(integer)*: This is the timeout for the scan. It is the maximum amount of time that the CROWler will wait for a host to respond to a scan.
    - **`idle_scan`** *(object)*: This is the configuration for the idle scan.
      - **`host`** *(string)*: Host FQDN or IP address.
      - **`port`** *(integer)*: Port number.
    - **`ping_scan`** *(boolean)*: This is a flag that tells the CROWler to use ping scanning techniques. This is useful for detecting hosts that are alive.
    - **`connect_scan`** *(boolean)*: This is a flag that tells the CROWler to use connect scanning techniques. This is useful for detecting services that are running on a host.
    - **`syn_scan`** *(boolean)*: This is a flag that tells the CROWler to use SYN scanning techniques. This is useful for detecting services that are running on a host.
    - **`udp_scan`** *(boolean)*: This is a flag that tells the CROWler to use UDP scanning techniques. This is useful for detecting services that are running on a host.
    - **`no_dns_resolution`** *(boolean)*: This is a flag that tells the CROWler not to resolve hostnames to IP addresses. This is useful for avoiding detection by intrusion detection systems.
    - **`service_detection`** *(boolean)*: This is a flag that tells the CROWler to use service detection techniques. This is useful for detecting services that are running on a host.
    - **`service_db`** *(string)*: This is the service detection database.
    - **`os_finger_print`** *(boolean)*: This is a flag that tells the CROWler to use OS fingerprinting techniques. This is useful for detecting the operating system that is running on a host.
    - **`aggressive_scan`** *(boolean)*: This is a flag that tells the CROWler to use aggressive scanning techniques. This is useful for detecting services that are running on a host.
    - **`script_scan`** *(array)*: This is a list of nmap scripts to run. This is particularly important when a user wants to do vulnerability scanning.
      - **Items** *(string)*
    - **`excluded_hosts`** *(array)*: This is a list of hosts to exclude from the scan. The CROWler may encounter such hosts during its crawling activities, so this field makes it easy to define a list of hosts that it should always avoid scanning.
      - **Items** *(string)*
    - **`timing_template`** *(string)*: This allows the user to set the timing template for the scan. The timing template is a string that is passed to nmap to set the timing of the scan. DO not specify values using Tx, where x is a number. Instead, use just the number, e.g., '3'.
    - **`host_timeout`** *(string)*: This is the timeout for the scan. It is the maximum amount of time that the CROWler will wait for a host to respond to a scan.
    - **`min_rate`** *(string)*: This is the minimum rate at which the CROWler will scan hosts. It is the minimum number of packets that the CROWler will send to a host per second.
    - **`max_retries`** *(integer)*: This is the maximum number of times that the CROWler will retry a scan on a host. If the CROWler is unable to scan a host after this number of retries, it will move on to the next host.
    - **`source_port`** *(integer)*: This is the source port that the CROWler will use for scanning. It is the port that the CROWler will use to send packets to hosts.
    - **`interface`** *(string)*: This is the interface that the CROWler will use for scanning. It is the network interface that the CROWler will use to send packets to hosts. Use this option with a port that is behind a VPN or a proxy for better results.
    - **`spoof_ip`** *(string)*: This is the IP address that the CROWler will use to spoof its identity. It is the IP address that the CROWler will use to send packets to hosts. Use this option with a port that is behind a VPN or a proxy for better results.
    - **`randomize_hosts`** *(boolean)*: This is a flag that tells the CROWler to randomize the order in which it scans hosts. This is useful for avoiding detection by intrusion detection systems.
    - **`data_length`** *(integer)*: This is the length of the data that the CROWler will send to hosts. It is the length of the data that the CROWler will use to send packets to hosts. Use this option with a port that is behind a VPN or a proxy for better results.
    - **`delay`** *(string)*: This is the delay between packets that the CROWler will use for scanning. It is the delay between packets that the CROWler will use to send packets to hosts. Use this option with a port that is behind a VPN or a proxy for better results. For the delay you can also use the CROWler exprterpreter to generate delay values at runtime, e.g., 'random(1, 3)' or 'random(random(1,3), random(5,8))'.
    - **`mtu_discovery`** *(boolean)*: This is a flag that tells the CROWler to use MTU discovery when scanning hosts. This is useful for avoiding detection by intrusion detection systems.
    - **`scan_flags`** *(string)*: This is the flags that the CROWler will use for scanning. It is the flags that the CROWler will use to send packets to hosts. Use this option with a port that is behind a VPN or a proxy for better results.
    - **`ip_fragment`** *(boolean)*: This is a flag that tells the CROWler to fragment IP packets. This is useful for avoiding detection by intrusion detection systems.
    - **`max_port_number`** *(integer)*: This is the maximum port number to scan (default is 9000).
    - **`max_parallelism`** *(integer)*: This is the maximum number of parallelism.
    - **`dns_servers`** *(array)*: This is a list of custom DNS servers.
      - **Items** *(string)*
    - **`proxies`** *(array)*: Proxies for the database connection.
      - **Items** *(string)*
- **`rulesets`** *(array)*: This is the configuration for the rulesets that the CROWler will use to crawl, interact, scrape info and detect stuff on the provided Sources to crawl.
  - **Items** *(object)*
    - **`path`** *(array)*
      - **Items** *(string)*
    - **`host`** *(string)*
    - **`port`** *(string)*
    - **`region`** *(string)*
    - **`token`** *(string)*
    - **`secret`** *(string)*
    - **`timeout`** *(integer)*
    - **`type`** *(string)*
    - **`sslmode`** *(string)*
    - **`refresh`** *(integer)*

- **`debug_level`** *(integer)*