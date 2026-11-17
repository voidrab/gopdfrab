//go:build !gopdfrab_nofonts

package fontsubst

import "embed"

//go:embed fonts/*.ttf.gz
var embedded embed.FS

// builtIn is true because the fonts are built into the binary.
const builtIn = true

// builtin is the source of the built-in fonts.
func builtin(name string) ([]byte, error) { return embedded.ReadFile("fonts/" + name) }
