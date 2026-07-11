// Marker module: keeps the vendored Arlington PDF Model TSVs (~3.7 MB) out of
// the published github.com/voidrab/gopdfrab module zip. Nested modules are
// pruned from module zips; the TSVs are only needed to run gen.go (and the
// generator-idempotency test, which skips when they are absent), and remain
// available in git checkouts.
module github.com/voidrab/gopdfrab/internal/arlington/testdata

go 1.26.4
