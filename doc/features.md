
# Features

The **CROWler** is a comprehensive web crawling and scraping tool designed to perform various tasks related to web content discovery and data collection. Below is a detailed list of its features, along with their descriptions and benefits.

## Table of Contents

- [Web Crawling](#features-group-1-web-crawling)
- [API-Based Search Engine](#features-group-2-powerful-api-based-search-engine)
- [Web Scraping](#features-group-3-web-scraping)
- [Action Execution](#features-group-4-action-execution)
- [Technology Detection](#features-group-5-technology-detection)
- [Network Information Collection](#features-group-6-network-information-collection)
- [Image and File Collection](#features-group-7-image-and-file-collection)
- [API Integration](#features-group-8-api-integration)
- [Comprehensive Ruleset System](#features-group-9-comprehensive-ruleset-system)
- [Plugin Support](#features-group-10-plugin-support)
- [Data Storage and Management](#features-group-11-data-storage-and-management)
- [Configuration and Scalability](#features-group-12-configuration-and-scalability)
- [Security and Privacy](#features-group-13-security-and-privacy)
- [Error Handling and Logging](#features-group-14-error-handling-and-logging)
- [User Interface and Console](#features-group-15-user-interface-and-console)
- [Cybersecurity Features](#features-group-16-cybersecurity-features)
- [Containerization](#features-group-17-containerization)
- [Event-Driven Architecture](#features-group-18-event-driven-architecture)
- [AI and traditional Agents](#features-group-19-ai-and-traditional-agents)
- [3rd party services Data Integration](#features-group-20-3rd-party-services-data-integration)

## (Features Group 1) Web Crawling

- **Recursive Crawling**: Supports deep crawling of websites, following links recursively to discover new content.
  - *Benefits*: Enables thorough exploration of websites to uncover hidden pages and data.

- **Human Browsing Mode**: Simulates human-like browsing behavior to access content that might be blocked by automated bots. This is part of the Human Behavior Simulation (HBS) architecture.
  - *Benefits*: Helps bypass basic bot detection mechanisms to access dynamic content.

- **Fuzzing Mode**: Automatically tests web pages with various inputs to discover hidden functionalities and vulnerabilities.
  - *Benefits*: Aids in security testing by discovering potential weaknesses in web applications.

- **Customizable Browsing Speed**: Allows users to configure the speed of crawling to avoid overloading servers, being detected, or triggering anti-bot mechanisms. Speed is also configurable at runtime and per source, allowing for more human-like behavior.
  - *Benefits*: Prevents excessive traffic to target websites, ensuring minimal impact on their performance and stability while reducing the risk of being blocked.

- **Per Source Configuration**: Allows users to define custom configurations for each source (URL) to control crawling behavior, such as the depth of crawling, the frequency of requests, the speed of crawling for that specific SOurce etc.
  - *Benefits*: Provides fine-grained control over the crawling process to optimize performance and avoid detection.

- **Human Behavior Simulation (HBS)**: A system architecture designed to mimic human-like browsing patterns to avoid detection by anti-bot systems.
  - *Benefits*: Enhances low-noise operations and reduces the risk of being blocked by websites and proxy services.

- **Dynamic Content Handling**: Supports the execution of JavaScript to access dynamically generated content.
  - *Benefits*: Allows access to content that is rendered dynamically by client-side scripts.

- **Keyword Extraction**: Extracts keywords from web pages to identify relevant topics and themes.
  - *Benefits*: Helps categorize and organize content for analysis and indexing. Keywords can also be used in security searches and events to identify sources of interest.

- **Site Language Detection**: Detects the language of a website to support multilingual crawling and content analysis. Even in the absence of language tags, CROWler can detect the language of a page.
  - *Benefits*: Facilitates language-specific processing and analysis of web content.

- **Content Analysis**: Analyzes the content of web pages to extract metadata, entities, and other structured information.
  - *Benefits*: Provides insights into the content of web pages for categorization, indexing, and analysis.

- **Source Categorization**: Allows users to define categories for sources (URLs), which can be used to filter and prioritize crawling operations, as well as for marketing and security operations.
  - *Benefits*: Enables category-based correlation and analysis of data.

- **Each VDI is remotely accessible via VNC and noVNC**: Each Virtual Desktop Image (VDI) can be accessed remotely via VNC and noVNC, allowing users to monitor and interact with the crawling process.
  - *Benefits*: Provides visibility and control over the crawling process.

- **User can customize which requests the browser makes**: Users can customize the requests made by the browser, including images, CSS, plugins, objects etc. This is extremely useful to users who wish to reduce Proxy bandwidth during data crawling and collection.
  - *Benefits*: Allows users to optimize the crawling process by controlling the types of requests made by the browser.

- **User can customize the browser's User-Agent**: Users can customize the User-Agent string sent by the browser to mimic different browsers or devices.
  - *Benefits*: Helps avoid detection by anti-bot systems and ensures compatibility with various websites.

- **The CROWler uses a real browser**: The CROWler uses a real browser (Chromium, Chrome and Firefox) to render and interact with web pages, enabling access to dynamic content and JavaScript-driven features.
  - *Benefits*: Ensures accurate rendering of web pages and access to dynamic content.

## (Features Group 2) Powerful API-Based Search Engine

- **Advanced Search Queries**: Supports complex search queries using operators like AND (&&) and OR (||), and "" for precise search results.
  - *Benefits*: Facilitates targeted searches to retrieve specific information from web pages.

- **Search Result Analysis**: Analyzes search results to extract relevant information such as titles, snippets, and URLs.
  - *Benefits*: Helps identify relevant content quickly and efficiently.

- **Search Result Export**: Allows exporting search results in various formats like CSV and JSON.
  - *Benefits*: Facilitates further processing and analysis of search results.

- **Dorking Techniques**: Supports advanced search techniques like Google Dorking to discover sensitive information and vulnerabilities.
  - *Benefits*: Useful for security assessments and reconnaissance.

- **Entity Correlation**: Correlates entities extracted from search results to identify relationships and patterns.
  - *Benefits*: Provides insights into the connections between entities across different sources.

- **High Performance API**: Provides a high-performance API for querying and retrieving search results.
  - *Benefits*: Ensures fast and efficient access to search data.

## (Features Group 3) Web Scraping

- **Customizable Scraping Rules**: Users can define specific rules for data extraction using CSS selectors, XPath, and other methods.
  - *Benefits*: Provides flexibility to extract specific data points from web pages as per user requirements.

- **Post-Processing of Scraped Data**: Includes steps to transform, clean, and validate data after extraction, as well as to enrich it with additional information, metadata, and annotations using plugins and AI models.
  - *Benefits*: Ensures the quality and usability of the collected data.

- **Data Transformation**: Supports data transformation operations like normalization, aggregation, and filtering.
  - *Benefits*: Helps prepare data for analysis and integration with other systems.

- **Data Enrichment**: Enhances scraped data with additional information from external sources or AI models.
  - *Benefits*: Improves the quality and relevance of the collected data.

- **3rd party Integration**: Integrates with third-party services and APIs to enrich scraped data with external information.
  - *Benefits*: Provides access to a wide range of external data sources for data enrichment.

## (Features Group 4) Action Execution

- **Automated Interactions**: Can perform actions like clicking, filling out forms, and navigating websites programmatically. Actions are executed at the SYSTEM level, making CROWler undetectable by most anti-bot systems. This is part of the Human Behavior Simulation (HBS) architecture.
  - *Benefits*: Enables the automation of repetitive tasks, improving efficiency in data collection.

- **Advanced Interactions**: Supports complex interactions like drag-and-drop, mouse hover, and keyboard inputs.
  - *Benefits*: Allows handling sophisticated user interface elements that require advanced manipulation.

## (Features Group 5) Technology Detection

- **Framework and Technology Identification**: Uses detection rules to identify:
  - Technologies (e.g., servers, programming languages, plugins)
  - Frameworks (e.g., server-side CMS, client-side JavaScript libraries)
  - Libraries
  - Vulnerabilities (e.g., outdated software versions, known security issues, XSS, SQL injection, and more)
  - *Benefits*: Provides insights into the tech stack of a site, which can be useful for competitive analysis or vulnerability assessment.

- **Fingerprinting Techniques**: Employs fingerprinting techniques like HTTP headers, cookies, and JavaScript objects to identify technologies.

- **Vulnerability Detection**: Detects known vulnerabilities in web applications and services.
  - *Benefits*: Helps identify security weaknesses that need to be addressed.

- **Security Headers Analysis**: Analyzes security headers like Content Security Policy (CSP), HTTP Strict Transport Security (HSTS), and others to assess the security posture of a website.
  - *Benefits*: Provides insights into the security measures implemented by a website.

- **SSL/TLS Analysis**: Analyzes SSL/TLS certificates and configurations to identify security risks and compliance issues.
  - The CROWler can detect and analyze the following:
    - Certificate information
    - Certificate chain (and order)
    - Expiry date
    - Key length
    - Signature algorithm
    - Cipher suites
    - Protocols
    - Vulnerabilities (e.g., Heartbleed, POODLE, DROWN)
  - *Benefits*: Helps ensure secure communication between clients and servers.

- **3rd party Integration**: Integrates with third-party services like Shodan, VirusTotal, and others to gather additional information about web assets.
  - *Benefits*: Provides access to external threat intelligence and security data.

## (Features Group 6) Network Information Collection

- **DNS and WHOIS Lookup**: Performs DNS resolution and WHOIS queries to gather domain information.
  - *Benefits*: Facilitates understanding of domain ownership and network infrastructure.

- **Service Scout**: Detects services running on a host using various scanning techniques. Service Scout can be extended via Nmap plugins.
  - *Benefits*: Useful in security assessments for identifying:
    - Open ports and services
    - Vulnerabilities
    - Test protocols and services

## (Features Group 7) Image and File Collection

- **Automated Collection**: Collects images and files from websites during the crawling process.
  - *Benefits*: Enables gathering of rich media content alongside textual data.

- **Full Web Page Screenshots**: Captures full-page screenshots (including websites with "infinite scrolling") of web pages for visual analysis and archiving.
  - *Benefits*: Provides a visual representation of web pages for reference and analysis.

## (Features Group 8) API Integration

- **REST API**: Provides an API for integrating with other systems and managing CROWler's operations programmatically.
  - *Benefits*: Facilitates automation and integration with existing data processing pipelines.

- **Bulk Upload Tools**: Supports bulk uploading of URLs and data for processing.
  - *Benefits*: Streamlines the process of adding multiple sources for crawling and scraping.

## (Features Group 9) Comprehensive Ruleset System

- **Ruleset Architecture**: Supports a comprehensive ruleset system for defining custom crawling and scraping rules. Specifically, four types of rules:
  - **Crawling Rules**: Define how to navigate a website.
  - **Scrape Rules**: Define what to extract from a page.
  - **Action Rules**: Define what to do on a page.
  - **Detection Rules**: Define what (and how) to detect technologies and vulnerabilities.
  - Ruleset architecture is declarative (can be expressed in both YAML and JSON) and can be shared across instances and updated dynamically.
  - Ruleset architecture can be extended with JavaScript plugins.
  - *Benefits*: Allows users to define complex logic for data extraction and processing, site navigation, and technology detection.

- **Ruleset Management**: Provides tools for managing and sharing rulesets across different instances.
  - *Benefits*: Enhances reusability and collaboration among users.

## (Features Group 10) Plugin Support

- **JavaScript Plugins**: Supports custom JavaScript plugins for extending functionality.
  - *Benefits*: Allows customization and enhancement of CROWler's capabilities to meet specific needs.

## (Features Group 11) Data Storage and Management

- **Database Integration**: Stores collected data in a structured format in databases like PostgreSQL.
  - *Benefits*: Ensures organized and easily retrievable data for analysis.

- **File Storage Options**: Configurable storage for images and other media files.
  - *Benefits*: Enables efficient handling of large volumes of media content.

## (Features Group 12) Configuration and Scalability

- **Configurable Environment**: Supports detailed configuration options for customizing crawling and scraping behavior.
  - *Benefits*: Provides flexibility to adapt to different use cases and environments.

- **Scalability**:
  - The CROWler Engine supports multiple workers for parallel processing of tasks.
  - All CROWler components can be scaled horizontally. Multiple Engines,
    multiple VDIs (Virtual Desktop Images) and also multiple APIs (Scaling the
     Search API requires a load Balancer).
  - Database can also be scaled (check PostgresSQL documentation for more
    information).
  - The CROWler also comes with a tool to have static scaled deployments (useful
    when a user does not want to use tools like Kubernetes or when the user is
     crawling the entire Internet, where tasks never ends basically).
  - *Benefits*: Ensures the tool can handle high workloads and scale as needed.

## (Features Group 13) Security and Privacy

- **Service Scout**: Provides features equivalent to Nmap for security auditing.
  - *Benefits*: Helps identify security vulnerabilities and ensures compliance with security standards.

- **Data Anonymization**: Supports techniques for anonymizing collected data to ensure privacy compliance.
  - *Benefits*: Protects sensitive information and complies with data protection regulations.

## (Features Group 14) Error Handling and Logging

- **Robust Error Handling**: Provides mechanisms to handle errors and retry operations automatically.
  - *Benefits*: Improves reliability by ensuring that transient issues do not disrupt the crawling process.

- **Detailed Logging**: Configurable logging options to capture detailed operational logs for troubleshooting.
  - *Benefits*: Aids in diagnosing issues and optimizing performance.

## (Features Group 15) User Interface and Console

- **Admin Console**: Offers an admin interface for monitoring and managing CROWler operations.
  - *Benefits*: Provides an intuitive interface for users to oversee and control crawling activities.

## (Features Group 16) Cybersecurity Features

- **Security Testing**: Supports fuzzing and scanning capabilities for identifying vulnerabilities in web applications.
  - *Benefits*: Helps improve the security posture of web assets.

- **Compliance Checks**: Includes features for checking compliance with security standards and best practices. **Note**: This feature requires additional configuration and purchase of specific rulesets.
  - *Benefits*: Ensures adherence to security guidelines and regulations.

- **Security Automation**: Enables automation of security testing and monitoring tasks.
  - *Benefits*: Enhances efficiency and accuracy in security assessments.

- **Native Support for Third-Party Security Services**: Integration with security services like Shodan, VirusTotal, and others.
  - *Benefits*: Provides access to external security intelligence and threat data.

- **Full Suite of TLS Fingerprinting**: Provides comprehensive TLS fingerprinting capabilities, including JA3, JA4, and others.
  - *Benefits*: Helps identify the underlying technologies and configurations of web servers.

## (Features Group 17) Containerization

- **Docker Support**: Can be easily containerized and deployed in containerized environments.
  - *Benefits*: Simplifies deployment and management in container orchestration platforms.

## (Features Group 18) Event-Driven Architecture

- **Event-Driven Processing**: Utilizes an event-driven architecture for handling asynchronous tasks and processing data in real-time.
  - *Benefits*: Improves performance and scalability by decoupling components and processing tasks in parallel.

- **Events can also be scheduled**: Events can be scheduled to run at specific times or intervals.
  - *Benefits*: Enables automation of tasks and data collection at predefined times.

- **Custom Events**: Users can define custom events to trigger specific actions or workflows.
  - *Benefits*: Provides flexibility to create custom workflows and automate tasks based on specific conditions.

- **Event-based plugins execution**: Plugins can be executed based on events, allowing for custom processing and integration with external systems.
  - *Benefits*: Enables extensibility and customization of CROWler's functionality.

## (Features Group 19) AI and Traditional Agents

- **AI Integration**: Supports integration with AI models for data analysis, entity recognition, and other tasks.
  - *Benefits*: Enhances data processing capabilities and enables advanced analysis of web content.

- **AI Agents**: Supports AI agents for performing complex tasks like natural language processing, image recognition, and sentiment analysis.
  - *Benefits*: Enables advanced data processing and analysis using AI techniques.

- **Traditional Agents**: Supports traditional agents for executing predefined tasks and workflows.
  - *Benefits*: Provides flexibility to choose between AI and traditional agents based on the requirements of the task.

- **Event-Driven AI Processing**: Utilizes an event-driven architecture for AI Agents processing to handle asynchronous tasks efficiently.
  - *Benefits*: Improves performance and scalability of AI processing tasks.

- **Agent-based plugins execution**: Plugins can be executed by AI or traditional agents, allowing for custom processing and integration with external systems.
  - *Benefits*: Enables extensibility and customization of CROWler's functionality based on the type of agent used.

- **Pre-Deployed AI models into containers**: AI models can be pre-deployed into containers (both with CUDA and non-CUDA accelerated) for best performance, easy integration and execution.
  - *Benefits*: Simplifies the deployment and management of AI models within CROWler.

- **Commercially available Multi-model AI Agents**: CROWler comes with commercially available multi-model AI agents for various tasks like NLP, Image Recognition, Sentiment Analysis, etc.
  - *Benefits*: Provides out-of-the-box AI capabilities for advanced data processing and analysis.

## (Features Group 20) 3rd party services Data Integration

- **Integration with 3rd party services**: CROWler can be integrated with 3rd party services like Shodan, VirusTotal, etc. to gather additional information about web assets.
  - *Benefits*: Provides access to external threat intelligence and security data.

- **Data Enrichment with 3rd party services**: CROWler can enrich scraped data with additional information from external services.

- **Custom 3rd party integration**: CROWler can be extended to integrate with custom 3rd party services APIs using plugins.
  - *Benefits*: Enables access to a wide range of external data sources for data enrichment and analysis.

- **3rd party Cybersecurity service native support**: For Cybersecurity applications the CROWler can be integrated with 3rd party services like Shodan, VirusTotal, etc. to gather additional information about web assets using the native support for t
  - *Benefits*: Provides access to external threat intelligence and security data.
  - List of 3rd party services natively supported in the CROWler:

  ### IP Scanners

  AbuseIPDB
  IPVoid
  Censys
  Shodan

  ### URL Scanners

  SSL Labs
  URLHaus
  ThreatCrowd
  Cuckoo
  VirusTotal
  PhishTank
  Google Safe Browsing
  OpenPhish
  Hybrid Analysis
  Cisco Umbrella
  AlienVault

  ### File Scanners

  VirusTotal (File)
  Hybrid Analysis (File)
  Cuckoo (File)

---
