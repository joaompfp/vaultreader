# Security policy

## Supported versions

Only the latest release (and the current `main`) receives security fixes. Please reproduce a problem on the latest version before reporting it.

## Reporting a vulnerability

Report vulnerabilities **privately** through GitHub's private vulnerability reporting: open the repository's **Security** tab, choose **Advisories**, then **Report a vulnerability**. Please do not open a public issue or pull request for a security problem.

Include what you can: affected version (`GET /health` shows it), configuration relevant to the issue (`config.json` with secrets removed), reproduction steps or a proof of concept, and the impact you expect.

This is a spare-time project, so there is **no SLA**. I will try to acknowledge reports within a week and to publish a fix and advisory as soon as is practical; credit is given unless you prefer otherwise.

## Scope

In scope: the VaultReader server and SPA in this repository and the published container image. The threat model and the hardening the project already provides (admin token, share-link signing, writable paths, rate limiting, request body caps, CSP) are described in [docs/security.md](docs/security.md).

Out of scope: problems that need an already-malicious operator or write access to the vault files, deployments that expose the app without the authentication layer the docs recommend, and vulnerabilities in third-party software that only affect VaultReader through a version you did not get from this repository.
