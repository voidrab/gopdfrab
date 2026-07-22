# Changelog

All notable changes to gopdfrab are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html) from 1.0
onward.

## Versioning and stability

**Pre-1.0 (now): the API is not stable.** Anything may change between releases
while PDF/A-1b verification and conversion is hardened toward 1.0. Pin a version
if you depend on current behavior.

**From 1.0, the stability guarantee covers the root package only** —
`github.com/voidrab/gopdfrab`. Everything under `internal/` is implementation
detail, is not importable by external code, and may change in any release without
notice.

- A **breaking change** is any change to the root package that requires a
  consumer to edit their code to keep compiling, or that alters documented
  behavior of a public symbol. These happen only in a major version after 1.0.
- **Additions** (new functions, types, options) are minor releases.
- **Fixes** that do not change the public surface are patch releases. Note that a
  verifier or converter producing a *more correct* result for a given input is
  treated as a fix, not a breaking change, even if a caller was relying on the
  previous verdict.
- **Deprecation:** a symbol slated for removal is marked with a `// Deprecated:`
  comment naming its replacement, kept for at least one subsequent minor release,
  and removed no earlier than the next major version.

## [Unreleased]

This is the first changelog entry; earlier history lives in the git log. Recent
notable work:

### Added
- Standard security handler **decryption**: RC4 40/128, AES-128 (R4) and AES-256
  (R6). Empty-password files decrypt automatically through `Open`/`OpenBytes`;
  `OpenWithPassword` / `OpenBytesWithPassword` take an explicit user or owner
  password.
- Typed, `errors.Is`-matchable sentinels `ErrEncrypted` and `ErrPasswordRequired`
  on the root package.
- Stable JSON encoding of results: `Check`, `PDFError` and `Result` now implement
  `MarshalJSON`, so `json.Marshal` of a verify/convert result produces a
  documented shape instead of empty objects.

### Changed
- Convert refuses a file that genuinely requires a password with
  `ErrPasswordRequired` instead of emitting a document with undecryptable
  streams.

### Fixed
- Undecodable content streams are now reported (`StreamUndecodable`) rather than
  silently turning a violation into a pass.
- `InflateZlib` returns `ErrOutputTooLarge` when a stream would inflate past the
  256 MB cap, instead of silently truncating to a prefix that downstream checks
  then trusted as complete. The deliberate leniency toward truncated/CRC-broken
  streams (which still return their inflated prefix) is unchanged.
