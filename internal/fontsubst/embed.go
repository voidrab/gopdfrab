//go:build !gopdfrab_nofonts

package fontsubst

import "embed"

//go:embed fonts/*.ttf.gz
var embedded embed.FS

// builtIn reports that the fonts are built in.
const builtIn = true

// builtin is the source of the built-in fonts.
func builtin(name string) ([]byte, error) { return embedded.ReadFile("fonts/" + name) }
