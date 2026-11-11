# Security policy

## Supported versions

Golar is before 1.0, so only the latest release receives security fixes.
Upgrade to it before reporting a problem you found in an older version.

| Version | Supported |
| --- | --- |
| Latest release (currently v0.8.x) | yes |
| Older releases | no |

## Reporting a vulnerability

Do not open a public issue, pull request or discussion for a security
problem.

Report it privately through GitHub: open the repository's
[Security tab](https://github.com/dayanchm/golar/security) and choose
**Report a vulnerability**. Include:

- the affected package and version or commit,
- how to reproduce the problem, ideally with a minimal program or test,
- what an attacker can do with it.

You will get a reply within 7 days. Once the problem is confirmed, a fix is
prepared in a private advisory and released as a new version, and the
advisory is published with credit to you unless you prefer otherwise.

## Scope

Reports are most useful for the code that protects applications:
`auth` (passwords, tokens, CSRF and rate limiting), `http/session`,
`http/middleware` (security headers and body limits), `validation`, `storage` (path handling and signed URLs) and the
templates `golar new` generates. Vulnerabilities in dependencies are tracked
by `govulncheck` and Dependabot; report them here only when Golar's use of
the dependency makes it exploitable.
