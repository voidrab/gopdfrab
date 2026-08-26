//go:build !js

package convert

// spillSupported reports whether a conversion's output may spill to a temp file.
// It is false only on js/wasm, which has no filesystem; there the output stays
// in memory exactly as it always did (the mmap-backed large-file guarantee is
// likewise native-only).
const spillSupported = true
