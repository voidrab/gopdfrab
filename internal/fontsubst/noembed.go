//go:build gopdfrab_nofonts

package fontsubst

import "errors"

// ErrNoSource is returned for a font of a build without the built-in fonts
// while no source is set with SetSource.
var ErrNoSource = errors.New("fontsubst: the fonts are not built in and no source is set")

// builtIn is false because this build leaves the fonts out.
const builtIn = false

// builtin is the source of the built-in fonts, which this build lacks.
func builtin(string) ([]byte, error) { return nil, ErrNoSource }
