#!/usr/bin/env python3
"""Source the real-world corpus under tests/realworld/ from public, freely
licensed collections.

The synthetic Isartor and veraPDF suites are hand-built to exercise one clause
each. This tool assembles the other corpus: documents from actual producers,
picked for variety rather than volume. It runs in four phases, each resumable:

    plan      query the source APIs, keep the freely licensed hits, write a
              candidate list. No downloads.
    fetch     download candidates into the staging directory, under a global
              byte budget and per-host rate limits.
    classify  run veraPDF over the staged files and let its verdict decide
              should-pass/ vs should-convert/. Provenance does not predict
              this -- a US Federal Register PDF is iText output and fails 1b,
              while a LibreOffice PDF/A-1b export passes -- so the split is
              measured, never guessed.
    manifest  merge the staged provenance into tests/realworld/manifest.json,
              the committed inventory. The PDF bytes stay gitignored.

Only licences that permit redistribution are accepted (CC0, CC-BY, CC-BY-SA,
US-federal public domain), so every entry stays legally fetchable from its
recorded URL by scripts/fetch-realworld-corpus.sh.

Staging lives outside the repository (${XDG_CACHE_HOME:-~/.cache}/gopdfrab-corpus)
so the working tree never carries untracked download sidecars.

Python 3 standard library only. Usage:

    scripts/source-realworld-corpus.py plan
    scripts/source-realworld-corpus.py fetch --max-bytes 3.5G
    scripts/source-realworld-corpus.py classify
    scripts/source-realworld-corpus.py manifest
"""

from __future__ import annotations

import argparse
import gzip
import hashlib
import json
import mmap
import os
import re
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET
from pathlib import Path

USER_AGENT = "gopdfrab-corpus/0.1 (+https://github.com/voidrab/gopdfrab; contact@thomas-karner.com)"

REPO = Path(__file__).resolve().parent.parent
CORPUS = REPO / "tests" / "realworld"
STAGE = Path(os.environ.get("XDG_CACHE_HOME", Path.home() / ".cache")) / "gopdfrab-corpus"
CANDIDATES = STAGE / "candidates.jsonl"
FILES = STAGE / "files"
META = STAGE / "meta"
VERAPDF = REPO / "benchmarks" / "tools" / "verapdf" / "verapdf"

# Licences that permit redistribution. A candidate whose licence does not map
# into this set is dropped at plan time and never downloaded.
FREE_LICENSES = {
    "CC0-1.0",
    "CC-BY-4.0",
    "CC-BY-3.0",
    "CC-BY-SA-4.0",
    "CC-BY-SA-3.0",
    "PD",
    "PD-USGov",
}

DEFAULT_BUDGET = 3_500_000_000
HARD_BUDGET_CAP = 5_000_000_000
DEFAULT_MAX_FILE = 150_000_000

# Per-source share of the byte budget. Sums above 1.0 on purpose: a source that
# runs dry must not strand budget the others could use.
SOURCE_SHARE = {
    "arxiv": 0.25,
    "zenodo": 0.40,
    "commons": 0.30,
    "usgov": 0.15,
    "eurlex": 0.12,
    "oapen": 0.30,
}

# Politeness delay between requests to one host, in seconds. arXiv asks for one
# request every three seconds; the rest are courtesy limits well inside the
# published quotas.
HOST_DELAY = {
    "arxiv": 3.0,
    "zenodo": 1.0,
    "commons": 1.0,
    "usgov": 0.5,
    "eurlex": 1.0,
    "oapen": 1.0,
}

_last_request: dict[str, float] = {}


def log(msg: str) -> None:
    print(msg, file=sys.stderr, flush=True)


def human(n: int) -> str:
    for unit in ("B", "KB", "MB", "GB"):
        if abs(n) < 1024 or unit == "GB":
            return f"{n:.1f}{unit}" if unit != "B" else f"{n}B"
        n /= 1024.0
    return str(n)


def parse_size(s: str) -> int:
    m = re.fullmatch(r"(?i)\s*([0-9.]+)\s*([kmgt]?)b?\s*", s)
    if not m:
        raise argparse.ArgumentTypeError(f"not a size: {s}")
    mult = {"": 1, "k": 10**3, "m": 10**6, "g": 10**9, "t": 10**12}[m.group(2).lower()]
    return int(float(m.group(1)) * mult)


# --------------------------------------------------------------------------
# HTTP


def throttle(host_key: str) -> None:
    delay = HOST_DELAY.get(host_key, 1.0)
    last = _last_request.get(host_key)
    if last is not None:
        wait = delay - (time.monotonic() - last)
        if wait > 0:
            time.sleep(wait)
    _last_request[host_key] = time.monotonic()


def request(url: str, host_key: str, accept: str | None = None, retries: int = 3):
    """Open url, returning the response object. Retries on 5xx and timeouts."""
    headers = {"User-Agent": USER_AGENT, "Accept-Encoding": "gzip"}
    if accept:
        headers["Accept"] = accept
    for attempt in range(retries + 1):
        throttle(host_key)
        req = urllib.request.Request(url, headers=headers)
        try:
            return urllib.request.urlopen(req, timeout=90)
        except urllib.error.HTTPError as e:
            # 503 with Retry-After is OAI-PMH flow control, not an error.
            if e.code == 503 and attempt < retries:
                time.sleep(float(e.headers.get("Retry-After", 20)))
                continue
            if e.code >= 500 and attempt < retries:
                time.sleep(2 ** attempt)
                continue
            raise
        except (urllib.error.URLError, TimeoutError, OSError):
            if attempt < retries:
                time.sleep(2 ** attempt)
                continue
            raise
    raise RuntimeError("unreachable")


def get_bytes(url: str, host_key: str, accept: str | None = None) -> bytes:
    with request(url, host_key, accept) as resp:
        data = resp.read()
        if resp.headers.get("Content-Encoding") == "gzip":
            data = gzip.decompress(data)
        return data


def get_json(url: str, host_key: str) -> object:
    return json.loads(get_bytes(url, host_key, accept="application/json"))


# --------------------------------------------------------------------------
# Licence normalisation


def canon_license(raw: str | None) -> str | None:
    """Map a source's licence string to a canonical id, or None if it is not
    one this corpus accepts. Deliberately conservative: an unrecognised licence
    is not a free licence."""
    if not raw:
        return None
    s = raw.strip().lower().rstrip("/")
    if "publicdomain/zero" in s or s in ("cc0", "cc0-1.0", "cc zero"):
        return "CC0-1.0"
    if "publicdomain/mark" in s or s in ("pd", "public domain", "pd-old", "pd-us", "pd-art"):
        return "PD"
    m = re.search(r"licenses/by(-sa)?(-nc)?(-nd)?/(\d)\.\d", s)
    if m:
        if m.group(2) or m.group(3):  # NC or ND: not redistributable enough
            return None
        return f"CC-BY{'-SA' if m.group(1) else ''}-{m.group(4)}.0"
    m = re.fullmatch(r"cc[- ]by([- ]sa)?([- ]nc)?([- ]nd)?[- ](\d)\.\d", s)
    if m:
        if m.group(2) or m.group(3):
            return None
        return f"CC-BY{'-SA' if m.group(1) else ''}-{m.group(4)}.0"
    if s.startswith("pd") or "public domain" in s:
        return "PD"
    return None


# --------------------------------------------------------------------------
# Candidate sources
#
# Each source yields dicts: source, id, url, license, title, note, size_hint.


def src_arxiv(limit: int):
    """arXiv, via OAI-PMH. The <license> element carries the author's choice;
    most papers are under arXiv's own non-exclusive licence, which is not
    redistributable, so only the CC-licensed minority is kept.

    Sampled across sets and years because the TeX toolchain -- and so the
    producer, the font technology and the shape of the content streams --
    changed a lot between 2010 and today."""
    base = "https://export.arxiv.org/oai2"
    windows = [
        (f"{y}-{m:02d}-01", f"{y}-{m:02d}-02")
        for y in range(2011, 2027, 3)
        for m in (3, 9)
    ]
    sets = ["cs", "math", "physics:astro-ph", "q-bio", "econ", "stat", "eess"]
    seen = 0
    for i, (frm, until) in enumerate(windows):
        for s in sets:
            if seen >= limit:
                return
            q = urllib.parse.urlencode(
                {"verb": "ListRecords", "metadataPrefix": "arXiv", "set": s, "from": frm, "until": until}
            )
            try:
                data = get_bytes(f"{base}?{q}", "arxiv")
            except Exception as e:  # a dead set or window costs coverage, not the run
                log(f"  arxiv {s} {frm}: {e}")
                continue
            try:
                root = ET.fromstring(data)
            except ET.ParseError as e:
                log(f"  arxiv {s} {frm}: bad XML: {e}")
                continue
            ns = "{http://arxiv.org/OAI/arXiv/}"
            for rec in root.iter(f"{ns}arXiv"):
                lic = canon_license(rec.findtext(f"{ns}license"))
                if lic is None:
                    continue
                aid = (rec.findtext(f"{ns}id") or "").strip()
                if not aid:
                    continue
                created = (rec.findtext(f"{ns}created") or "").strip()
                title = " ".join((rec.findtext(f"{ns}title") or "").split())
                yield {
                    "source": "arxiv",
                    "id": aid,
                    "url": f"https://arxiv.org/pdf/{aid}",
                    "license": lic,
                    "title": title[:200],
                    "note": f"arXiv {aid}, submitted {created}, set {s}",
                    "size_hint": 0,
                }
                seen += 1
                if seen >= limit:
                    return


def src_zenodo(limit: int):
    """Zenodo. The widest producer spread of any source here: theses, reports,
    posters, slide decks and scans, deposited as whatever tool the author had.
    The licence id is in the record, so the filter is exact."""
    # Facet parameters rather than a q= expression, and size 25: an
    # unauthenticated request is capped there and 400s above it.
    kinds = [
        "publication", "presentation", "poster", "report", "thesis",
        "lesson", "image", "other", "software", "dataset",
    ]
    seen = 0
    for q in kinds:
        for page in range(1, 25):
            if seen >= limit:
                return
            params = urllib.parse.urlencode(
                {
                    "resource_type": q, "file_type": "pdf", "size": 25, "page": page,
                    "access_status": "open", "sort": "newest",
                }
            )
            try:
                doc = get_json(f"https://zenodo.org/api/records?{params}", "zenodo")
            except Exception as e:
                log(f"  zenodo {q} p{page}: {e}")
                break
            hits = doc.get("hits", {}).get("hits", [])
            if not hits:
                break
            for rec in hits:
                lic = canon_license((rec.get("metadata", {}).get("license") or {}).get("id"))
                if lic is None:
                    continue
                for f in rec.get("files", []) or []:
                    key = f.get("key", "")
                    if not key.lower().endswith(".pdf"):
                        continue
                    link = (f.get("links") or {}).get("self")
                    if not link:
                        continue
                    yield {
                        "source": "zenodo",
                        "id": f"{rec.get('id')}-{key}",
                        "url": link,
                        "license": lic,
                        "title": " ".join(str(rec.get("title", "")).split())[:200],
                        "note": f"Zenodo record {rec.get('id')}",
                        "size_hint": int(f.get("size") or 0),
                    }
                    seen += 1
                    if seen >= limit:
                        return


def src_commons(limit: int):
    """Wikimedia Commons. The stress end of the corpus: scanned books and
    gazettes, CCITT G4 and JBIG2 bitonal images, DjVu-derived PDFs, and
    non-Latin scripts that no synthetic fixture covers."""
    terms = [
        "scan", "report", "manuscript", "gazette", "annual report", "brochure",
        "map", "sheet music", "book", "letter", "newspaper", "catalogue",
        "handbook", "poster", "atlas", "census", "bulletin",
    ]
    api = "https://commons.wikimedia.org/w/api.php"
    seen = 0
    for term in terms:
        for offset in (0, 50, 100):
            if seen >= limit:
                return
            params = urllib.parse.urlencode(
                {
                    "action": "query", "format": "json", "generator": "search",
                    "gsrsearch": f"filetype:pdf {term}", "gsrnamespace": 6,
                    "gsrlimit": 50, "gsroffset": offset,
                    "prop": "imageinfo", "iiprop": "url|size|mime|extmetadata",
                }
            )
            try:
                doc = get_json(f"{api}?{params}", "commons")
            except Exception as e:
                log(f"  commons {term}+{offset}: {e}")
                break
            pages = (doc.get("query") or {}).get("pages") or {}
            if not pages:
                break
            for page in pages.values():
                info = (page.get("imageinfo") or [{}])[0]
                if info.get("mime") != "application/pdf":
                    continue
                ext = info.get("extmetadata") or {}
                raw = (ext.get("License") or {}).get("value") or (
                    ext.get("LicenseShortName") or {}
                ).get("value")
                lic = canon_license(raw)
                if lic is None:
                    continue
                url = info.get("url")
                if not url:
                    continue
                yield {
                    "source": "commons",
                    "id": str(page.get("pageid")),
                    "url": url,
                    "license": lic,
                    "title": " ".join(str(page.get("title", "")).split())[:200],
                    "note": f"Wikimedia Commons {page.get('title')}",
                    "size_hint": int(info.get("size") or 0),
                }
                seen += 1
                if seen >= limit:
                    return


def src_usgov(limit: int):
    """US Federal Register, served from govinfo. Work of the US government, so
    public domain. Uniform iText/GPO output with digital signatures -- a
    producer no other source here provides."""
    seen = 0
    for year in range(2000, 2027, 2):
        for page in (1, 2):
            if seen >= limit:
                return
            params = urllib.parse.urlencode(
                {
                    "per_page": 100, "page": page, "order": "newest",
                    "conditions[publication_date][year]": year,
                    "fields[]": ["pdf_url", "document_number", "title", "publication_date"],
                },
                doseq=True,
            )
            try:
                doc = get_json(f"https://www.federalregister.gov/api/v1/documents.json?{params}", "usgov")
            except Exception as e:
                log(f"  usgov {year} p{page}: {e}")
                break
            results = doc.get("results") or []
            if not results:
                break
            for r in results:
                url = r.get("pdf_url")
                if not url:
                    continue
                yield {
                    "source": "usgov",
                    "id": str(r.get("document_number")),
                    "url": url,
                    "license": "PD-USGov",
                    "title": " ".join(str(r.get("title", "")).split())[:200],
                    "note": f"US Federal Register {r.get('document_number')}, {r.get('publication_date')}",
                    "size_hint": 0,
                }
                seen += 1
                if seen >= limit:
                    return


def src_eurlex(limit: int):
    """EUR-Lex. Legislation is published as genuine PDF/A in every official
    language, which would make this the strongest natural source of should-pass
    files nobody in this repo generated. Reuse is permitted under Commission
    Decision 2011/833/EU (CC-BY-4.0 equivalent).

    Off by default: EUR-Lex answers automated clients with an HTTP 202
    interstitial rather than the document, and the right response to a site
    signalling that is to stop, not to work around it. Left here, opt-in with
    --source eurlex, in case the policy changes.

    CELEX numbers are enumerated rather than searched: gaps just 404."""
    langs = ["EN", "DE", "FR", "ES", "IT", "PL", "EL", "BG", "FI", "HU", "CS", "SV"]
    seen = 0
    misses = 0
    for year in range(2014, 2026):
        for kind in ("R", "L", "D"):
            for num in range(1, 40):
                if seen >= limit or misses > 400:
                    return
                celex = f"3{year}{kind}{num:04d}"
                lang = langs[(year + num) % len(langs)]
                url = f"https://eur-lex.europa.eu/legal-content/{lang}/TXT/PDF/?uri=CELEX:{celex}"
                try:
                    with request(url, "eurlex") as resp:
                        ctype = resp.headers.get("Content-Type", "")
                        size = int(resp.headers.get("Content-Length") or 0)
                except Exception:
                    misses += 1
                    continue
                if "application/pdf" not in ctype:
                    misses += 1
                    continue
                yield {
                    "source": "eurlex",
                    "id": f"{celex}-{lang}",
                    "url": url,
                    "license": "CC-BY-4.0",
                    "title": f"CELEX {celex} ({lang})",
                    "note": f"EUR-Lex CELEX {celex}, language {lang}; reuse under Decision 2011/833/EU",
                    "size_hint": size,
                }
                seen += 1


def src_oapen(limit: int):
    """OAPEN's open-access book library. Long, image-rich, professionally
    typeset documents -- InDesign and Distiller output running to hundreds of
    pages, which nothing else here supplies."""
    # filtered-items rather than search: most OAPEN records carry no rights
    # metadata at all, so filtering server-side on dc.rights.uri is the only way
    # to get a usable yield.
    seen = 0
    for offset in range(0, 2000, 100):
        if seen >= limit:
            return
        params = urllib.parse.urlencode(
            {
                "query_field[]": "dc.rights.uri", "query_op[]": "contains",
                "query_val[]": "creativecommons", "expand": "bitstreams,metadata",
                "limit": 100, "offset": offset,
            }
        )
        try:
            doc = get_json(f"https://library.oapen.org/rest/filtered-items?{params}", "oapen")
        except Exception as e:
            log(f"  oapen offset {offset}: {e}")
            break
        items = doc.get("items") if isinstance(doc, dict) else doc
        if not items:
            break
        for item in items:
            lic = None
            for md in item.get("metadata") or []:
                if md.get("key") in ("dc.rights.uri", "dc.rights"):
                    lic = canon_license(md.get("value")) or lic
            if lic is None:
                continue
            for bs in item.get("bitstreams") or []:
                # Some records type the book PDF as application/octet-stream, so
                # the file name is the reliable signal.
                name = (bs.get("name") or "").lower()
                if bs.get("mimeType") != "application/pdf" and not name.endswith(".pdf"):
                    continue
                uuid = bs.get("uuid")
                if not uuid:
                    continue
                yield {
                    "source": "oapen",
                    "id": uuid,
                    "url": f"https://library.oapen.org/rest/bitstreams/{uuid}/retrieve",
                    "license": lic,
                    "title": " ".join(str(item.get("name", "")).split())[:200],
                    "note": f"OAPEN {item.get('handle')}",
                    "size_hint": int(bs.get("sizeBytes") or 0),
                }
                seen += 1
                if seen >= limit:
                    return


# eurlex is deliberately absent from DEFAULT_SOURCES; see src_eurlex.
SOURCES = {
    "arxiv": src_arxiv,
    "zenodo": src_zenodo,
    "commons": src_commons,
    "usgov": src_usgov,
    "eurlex": src_eurlex,
    "oapen": src_oapen,
}

DEFAULT_SOURCES = [s for s in SOURCES if s != "eurlex"]


# --------------------------------------------------------------------------
# Phases


def safe_name(source: str, ident: str) -> str:
    stem = re.sub(r"[^A-Za-z0-9._-]+", "-", ident).strip("-")[:110] or "file"
    return f"{source}-{stem}.pdf"


def cmd_plan(args) -> int:
    STAGE.mkdir(parents=True, exist_ok=True)
    wanted = args.source or DEFAULT_SOURCES
    existing: dict[str, dict] = {}
    if CANDIDATES.exists() and not args.refresh:
        for line in CANDIDATES.read_text().splitlines():
            if line.strip():
                c = json.loads(line)
                existing[c["url"]] = c
    added = 0
    for name in wanted:
        log(f"planning {name} ...")
        before = added
        # A source is given a time budget rather than being run to exhaustion.
        # arXiv's OAI endpoint in particular flow-controls hard and can stall
        # for many minutes; a slow source must cost coverage, not the run.
        deadline = time.monotonic() + args.time_budget
        try:
            for cand in SOURCES[name](args.per_source):
                if cand["url"] not in existing:
                    existing[cand["url"]] = cand
                    added += 1
                if time.monotonic() > deadline:
                    log(f"  {name}: time budget reached")
                    break
        except Exception as e:
            log(f"  {name}: aborted: {e}")
        log(f"  {name}: {added - before} new candidates")
        # Write after each source, so an interrupted plan keeps what it found.
        with CANDIDATES.open("w") as fh:
            for c in sorted(existing.values(), key=lambda c: (c["source"], c["id"])):
                fh.write(json.dumps(c, ensure_ascii=False) + "\n")
    with CANDIDATES.open("w") as fh:
        for c in sorted(existing.values(), key=lambda c: (c["source"], c["id"])):
            fh.write(json.dumps(c, ensure_ascii=False) + "\n")
    by_source: dict[str, int] = {}
    for c in existing.values():
        by_source[c["source"]] = by_source.get(c["source"], 0) + 1
    log(f"candidates: {len(existing)} total {by_source}")
    return 0


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def corpus_bytes() -> int:
    """Bytes the corpus already holds, staged plus classified.

    The budget has to count both: classify empties the staging directory, so a
    second fetch that looked only at staging would start from zero and download
    a whole budget again on top of the corpus already on disk."""
    total = sum(p.stat().st_size for p in FILES.rglob("*.pdf"))
    for sub in ("should-pass", "should-convert"):
        total += sum(p.stat().st_size for p in (CORPUS / sub).rglob("*.pdf"))
    return total


def staged_bytes() -> int:
    return sum(p.stat().st_size for p in FILES.rglob("*.pdf"))


def known_hashes() -> dict[str, str]:
    """sha256 -> where it already lives, over staging and the live corpus."""
    out: dict[str, str] = {}
    for meta in META.glob("*.json"):
        out[meta.stem] = json.loads(meta.read_text()).get("staged", "staging")
    for sub in ("should-pass", "should-convert"):
        for pdf in (CORPUS / sub).rglob("*.pdf"):
            out.setdefault(sha256_file(pdf), str(pdf.relative_to(CORPUS)))
    return out


def looks_like_pdf(path: Path) -> bool:
    with path.open("rb") as fh:
        return b"%PDF-" in fh.read(1024)


def cmd_fetch(args) -> int:
    if not CANDIDATES.exists():
        log("no candidate list; run 'plan' first")
        return 1
    budget = min(args.max_bytes, HARD_BUDGET_CAP)
    FILES.mkdir(parents=True, exist_ok=True)
    META.mkdir(parents=True, exist_ok=True)

    cands = [json.loads(l) for l in CANDIDATES.read_text().splitlines() if l.strip()]
    if args.source:
        cands = [c for c in cands if c["source"] in args.source]
    # Interleave sources so an early stop still leaves a balanced corpus.
    by_source: dict[str, list] = {}
    for c in cands:
        by_source.setdefault(c["source"], []).append(c)
    ordered: list[dict] = []
    for i in range(max((len(v) for v in by_source.values()), default=0)):
        for name in sorted(by_source):
            if i < len(by_source[name]):
                ordered.append(by_source[name][i])

    total = corpus_bytes()
    per_source_used = {s: 0 for s in by_source}
    seen = known_hashes()
    log(f"budget {human(budget)}, corpus already holds {human(total)}, {len(seen)} known hashes")

    got = dup = skip = fail = 0
    for cand in ordered:
        if total >= budget:
            log("budget reached")
            break
        source = cand["source"]
        cap = int(budget * SOURCE_SHARE.get(source, 0.2))
        if per_source_used[source] >= cap:
            continue
        hint = cand.get("size_hint") or 0
        if hint and hint > args.max_file_bytes:
            skip += 1
            continue
        dest = FILES / source / safe_name(source, cand["id"])
        if dest.exists():
            per_source_used[source] += dest.stat().st_size
            continue
        dest.parent.mkdir(parents=True, exist_ok=True)
        tmp = dest.with_suffix(".part")
        try:
            with request(cand["url"], source) as resp:
                ctype = resp.headers.get("Content-Type", "")
                if "pdf" not in ctype and "octet-stream" not in ctype:
                    skip += 1
                    continue
                written = 0
                with tmp.open("wb") as out:
                    while True:
                        chunk = resp.read(1 << 20)
                        if not chunk:
                            break
                        written += len(chunk)
                        if written > args.max_file_bytes:
                            raise ValueError(f"over max-file-bytes ({human(written)})")
                        out.write(chunk)
        except Exception as e:
            fail += 1
            tmp.unlink(missing_ok=True)
            log(f"  fail {cand['url']}: {e}")
            continue
        if not looks_like_pdf(tmp):
            tmp.unlink(missing_ok=True)
            skip += 1
            continue
        digest = sha256_file(tmp)
        if digest in seen:
            tmp.unlink(missing_ok=True)
            dup += 1
            continue
        tmp.rename(dest)
        seen[digest] = str(dest)
        size = dest.stat().st_size
        total += size
        per_source_used[source] += size
        got += 1
        (META / f"{digest}.json").write_text(
            json.dumps(
                {
                    "sha256": digest, "url": cand["url"], "license": cand["license"],
                    "source": source, "title": cand.get("title", ""),
                    "note": cand.get("note", ""), "staged": str(dest.relative_to(STAGE)),
                    "bytes": size,
                },
                ensure_ascii=False, indent=1,
            )
        )
        if got % 25 == 0:
            log(f"  {got} fetched, {human(total)} of {human(budget)}")
    log(f"fetched {got}, duplicates {dup}, skipped {skip}, failed {fail}; staged {human(total)}")
    return 0


def verapdf_verdicts(paths: list[Path]) -> dict[str, bool]:
    """Run veraPDF once over a batch and return path -> is PDF/A-1b. A file the
    reference verifier cannot even parse counts as not-1b, which is the honest
    reading and puts it in should-convert where it is more interesting."""
    verdicts: dict[str, bool] = {}
    batch = 40
    for i in range(0, len(paths), batch):
        chunk = [str(p) for p in paths[i : i + batch]]
        try:
            out = subprocess.run(
                [str(VERAPDF), "--flavour", "1b", "--format", "text", *chunk],
                capture_output=True, text=True, timeout=1800,
            ).stdout
        except subprocess.TimeoutExpired:
            log(f"  veraPDF timed out on batch {i // batch}; marking as not-1b")
            for p in chunk:
                verdicts[p] = False
            continue
        for line in out.splitlines():
            line = line.strip()
            if line.startswith("PASS ") or line.startswith("FAIL "):
                ok = line.startswith("PASS ")
                rest = line[5:].rsplit(" ", 1)[0].strip()
                verdicts[rest] = ok
        log(f"  veraPDF: {min(i + batch, len(paths))}/{len(paths)}")
    return verdicts


def cmd_classify(args) -> int:
    if not VERAPDF.exists():
        log(f"veraPDF not found at {VERAPDF} (run scripts/install-verapdf.sh)")
        return 1
    staged = sorted(FILES.rglob("*.pdf"))
    if not staged:
        log("nothing staged; run 'fetch' first")
        return 1
    log(f"classifying {len(staged)} files with veraPDF")
    verdicts = verapdf_verdicts(staged)
    moved = {"should-pass": 0, "should-convert": 0}
    unknown = 0
    for path in staged:
        v = verdicts.get(str(path))
        if v is None:
            unknown += 1
            v = False
        sub = "should-pass" if v else "should-convert"
        dest = CORPUS / sub / path.parent.name / path.name
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.move(str(path), str(dest))
        moved[sub] += 1
    log(f"classified: {moved['should-pass']} should-pass, {moved['should-convert']} should-convert"
        + (f" ({unknown} with no veraPDF verdict, treated as not-1b)" if unknown else ""))
    return 0


PRODUCER_RE = re.compile(rb"/Producer\s*(?:\(((?:[^()\\]|\\.)*)\)|<([0-9A-Fa-f\s]+)>)")
XMP_PRODUCER_RE = re.compile(rb"<pdf:Producer>([^<]{1,200})</pdf:Producer>")


def decode_pdf_text(raw: bytes) -> str:
    """Decode a PDF text string. UTF-16BE when it carries the BOM, otherwise
    PDFDocEncoding -- which agrees with Latin-1 over the range producers
    actually use, so 'iText(R)' survives instead of becoming U+FFFD."""
    if raw.startswith(b"\xfe\xff"):
        return raw[2:].decode("utf-16-be", "replace")
    try:
        return raw.decode("utf-8")
    except UnicodeDecodeError:
        return raw.decode("latin-1", "replace")


def read_producer(path: Path) -> str:
    """Best-effort /Producer for the manifest's provenance column. Never fatal:
    an unreadable producer is a blank column, not a failed run."""
    try:
        with path.open("rb") as fh:
            with mmap.mmap(fh.fileno(), 0, access=mmap.ACCESS_READ) as mm:
                m = PRODUCER_RE.search(mm)
                if m and m.group(2):
                    raw = bytes.fromhex(re.sub(rb"\s", b"", m.group(2)).decode("ascii", "ignore"))
                elif m:
                    raw = re.sub(rb"\\([()\\])", rb"\1", m.group(1))
                else:
                    m = XMP_PRODUCER_RE.search(mm)
                    if not m:
                        return ""
                    raw = m.group(1)
                return " ".join(decode_pdf_text(raw).split())[:120]
    except (ValueError, OSError):
        return ""


def cmd_manifest(args) -> int:
    manifest_path = CORPUS / "manifest.json"
    entries = json.loads(manifest_path.read_text()) if manifest_path.exists() else []
    by_path = {e["path"]: e for e in entries}

    present = sorted(
        p for sub in ("should-pass", "should-convert") for p in (CORPUS / sub).rglob("*.pdf")
    )
    log(f"hashing {len(present)} corpus files")
    out: dict[str, dict] = {}
    hashes: dict[str, str] = {}
    stubs = 0
    for i, pdf in enumerate(present, 1):
        rel = pdf.relative_to(CORPUS).as_posix()
        digest = sha256_file(pdf)
        hashes[digest] = rel
        meta_file = META / f"{digest}.json"
        prev = by_path.get(rel, {})
        if meta_file.exists():
            meta = json.loads(meta_file.read_text())
            note = meta.get("note", "")
            title = meta.get("title", "")
            if title and title not in note:
                note = f"{note}; {title}" if note else title
            entry = {
                "path": rel, "url": meta.get("url", ""), "sha256": digest,
                "license": meta.get("license", "TODO"),
                "producer": read_producer(pdf) or meta.get("source", ""),
                "note": note[:300],
            }
        else:
            # Hand-dropped or self-generated: keep whatever provenance is already
            # recorded and stub only what is genuinely unknown.
            entry = {
                "path": rel, "url": prev.get("url", ""), "sha256": digest,
                "license": prev.get("license") or "TODO",
                "producer": prev.get("producer") or read_producer(pdf),
                "note": prev.get("note", ""),
            }
            if entry["license"] == "TODO":
                stubs += 1
        out[rel] = entry
        if i % 250 == 0:
            log(f"  {i}/{len(present)}")

    # Keep entries for files that live on another machine, but drop ones this
    # run reclassified into a different directory (same hash, new path).
    for e in entries:
        if e["path"] in out:
            continue
        if hashes.get(e.get("sha256", "")) is not None:
            continue
        out[e["path"]] = e

    merged = sorted(out.values(), key=lambda e: e["path"])
    manifest_path.write_text(json.dumps(merged, ensure_ascii=False, indent=2) + "\n")
    todo = sum(1 for e in merged if e["license"] in ("", "TODO"))
    log(f"manifest: {len(merged)} entries, {todo} need a licence ({stubs} new stubs)")
    return 0 if todo == 0 else 2


def cmd_status(args) -> int:
    n_cand = sum(1 for _ in CANDIDATES.open()) if CANDIDATES.exists() else 0
    log(f"candidates:  {n_cand}")
    log(f"staged:      {len(list(FILES.rglob('*.pdf')))} files, {human(staged_bytes())}")
    for sub in ("should-pass", "should-convert"):
        files = list((CORPUS / sub).rglob("*.pdf"))
        size = sum(f.stat().st_size for f in files)
        log(f"{sub + ':':13}{len(files)} files, {human(size)}")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("plan", help="query the sources and write the candidate list")
    p.add_argument("--source", action="append", choices=list(SOURCES))
    p.add_argument("--per-source", type=int, default=1200, help="candidate cap per source")
    p.add_argument("--time-budget", type=float, default=900, help="seconds to spend per source")
    p.add_argument("--refresh", action="store_true", help="discard the existing candidate list")
    p.set_defaults(fn=cmd_plan)

    p = sub.add_parser("fetch", help="download candidates into staging")
    p.add_argument("--max-bytes", type=parse_size, default=DEFAULT_BUDGET)
    p.add_argument("--max-file-bytes", type=parse_size, default=DEFAULT_MAX_FILE)
    p.add_argument("--source", action="append", choices=list(SOURCES))
    p.set_defaults(fn=cmd_fetch)

    p = sub.add_parser("classify", help="split staged files by veraPDF verdict")
    p.set_defaults(fn=cmd_classify)

    p = sub.add_parser("manifest", help="merge provenance into tests/realworld/manifest.json")
    p.set_defaults(fn=cmd_manifest)

    p = sub.add_parser("status", help="report candidate, staging and corpus counts")
    p.set_defaults(fn=cmd_status)

    args = ap.parse_args()
    return args.fn(args)


if __name__ == "__main__":
    sys.exit(main())
