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

## The corpus is not committed

Most real PDFs cannot be redistributed, and committing them would bloat the
module zip, so the `.pdf` files here are gitignored. A clean checkout has empty
`should-pass/`/`should-convert/` dirs and `TestRealWorldCorpus` skips.

Populate the corpus reproducibly from hashes, without redistributing the files:

1. Copy `manifest.example.json` to `manifest.json` and list each document with
   its source URL, expected sha256, license, and producer.
2. Run `scripts/fetch-realworld-corpus.sh`, which downloads each file to its
   `path` under this directory and verifies its sha256.

## Sourcing

Prefer permissively-licensed or public-domain sources so the manifest URLs stay
stable and the files are legally fetchable:

- **arXiv** papers under CC-BY (LaTeX/pdfTeX, and PDF/A where the author opted in)
- **US federal** publications (public domain — GPO, agency reports)
- **Wikimedia Commons** PDFs (CC / public domain)
- Output you generate yourself from LibreOffice, Word, Ghostscript, and the
  common PDF/A converters, saved with its provenance recorded in the manifest.

Record the license of every file in the manifest before adding it.
