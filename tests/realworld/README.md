# Real-world corpus

The Isartor and veraPDF suites are hand-built to exercise one clause each. Real
PDFs are not like that, so this directory holds a corpus of documents from actual
producers. It answers two questions the synthetic suites cannot:

- **`should-pass/`** — real files that are genuinely PDF/A-1b (an authoring tool
  claims PDF/A and the veraPDF reference verifier agrees). gopdfrab must verify
  every one of them clean. A verifier that flags real Acrobat/Ghostscript output
  is unusable regardless of what the synthetic suites say, so a rejection here is
  a false positive and fails `TestRealWorldCorpus`.
- **`should-convert/`** — ordinary, non-PDF/A documents across producers, page
  counts, and font technologies. The metric is what fraction convert to
  conformant output, and how many only get there by rasterizing (a lossy
  fallback). This is reported, not gated, since the fraction is a moving target.

## The PDFs are not committed; the manifest is

Most real PDFs cannot be redistributed, and committing them would bloat the
module zip, so the `.pdf` files here are gitignored. What *is* committed is
`manifest.json`: the inventory of the corpus — each file's hash, licence,
provenance, and an optional source URL, never its bytes. A clean checkout has an
empty inventory, empty corpus dirs, and `TestRealWorldCorpus` skips.

### Adding files

1. Drop PDFs into `should-pass/` (real PDF/A-1b — confirm with
   `verapdf --flavour 1b file.pdf` first) or `should-convert/` (ordinary PDFs).
2. Run `scripts/gen-realworld-manifest.sh`. It hashes each file and merges it
   into `manifest.json`, preserving fields you have already filled in and
   stubbing new entries with `"license": "TODO"`.
3. Edit each new entry's `license` (and `url`, if the file is publicly hosted).
4. Commit `manifest.json`.

`TestRealWorldCorpus` then checks every present file against the inventory: an
unlisted file, a hash mismatch, or a `TODO` licence fails the test, so the
committed inventory stays honest.

### Getting the corpus onto another machine

For entries with a `url` (publicly hosted), `scripts/fetch-realworld-corpus.sh`
re-downloads them and verifies the recorded hash. Entries with no `url` (e.g.
self-generated PDF/A) are local-only — they live only where they were created,
and the manifest records their hash and provenance for reference.

## Sourcing

Prefer permissively-licensed or public-domain sources so the manifest URLs stay
stable and the files are legally fetchable:

- **arXiv** papers under CC-BY (LaTeX/pdfTeX, and PDF/A where the author opted in)
- **US federal** publications (public domain — GPO, agency reports)
- **Wikimedia Commons** PDFs (CC / public domain)
- Output you generate yourself from LibreOffice, Word, Ghostscript, and the
  common PDF/A converters, saved with its provenance recorded in the manifest.

Record the license of every file in the manifest before adding it.
