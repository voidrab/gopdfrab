# Vendored Arlington PDF Model data

`tsv/1.4/`, `tsv/1.7/` and `tsv/latest/` are verbatim from the
[Arlington PDF Model](https://github.com/pdf-association/arlington-pdf-model).
`tsv/1.7/` was taken at upstream commit d148fee (2026-10-21).

`tsv/1.4/` and `tsv/1.7/` are the sources of truth for the generated object models
(`internal/arlington`, `model-1.4.bin` and `model-1.7.bin`).
`tsv/latest/` is used only to compute, per type, the set of keys introduced after the
model's version.

Licensed under Apache-2.0; see `LICENSE` in this directory. Copyright PDF Association, Inc.
