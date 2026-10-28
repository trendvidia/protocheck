# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in `protocheck`, please report it
privately to **security@trendvidia.com**. PGP key available on request.
**Do not open a public GitHub issue or pull request** for
security-sensitive reports.

Please include:

- A description of the issue and the impact you observed.
- Steps to reproduce, ideally a `.proto` and a message, or a Go test.
- The commit or release tag the report applies to.
- Whether you intend to publish a writeup, and on what timeline.

## What to Expect

- Acknowledgement within **3 business days**.
- An initial assessment (severity, scope, affected versions) within **7
  business days**.
- Status updates at least every 14 days until the report is closed.
- Credit in the release notes and any published advisory, unless you ask
  to remain anonymous.

## Scope

In scope:

- Validation bypasses: a message that violates its declared rules but
  validates clean.
- Panics, hangs, or unbounded memory while validating an
  attacker-controlled message.
- Values of `@sensitive` fields appearing in a violation or report
  instead of being redacted (RFC-001 §6.7).

Out of scope:

- Vulnerabilities in third-party dependencies — please report those
  upstream. We will of course bump dependency versions in response.
- Issues that require a malicious *trusted* schema (a hostile `.proto`,
  rule, or registered function supplied by the operator). Schemas are
  part of the trust boundary; only the message being validated is
  treated as untrusted.
- Third-party expression engines plugged in through
  `WithExpressionEngine`; report those to the engine's maintainers.
- Performance regressions and slow but bounded validation of legitimate
  messages (these are tracked as normal issues).

## Supported Versions

Only the latest minor release of the current major version (`v2`)
receives security fixes.

## Coordinated Disclosure

We follow a 90-day coordinated disclosure window by default. If a fix
ships sooner, the advisory will be published immediately. Reports
involving an actively exploited vulnerability will be triaged
out-of-band — flag this in your initial email.
