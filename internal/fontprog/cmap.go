package fontprog

import (
	_ "embed"
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

var (
	toUnicodeBfCharBlockRe  = regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`)
	toUnicodeBfRangeBlockRe = regexp.MustCompile(`(?s)beginbfrange(.*?)endbfrange`)
	toUnicodeBfCharEntryRe  = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	toUnicodeBfRangeEntryRe = regexp.MustCompile(`(?s)<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>\s*(?:<([0-9A-Fa-f]+)>|\[([^\]]*)\])`)
	toUnicodeHexTokenRe     = regexp.MustCompile(`<([0-9A-Fa-f]+)>`)
)

// hexToUnicode takes the first UTF-16 code unit of a ToUnicode destination
// hex string as its Unicode value, ignoring surrogate pairs/ligatures.
func hexToUnicode(hex string) (uint16, bool) {
	b := pdf.DecodePDFHexStringBytes(hex)
	if len(b) < 2 {
		return 0, false
	}
	return binary.BigEndian.Uint16(b[:2]), true
}

// ParseToUnicodeCMap extracts a code->Unicode mapping from a /ToUnicode
// CMap stream's bfchar/bfrange blocks (PDF 32000-1, 9.10.3).
func ParseToUnicodeCMap(data []byte) map[int]uint16 {
	result := map[int]uint16{}
	s := string(data)
	for _, block := range toUnicodeBfCharBlockRe.FindAllStringSubmatch(s, -1) {
		for _, m := range toUnicodeBfCharEntryRe.FindAllStringSubmatch(block[1], -1) {
			code, err := strconv.ParseInt(m[1], 16, 32)
			if err != nil {
				continue
			}
			if u, ok := hexToUnicode(m[2]); ok {
				result[int(code)] = u
			}
		}
	}
	for _, block := range toUnicodeBfRangeBlockRe.FindAllStringSubmatch(s, -1) {
		for _, m := range toUnicodeBfRangeEntryRe.FindAllStringSubmatch(block[1], -1) {
			lo, errLo := strconv.ParseInt(m[1], 16, 32)
			hi, errHi := strconv.ParseInt(m[2], 16, 32)
			if errLo != nil || errHi != nil || hi < lo {
				continue
			}
			if m[3] != "" {
				base, ok := hexToUnicode(m[3])
				if !ok {
					continue
				}
				for c := lo; c <= hi; c++ {
					result[int(c)] = base + uint16(c-lo)
				}
			} else if m[4] != "" {
				dsts := toUnicodeHexTokenRe.FindAllStringSubmatch(m[4], -1)
				for i, c := 0, lo; i < len(dsts) && c <= hi; i, c = i+1, c+1 {
					if u, ok := hexToUnicode(dsts[i][1]); ok {
						result[int(c)] = u
					}
				}
			}
		}
	}
	return result
}

// CmapToken pairs a CmapTokenize token with its byte range in the source
// data, so a fixer can splice a replacement in place (fixups_limits.go)
// without disturbing anything else (comments, whitespace, formatting).
type CmapToken struct {
	Text       string
	Start, End int
}

// CmapTokenize splits CMap PostScript content into tokens, skipping comments.
func CmapTokenize(data []byte) []CmapToken {
	var tokens []CmapToken
	i := 0
	for i < len(data) {
		for i < len(data) && pdf.IsWhitespace(data[i]) {
			i++
		}
		if i >= len(data) {
			break
		}
		if data[i] == '%' {
			for i < len(data) && data[i] != '\n' {
				i++
			}
			continue
		}
		start := i
		if data[i] == '<' {
			// hex string token: <...> (stop at first >)
			j := i + 1
			for j < len(data) && data[j] != '>' {
				j++
			}
			if j < len(data) {
				j++
			}
			tokens = append(tokens, CmapToken{string(data[i:j]), start, j})
			i = j
		} else if data[i] == '(' {
			// literal string: skip to matching ')'
			j := i + 1
			depth := 1
			for j < len(data) && depth > 0 {
				if data[j] == '\\' {
					j += 2
					continue
				}
				switch data[j] {
				case '(':
					depth++
				case ')':
					depth--
				}
				j++
			}
			tokens = append(tokens, CmapToken{string(data[i:j]), start, j})
			i = j
		} else {
			// regular token: read until whitespace or delimiter
			j := i
			for j < len(data) && !pdf.IsWhitespace(data[j]) && data[j] != '<' && data[j] != '(' && data[j] != ')' {
				j++
			}
			if j > i {
				tokens = append(tokens, CmapToken{string(data[i:j]), start, j})
			}
			i = j
		}
	}
	return tokens
}

// CMapCIDEntry is one cidrange or cidchar entry of a CMap: the CID token, and
// for a range the codes it spans.
type CMapCIDEntry struct {
	// Lo and Hi are the first and last code of a range; they are zero for a
	// cidchar entry.
	Lo, Hi CmapToken
	// CID is the token holding the CID, which for a range is the first code's.
	CID CmapToken
}

// CodeSpan returns how many CIDs a range runs past its first one, or 0 for a
// cidchar entry and for codes that are malformed or out of order.
func (e CMapCIDEntry) CodeSpan() int64 {
	lo, okLo := cmapHexValue(e.Lo.Text)
	hi, okHi := cmapHexValue(e.Hi.Text)
	if !okLo || !okHi || hi < lo {
		return 0
	}
	return hi - lo
}

// LastCID returns the largest CID the entry maps to. ok is false when its CID
// token is not a number.
func (e CMapCIDEntry) LastCID() (cid int64, ok bool) {
	first, ok := CmapParseInt(e.CID.Text)
	return first + e.CodeSpan(), ok
}

// cmapHexDigits returns the hex digits of a hex string token such as "<00 ff>",
// without the white space the string may hold. ok is false for any other token.
func cmapHexDigits(tok string) (string, bool) {
	if len(tok) < 3 || tok[0] != '<' || tok[len(tok)-1] != '>' {
		return "", false
	}
	digits := tok[1 : len(tok)-1]
	if strings.ContainsAny(digits, " \t\r\n\f\x00") {
		digits = strings.Join(strings.FieldsFunc(digits, func(r rune) bool { return strings.ContainsRune(" \t\r\n\f\x00", r) }), "")
	}
	return digits, true
}

// cmapHexValue returns the value of a hex string token. ok is false when the
// token is not one or its digits do not parse as a 63-bit integer.
func cmapHexValue(tok string) (int64, bool) {
	digits, ok := cmapHexDigits(tok)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseUint(digits, 16, 63)
	return int64(v), err == nil
}

// CMapCodeAfter returns the hex string token, as many digits wide as code, for
// the value of code plus n. code must be a token CodeSpan could read.
func CMapCodeAfter(code string, n int64) string {
	digits, _ := cmapHexDigits(code)
	v, _ := cmapHexValue(code)
	return fmt.Sprintf("<%0*x>", len(digits), v+n)
}

// EachCMapCID calls fn with each cidrange and cidchar entry in CMap PostScript
// content: a "<lo> <hi> cid" range or a "<code> cid" mapping.
func EachCMapCID(data []byte, fn func(e CMapCIDEntry)) {
	entryLen, pos := 0, 0
	var held [2]CmapToken
	for _, tok := range CmapTokenize(data) {
		switch tok.Text {
		case "begincidrange":
			entryLen, pos = 3, 0
		case "begincidchar":
			entryLen, pos = 2, 0
		case "endcidrange", "endcidchar":
			// Each keyword ends only its own kind of block.
			if tok.Text == "endcidrange" && entryLen == 3 || tok.Text == "endcidchar" && entryLen == 2 {
				entryLen = 0
			}
			pos = 0
		default:
			if entryLen == 0 {
				continue
			}
			if pos++; pos < entryLen {
				held[pos-1] = tok
				continue
			}
			e := CMapCIDEntry{CID: tok}
			if entryLen == 3 {
				e.Lo, e.Hi = held[0], held[1]
			}
			fn(e)
			pos = 0
		}
	}
}

// CmapParseInt parses a decimal integer token from a CMap stream.
func CmapParseInt(tok string) (int64, bool) {
	if len(tok) == 0 {
		return 0, false
	}
	var n int64
	for _, c := range []byte(tok) {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}

var WmodeRe = regexp.MustCompile(`/WMode\s+(\d+)\s+def`)
