//go:build gopdfrab_nofonts

package fontsubst

// embedded is empty: this build leaves the fonts out, to be supplied by a
// Source.
var embedded map[string][]byte
