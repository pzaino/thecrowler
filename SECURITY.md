# Security Policy

## Reporting a Vulnerability

We take the security of our project seriously and appreciate your efforts to responsibly disclose vulnerabilities. If you believe you have found a security vulnerability in the **CROWler** project, please report it by following the steps below.

### What Constitutes a Vulnerability

A security vulnerability in the **CROWler** project is any issue that can potentially allow an attacker to compromise the integrity, availability, or confidentiality of the data and functionalities provided by the application. Examples include but are not limited to:

- Buffer Overflows
- Denial of Service (DoS)
- Improper Handling of User Input
- Insecure Use of Cryptographic Algorithms
- Memory Leaks
- Race Conditions
- Unsafe Concurrency Practices
- SQL Injection
- Cross-Site Scripting (XSS) (for plugins, given they are written in JavaScript and can be executed on a browser)
- Cross-Site Request Forgery (CSRF) (for plugins, given they are written in JavaScript and can be executed on a browser)
- Directory Traversal
- Authentication and Authorization Flaws
- Insecure Deserialization

### How to Report

Please report vulnerabilities by opening a private issue on our GitHub repository:

1. **GitHub Issue Tracker:** Open a private issue [here](https://github.com/pzaino/thecrowler/issues). Make sure the issue is marked as confidential and contains detailed information about the vulnerability and steps to reproduce it.

### Coordinated Vulnerability Disclosure Guidelines

- **Initial Acknowledgment:** We will acknowledge receipt of your report within 2 business days.
- **Assessment:** We will assess the vulnerability and determine its impact. This process may take up to 5 business days.
- **Mitigation:** If the vulnerability is confirmed, we will work on a mitigation plan and provide an estimated timeline for the fix. This typically takes between 15 and 30 days.
- **Disclosure:** We will notify you when the vulnerability is fixed and coordinate a public disclosure, ensuring you receive credit for the discovery if you wish.

## Security Contacts

- **GitHub Issue Tracker:** [Report an issue](https://github.com/pzaino/thecrowler/issues)

## Supported Versions

Use this section to verify if the version of **CROWler** you are using is currently supported and eligible for security updates.

| Version | Supported          |
| ------- | ------------------ |
| 1.x.y   | :white_check_mark: |
| 0.x.y   | :x:                |
