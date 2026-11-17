//go:build !gopdfrab_nofonts

package fontsubst

import _ "embed"

var (
	//go:embed fonts/LiberationSans-Regular.ttf.gz
	libSansRegularGz []byte
	//go:embed fonts/LiberationSans-Bold.ttf.gz
	libSansBoldGz []byte
	//go:embed fonts/LiberationSans-Italic.ttf.gz
	libSansItalicGz []byte
	//go:embed fonts/LiberationSans-BoldItalic.ttf.gz
	libSansBoldItalicGz []byte
	//go:embed fonts/LiberationSerif-Regular.ttf.gz
	libSerifRegularGz []byte
	//go:embed fonts/LiberationSerif-Bold.ttf.gz
	libSerifBoldGz []byte
	//go:embed fonts/LiberationSerif-Italic.ttf.gz
	libSerifItalicGz []byte
	//go:embed fonts/LiberationSerif-BoldItalic.ttf.gz
	libSerifBoldItalicGz []byte
	//go:embed fonts/LiberationMono-Regular.ttf.gz
	libMonoRegularGz []byte
	//go:embed fonts/LiberationMono-Bold.ttf.gz
	libMonoBoldGz []byte
	//go:embed fonts/LiberationMono-Italic.ttf.gz
	libMonoItalicGz []byte
	//go:embed fonts/LiberationMono-BoldItalic.ttf.gz
	libMonoBoldItalicGz []byte
	//go:embed fonts/NotoSansSymbols2-Regular.ttf.gz
	notoSymbols2Gz []byte
	//go:embed fonts/NotoSansSymbols-VariableFont_wght.ttf.gz
	notoSymbolsGz []byte
)

// embedded holds the built-in fonts, gzip-compressed, by file name.
var embedded = map[string][]byte{
	libSansRegularFile: libSansRegularGz, libSansBoldFile: libSansBoldGz,
	libSansItalicFile: libSansItalicGz, libSansBoldItalicFile: libSansBoldItalicGz,
	libSerifRegularFile: libSerifRegularGz, libSerifBoldFile: libSerifBoldGz,
	libSerifItalicFile: libSerifItalicGz, libSerifBoldItalicFile: libSerifBoldItalicGz,
	libMonoRegularFile: libMonoRegularGz, libMonoBoldFile: libMonoBoldGz,
	libMonoItalicFile: libMonoItalicGz, libMonoBoldItalicFile: libMonoBoldItalicGz,
	notoSymbols2File: notoSymbols2Gz, notoSymbolsFile: notoSymbolsGz,
}
