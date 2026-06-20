#!/usr/bin/env node
// runner.mjs — JS-ecosystem reference point for the gopdfrab benchmark suite.
//
// IMPORTANT CAVEAT: there is no mature pure-JS PDF/A-1b *validator*. The
// `mupdf` package (WASM build of MuPDF) was the best candidate evaluated,
// but its API (see node_modules/mupdf/dist/mupdf.d.ts) exposes no PDF/A
// conformance checking at all — only generic document loading. This runner
// therefore measures Document.openDocument() + countPages(), i.e. parse/load
// time only, NOT PDF/A verification. Every output row and summary is tagged
// "load_only" so this is never confused with an apples-to-apples conformance
// check against gopdfrab/veraPDF/PDFBox. Treat its numbers as a loose
// JS-runtime baseline, not a competing validator's verdict.
//
// Mirrors the output shape of cmd/gopdfrab-bench and PreflightBatch.java:
// CSV rows of path,size_bytes,nanos,valid,err,issues on stdout, then a
// "#summary {...}" JSON line.
//
// Usage:
//   node runner.mjs --mode=single <file>
//   node runner.mjs --mode=batch  <dir>
import * as mupdf from "mupdf";
import { readFileSync, statSync } from "fs";
import { readdir } from "fs/promises";
import path from "path";

function parseArgs(argv) {
    let mode = "single";
    const rest = [];
    for (const a of argv) {
        if (a.startsWith("--mode=")) mode = a.slice("--mode=".length);
        else rest.push(a);
    }
    if (rest.length < 1) {
        console.error("usage: runner.mjs --mode=single|batch <path>...");
        process.exit(2);
    }
    if (mode === "single" && rest.length !== 1) {
        console.error("--mode=single takes exactly one file path");
        process.exit(2);
    }
    return { mode, targets: rest };
}

async function walkPdfs(dir) {
    const out = [];
    async function rec(d) {
        for (const ent of await readdir(d, { withFileTypes: true })) {
            const p = path.join(d, ent.name);
            if (ent.isDirectory()) await rec(p);
            else if (ent.name.toLowerCase().endsWith(".pdf")) out.push(p);
        }
    }
    await rec(dir);
    return out;
}

function runOne(filePath) {
    let size = 0;
    try {
        size = statSync(filePath).size;
    } catch {
        // fall through; size stays 0
    }

    const start = process.hrtime.bigint();
    try {
        const buf = readFileSync(filePath);
        const doc = mupdf.Document.openDocument(buf, "application/pdf");
        doc.countPages();
        const nanos = Number(process.hrtime.bigint() - start);
        return { path: filePath, size_bytes: size, nanos, valid: "load_only", err: "", issues: 0 };
    } catch (e) {
        const nanos = Number(process.hrtime.bigint() - start);
        return { path: filePath, size_bytes: size, nanos, valid: "load_only", err: String(e.message || e), issues: 0 };
    }
}

async function main() {
    const { mode, targets } = parseArgs(process.argv.slice(2));

    let paths;
    if (mode === "single") {
        paths = targets;
    } else if (mode === "batch") {
        // Accepts multiple roots (e.g. two separate corpus directories) so
        // callers don't need a shared parent directory that might pull in
        // unrelated files.
        paths = [];
        for (const t of targets) paths.push(...(await walkPdfs(t)));
    } else {
        console.error(`unknown --mode=${mode} (want single|batch)`);
        process.exit(2);
    }

    console.log("path,size_bytes,nanos,valid,err,issues");
    let files = 0, errors = 0, totalBytes = 0, totalNanos = 0;
    for (const p of paths) {
        const r = runOne(p);
        files++;
        totalBytes += r.size_bytes;
        totalNanos += r.nanos;
        if (r.err) errors++;
        console.log([r.path, r.size_bytes, r.nanos, r.valid, r.err, r.issues].join(","));
    }

    const secs = totalNanos / 1e9;
    const summary = {
        tool: "js-mupdf-load-only",
        caveat: "no PDF/A conformance check performed; measures document load+parse only",
        mode,
        files,
        errors,
        total_bytes: totalBytes,
        total_nanos: totalNanos,
        files_per_sec: secs > 0 ? files / secs : 0,
        mb_per_sec: secs > 0 ? (totalBytes / 1048576) / secs : 0,
        max_rss_kb: process.resourceUsage().maxRSS,
    };
    console.error("#summary " + JSON.stringify(summary));
}

main();
