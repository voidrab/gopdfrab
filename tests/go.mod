// Marker module: keeps the vendored Isartor and veraPDF corpora (~18 MB) out
// of the published github.com/voidrab/gopdfrab module zip. Nested modules are
// pruned from module zips; the corpora remain available in git checkouts, and
// every test that reads them skips when they are absent.
module github.com/voidrab/gopdfrab/tests

go 1.26.4
