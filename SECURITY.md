# Security policy

## Supported versions

Gopad is currently in v1 development. Security fixes are applied to the
`main` branch until a release policy is published.

## Reporting a vulnerability

Please do not open a public issue for an exploitable vulnerability. Use
[GitHub private vulnerability reporting](https://github.com/SlmnFz/gopad/security/advisories/new)
when it is enabled, or contact the maintainers privately through the
repository owner. Include the affected commit, reproduction steps, impact,
and any suggested mitigation.

Gopad's v1 identity model is intentionally username-only and unauthenticated.
Document links grant edit access, so deployments must not be treated as a
secure private workspace without an authentication and authorization layer.
