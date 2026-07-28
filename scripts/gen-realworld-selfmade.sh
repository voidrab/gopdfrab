#!/usr/bin/env bash
# Generate the self-generated half of the real-world corpus: one document per
# producer/feature combination that the downloaded sources do not reliably
# supply.
#
# The public collections give breadth of producer but no control over what is
# inside the files. This fills the deliberate gaps: PDF/A-1b and -2b exports,
# tagged output, encryption at every revision, object streams (only one file in
# the whole committed corpus uses them), CCITT and DCT image codecs, CJK/Arabic
# CID fonts, and a scanned page carrying an invisible OCR text layer -- the
# shape that made the rasterizer print hidden text before item 6 fixed it.
#
# Output goes into the same staging directory the sourcing tool uses, with a
# metadata sidecar per file recording "self-generated" provenance, so
# `source-realworld-corpus.py classify` and `... manifest` handle these exactly
# like downloaded files.
#
# Requires: soffice, gs, qpdf. Uses tesseract, pdftoppm and ImageMagick when
# present. Every generator is optional: one that fails logs and is skipped.
#
# Usage: scripts/gen-realworld-selfmade.sh
set -uo pipefail

stage="${XDG_CACHE_HOME:-$HOME/.cache}/gopdfrab-corpus"
out="$stage/files/selfmade"
meta="$stage/meta"
mkdir -p "$out" "$meta"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

made=0
skipped=0

# record <file> <note> -- move a generated PDF into staging and write its
# provenance sidecar, keyed by hash exactly as a downloaded file's is.
record() {
  local src=$1 note=$2 name sum
  if [ ! -s "$src" ]; then
    echo "  skip: $2 (nothing produced)" >&2
    skipped=$((skipped + 1))
    return
  fi
  if ! head -c 1024 "$src" | grep -aq '%PDF-'; then
    echo "  skip: $2 (not a PDF)" >&2
    skipped=$((skipped + 1))
    return
  fi
  name="selfmade-$(basename "$src")"
  sum=$(sha256sum "$src" | cut -d' ' -f1)
  cp "$src" "$out/$name"
  jq -n --arg sum "$sum" --arg note "$note" --arg staged "files/selfmade/$name" \
        --arg bytes "$(stat -c%s "$src")" '{
    sha256: $sum, url: "", license: "self-generated", source: "selfmade",
    title: $note, note: $note, staged: $staged, bytes: ($bytes | tonumber)
  }' >"$meta/$sum.json"
  made=$((made + 1))
}

# try <label> <command...> -- run a generator, reporting rather than aborting.
try() {
  local label=$1
  shift
  if ! "$@" >>"$work/log" 2>&1; then
    echo "  skip: $label (generator failed; see $work/log)" >&2
    skipped=$((skipped + 1))
    return 1
  fi
}

echo "generating source documents ..."

cat >"$work/plain.txt" <<'EOF'
Real-world corpus: self-generated sample

This document exists to exercise a producer, not to be read. It carries a few
paragraphs of ordinary body text so the exported PDF contains a genuine
embedded font subset, a realistic content stream, and more than one page when
the exporter paginates it.

Verification is a claim about a file, and a claim is only as good as the files
it has been tested against.
EOF

# Flat ODF: one file, no zip, and it can carry a table, colour fills, and runs
# in scripts whose fonts become CID-keyed in the export.
cat >"$work/rich.fodt" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<office:document xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
 xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0"
 xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"
 xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0"
 xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0"
 office:version="1.3" office:mimetype="application/vnd.oasis.opendocument.text">
 <office:automatic-styles>
  <style:style style:name="H" style:family="paragraph">
   <style:text-properties fo:font-size="20pt" fo:color="#1a5fb4" fo:font-weight="bold"/>
  </style:style>
  <style:style style:name="CJK" style:family="paragraph">
   <style:text-properties style:font-name-asian="Noto Sans CJK TC" style:font-size-asian="14pt"/>
  </style:style>
 </office:automatic-styles>
 <office:body><office:text>
  <text:p text:style-name="H">Multilingual sample</text:p>
  <text:p>Latin: the quick brown fox jumps over the lazy dog.</text:p>
  <text:p>Cyrillic: Съешь же ещё этих мягких французских булок.</text:p>
  <text:p>Greek: Ξεσκεπάζω την ψυχοφθόρα βδελυγμία.</text:p>
  <text:p text:style-name="CJK">Chinese: 中文字型嵌入測試，這是一段範例文字。</text:p>
  <text:p text:style-name="CJK">Japanese: 日本語のフォント埋め込みテストです。</text:p>
  <text:p>Arabic: نص عربي لاختبار الخطوط ثنائية الاتجاه.</text:p>
  <table:table table:name="T1">
   <table:table-column table:number-columns-repeated="3"/>
   <table:table-row>
    <table:table-cell><text:p>Clause</text:p></table:table-cell>
    <table:table-cell><text:p>Subject</text:p></table:table-cell>
    <table:table-cell><text:p>Level</text:p></table:table-cell>
   </table:table-row>
   <table:table-row>
    <table:table-cell><text:p>6.3</text:p></table:table-cell>
    <table:table-cell><text:p>Fonts</text:p></table:table-cell>
    <table:table-cell><text:p>1b</text:p></table:table-cell>
   </table:table-row>
  </table:table>
 </office:text></office:body>
</office:document>
EOF

printf 'clause,subject,files\n6.1,structure,204\n6.2,graphics,263\n6.3,fonts,306\n6.4,transparency,12\n' >"$work/sheet.csv"

cat >"$work/page.html" <<'EOF'
<html><head><meta charset="utf-8"><title>HTML source</title></head>
<body><h1 style="color:#c01c28">Heading</h1>
<p>An HTML original, exported through a word processor: a different content
stream shape again, with colour set in DeviceRGB.</p>
<table border="1"><tr><td>a</td><td>b</td></tr></table></body></html>
EOF

# ---------------------------------------------------------------- LibreOffice
echo "LibreOffice exports ..."
lo() { # lo <label> <input> <filter-json> <suffix>
  local label=$1 input=$2 json=$3 suffix=$4 base
  base=$(basename "${input%.*}")
  rm -rf "$work/lo-$suffix"
  mkdir -p "$work/lo-$suffix"
  if try "$label" soffice --headless --norestore \
      --convert-to "pdf:writer_pdf_Export:$json" --outdir "$work/lo-$suffix" "$input"; then
    mv "$work/lo-$suffix/$base.pdf" "$work/lo-$suffix-$base.pdf" 2>/dev/null &&
      record "$work/lo-$suffix-$base.pdf" "$label"
  fi
}

lo "LibreOffice Writer, default export" "$work/plain.txt" '{}' plain
lo "LibreOffice Writer, PDF/A-1b export" "$work/plain.txt" \
   '{"SelectPdfVersion":{"type":"long","value":1}}' pdfa1b
lo "LibreOffice Writer, PDF/A-2b export" "$work/plain.txt" \
   '{"SelectPdfVersion":{"type":"long","value":2}}' pdfa2b
lo "LibreOffice Writer, PDF/A-3b export" "$work/plain.txt" \
   '{"SelectPdfVersion":{"type":"long","value":3}}' pdfa3b
lo "LibreOffice Writer, tagged PDF" "$work/plain.txt" \
   '{"UseTaggedPDF":{"type":"boolean","value":true}}' tagged
lo "LibreOffice Writer, multilingual CID fonts" "$work/rich.fodt" '{}' rich
lo "LibreOffice Writer, multilingual PDF/A-1b" "$work/rich.fodt" \
   '{"SelectPdfVersion":{"type":"long","value":1}}' richpdfa
lo "LibreOffice Writer, AES-encrypted export" "$work/plain.txt" \
   '{"EncryptFile":{"type":"boolean","value":true},"DocumentOpenPassword":{"type":"string","value":""}}' enc
lo "LibreOffice Writer, JPEG-compressed images" "$work/rich.fodt" \
   '{"UseLosslessCompression":{"type":"boolean","value":false},"Quality":{"type":"long","value":45}}' jpeg
lo "LibreOffice Web, HTML original" "$work/page.html" '{}' html

rm -rf "$work/lo-calc"
mkdir -p "$work/lo-calc"
if try "LibreOffice Calc export" soffice --headless --norestore --convert-to pdf \
    --outdir "$work/lo-calc" "$work/sheet.csv"; then
  mv "$work/lo-calc/sheet.pdf" "$work/lo-calc-sheet.pdf" 2>/dev/null &&
    record "$work/lo-calc-sheet.pdf" "LibreOffice Calc, spreadsheet export"
fi

# The rest of the matrix rewrites a LibreOffice export, so one has to exist.
base=$(ls "$out"/selfmade-lo-rich-rich.pdf "$out"/selfmade-lo-plain-plain.pdf 2>/dev/null | head -1)
if [ -z "$base" ]; then
  echo "no LibreOffice export produced; skipping the Ghostscript/qpdf matrix" >&2
  echo "generated $made files, skipped $skipped"
  exit 0
fi
cp "$base" "$work/base.pdf"

# ---------------------------------------------------------------- Ghostscript
echo "Ghostscript conversions ..."
gsconv() { # gsconv <label> <suffix> <extra gs args...>
  local label=$1 suffix=$2
  shift 2
  if try "$label" gs -dBATCH -dNOPAUSE -dQUIET -sDEVICE=pdfwrite \
      -sOutputFile="$work/gs-$suffix.pdf" "$@" "$work/base.pdf"; then
    record "$work/gs-$suffix.pdf" "$label"
  fi
}

gsconv "Ghostscript pdfwrite, PDF 1.3" 13 -dCompatibilityLevel=1.3
gsconv "Ghostscript pdfwrite, PDF 1.4" 14 -dCompatibilityLevel=1.4
gsconv "Ghostscript pdfwrite, PDF 1.7" 17 -dCompatibilityLevel=1.7
gsconv "Ghostscript pdfwrite, DCT-encoded images" dct \
  -dAutoFilterColorImages=false -sColorImageFilter=DCTEncode -dColorImageResolution=72 \
  -dDownsampleColorImages=true
gsconv "Ghostscript pdfwrite, CCITT G4 mono images" ccitt \
  -dAutoFilterMonoImages=false -sMonoImageFilter=CCITTFaxEncode

# PDF/A needs a definition file naming an output-intent ICC profile.
gslib=/usr/share/ghostscript/lib/PDFA_def.ps
icc=$(ls /usr/share/ghostscript/iccprofiles/default_rgb.icc \
         /usr/share/ghostscript/iccprofiles/srgb.icc 2>/dev/null | head -1)
if [ -f "$gslib" ] && [ -n "$icc" ]; then
  sed "s|%.*ICCProfile (srgb.icc)|/ICCProfile ($icc)|; s|^/ICCProfile.*|/ICCProfile ($icc)|" \
    "$gslib" >"$work/pdfa_def.ps"
  grep -q "$icc" "$work/pdfa_def.ps" || echo "/ICCProfile ($icc) def" >>"$work/pdfa_def.ps"
  for part in 1 2; do
    if try "Ghostscript PDF/A-${part}b" gs -dBATCH -dNOPAUSE -dQUIET -sDEVICE=pdfwrite \
        -dPDFA=$part -dPDFACompatibilityPolicy=1 \
        -sColorConversionStrategy=RGB -dEmbedAllFonts=true \
        -sOutputFile="$work/gs-pdfa$part.pdf" "$work/pdfa_def.ps" "$work/base.pdf"; then
      record "$work/gs-pdfa$part.pdf" "Ghostscript pdfwrite, PDF/A-${part}b"
    fi
  done
else
  echo "  skip: Ghostscript PDF/A (no PDFA_def.ps or ICC profile)" >&2
fi

# ----------------------------------------------------------------------- qpdf
echo "qpdf rewrites ..."
qp() { # qp <label> <suffix> <qpdf args...>
  local label=$1 suffix=$2
  shift 2
  if try "$label" qpdf "$@" "$work/base.pdf" "$work/qpdf-$suffix.pdf"; then
    record "$work/qpdf-$suffix.pdf" "$label"
  fi
}

qp "qpdf, object streams and a cross-reference stream" objstm --object-streams=generate
qp "qpdf, cross-reference table, PDF 1.4" xreftable --object-streams=disable --force-version=1.4
qp "qpdf, linearized" linearized --linearize
qp "qpdf, uncompressed QDF form" qdf --qdf --object-streams=disable
# RC4 needs --allow-weak-crypto on modern qpdf. It is still what the installed
# base of PDFs is encrypted with, and gopdfrab decrypts it, so it is tested.
qp "qpdf, RC4 40-bit encryption, empty passwords" rc440 --allow-weak-crypto --encrypt "" "" 40 --
qp "qpdf, RC4 128-bit encryption, empty passwords" rc4128 --allow-weak-crypto --encrypt "" "" 128 --
qp "qpdf, AES-128 encryption, empty passwords" aes128 --encrypt "" "" 128 --use-aes=y --
qp "qpdf, AES-256 encryption, empty passwords" aes256 --encrypt "" "" 256 --
qp "qpdf, AES-256 with cleartext metadata" aes256meta --encrypt "" "" 256 --cleartext-metadata --

# ------------------------------------------------------- scans and OCR layers
echo "scanned-page simulations ..."
if command -v pdftoppm >/dev/null; then
  try "page raster" pdftoppm -r 150 -png -f 1 -l 1 "$work/base.pdf" "$work/scan"
  scan=$(ls "$work"/scan*.png 2>/dev/null | head -1)
  if [ -n "${scan:-}" ]; then
    if command -v tesseract >/dev/null; then
      # A scanned page with an invisible OCR text layer (Tr 3): the exact shape
      # whose hidden text the rasterizer used to print.
      if try "tesseract OCR PDF" tesseract "$scan" "$work/ocr" pdf; then
        record "$work/ocr.pdf" "tesseract OCR, scanned page with an invisible text layer"
      fi
    fi
    if command -v convert >/dev/null; then
      if try "ImageMagick CCITT G4 scan" convert "$scan" -threshold 60% -monochrome \
          -compress Group4 "$work/scan-g4.pdf"; then
        record "$work/scan-g4.pdf" "ImageMagick, bitonal CCITT G4 scan"
      fi
      if try "ImageMagick JPEG scan" convert "$scan" -quality 40 -compress JPEG "$work/scan-jpeg.pdf"; then
        record "$work/scan-jpeg.pdf" "ImageMagick, JPEG-compressed scan"
      fi
    fi
  fi
fi

echo "generated $made files, skipped $skipped -> $out"
echo "next: scripts/source-realworld-corpus.py classify && ... manifest"
