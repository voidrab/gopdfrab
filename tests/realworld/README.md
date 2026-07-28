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

### Populating it from the public collections

`scripts/source-realworld-corpus.py` does the bulk work, in four resumable
phases. Staging lives outside the repository
(`${XDG_CACHE_HOME:-~/.cache}/gopdfrab-corpus`), so the working tree never
carries download sidecars.

```sh
scripts/source-realworld-corpus.py plan       # query the source APIs
scripts/source-realworld-corpus.py fetch --max-bytes 3.5G
scripts/source-realworld-corpus.py classify   # veraPDF decides the split
scripts/source-realworld-corpus.py manifest   # merge provenance, hash everything
scripts/source-realworld-corpus.py status     # counts and sizes
```

It records each file's licence, URL, producer and provenance from the source's
own API, so nothing has to be filled in by hand — a corpus of a few thousand
files cannot be annotated one entry at a time.

Then `scripts/gen-realworld-selfmade.sh` generates the self-made half: the
producer/feature combinations the public collections do not reliably supply
(PDF/A-1b and -2b exports, tagged output, encryption at every revision, object
streams, CCITT and DCT images, CJK/Arabic CID fonts, and a scanned page with an
invisible OCR text layer). It writes into the same staging directory, so
`classify` and `manifest` pick those up unchanged.

**Classification is measured, not guessed.** Every file is run through veraPDF
and its verdict decides the directory. Provenance predicts nothing: a US
Federal Register PDF is iText output and fails 1b, while a LibreOffice PDF/A-1b
export passes.

**Only redistributable licences are accepted** — CC0, CC-BY, CC-BY-SA,
US-federal public domain, and self-generated — so every downloaded entry stays
legally fetchable from its recorded URL. A licence string the tool does not
recognise is treated as not-free and the candidate is dropped before download.

### Adding files by hand

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

### Running it

```sh
go test -run TestRealWorldCorpus -v -timeout 4h .
```

Not under `-short`, so no CI job runs it; a full local run over a multi-gigabyte
corpus takes tens of minutes. Two environment variables extend it:

- `GOPDFRAB_REALWORLD_REPORT=<path>` writes one JSON record per file — verdict,
  issue clauses, rasterized pages, raster drops, size, completion offset. Triage
  groups that by clause; re-running files one at a time does not scale.
- `GOPDFRAB_REALWORLD_DETERMINISM=1` converts the whole corpus twice and
  compares the outputs byte for byte. Every nondeterminism this library has had
  was a map-iteration order reaching output, and each was found only by a parity
  run.

### Getting the corpus onto another machine

For entries with a `url` (publicly hosted), `scripts/fetch-realworld-corpus.sh`
re-downloads them and verifies the recorded hash. Entries with no `url` (e.g.
self-generated PDF/A) are local-only — they live only where they were created,
and the manifest records their hash and provenance for reference.

## Sourcing

Each source is in the corpus for a kind of file the others do not produce, not
for volume:

| Source | Licence | What it contributes |
|---|---|---|
| **arXiv** (OAI-PMH) | the CC-licensed minority | pdfTeX/dvips/XeTeX/LuaTeX, Type1 and CFF subsets, maths-heavy vectors, long documents; sampled across years because the TeX toolchain changed a lot |
| **Zenodo** | `license.id` in the record | the widest producer spread available — Word, InDesign, Distiller, Quartz, scanners, in many languages |
| **Wikimedia Commons** | PD and CC | the stress end: scanned books and gazettes, CCITT G4 and JBIG2 bitonal images, DjVu-derived PDFs, non-Latin scripts |
| **US Federal Register** (govinfo) | public domain | iText/GPO output, digital signatures, `/AcroForm` |
| **EUR-Lex** | Decision 2011/833/EU | real, non-self-generated **PDF/A** in every official language |
| **OAPEN** | CC book licences | hundreds of pages of professionally typeset, image-rich layout |
| **Self-generated** | self-generated | the deliberate gaps — see `gen-realworld-selfmade.sh` above |

Record the license of every file in the manifest before adding it. The sourcing
tool does this automatically from each API; a hand-added file needs it filled in
by hand.
