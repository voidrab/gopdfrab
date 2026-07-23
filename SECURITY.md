# Security Policy

gopdfrab parses untrusted, potentially hostile PDF input by design — that is its
job. We take reports of memory-safety issues, denial-of-service vectors, and
incorrect verdicts that could mislead a security decision seriously.

## Reporting a Vulnerability

**Please do not open a public issue for a suspected vulnerability.**

Report it privately, either way:

- Preferred: open a [GitHub private security advisory](https://github.com/voidrab/gopdfrab/security/advisories/new).
- Or email **contact@voidrab.com** with `[SECURITY]` in the subject.

Include, as far as you can:

- the affected version or commit,
- a description of the issue and its impact,
- a minimal PDF or input that reproduces it (attach the file; do not paste raw
  bytes inline),
- the entry point you called (`Verify`, `Convert`, `Open`, a fuzz target, …).

A reproducing input file is by far the most useful thing you can send — it goes
straight into the corpus as a regression test once fixed.

## What we consider a vulnerability

- A crash, panic, unbounded memory growth, or non-terminating loop reachable from
  any public entry point on crafted input. Resource limits are documented in the
  README; a way to defeat them counts.
- A verifier that reports a non-conformant file as conformant (a false pass), or
  otherwise answers "valid" when it could not actually check — the failure mode
  the project guards against most carefully.
- A converter that silently drops or corrupts content while reporting success.

Ordinary false positives (flagging a conformant file) and feature gaps are normal
bugs — please open a regular [issue](https://github.com/voidrab/gopdfrab/issues)
for those.

## Response expectations

This is an open-source project maintained on a best-effort basis. We aim to:

- acknowledge a report within **3 business days**,
- provide an initial assessment and a remediation plan within **10 business
  days**,
- coordinate a disclosure timeline with you before any public write-up.

We will credit reporters who wish to be named once a fix is released.

## Supported versions

Until the 1.0 release the API is still changing (see the stability note in
`CHANGELOG.md`). Fixes land on `main` and, when a release is tagged, in the latest
tagged release. Only the latest release and `main` receive security fixes
pre-1.0.
