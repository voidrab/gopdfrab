//go:build gopdfrab_nofonts

package fontsubst

import "errors"

// ErrNoSource is returned for every font of this build, which leaves the
// fonts out, until SetSource sets a source for them.
var ErrNoSource = errors.New("fontsubst: the fonts are not built in and no source is set")

// builtIn reports that the fonts are built in: they are not.
const builtIn = false

// builtin is the source of the built-in fonts, of which there are none.
func builtin(string) ([]byte, error) { return nil, ErrNoSource }
