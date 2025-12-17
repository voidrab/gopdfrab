package pdfrab

type TokenType int

const (
	TokenError TokenType = iota
	TokenEOF
	TokenBoolean
	TokenInteger
	TokenReal
	TokenString
	TokenHexString
	TokenName
	TokenKeyword
	TokenArrayStart
	TokenArrayEnd
	TokenDictStart
	TokenDictEnd
	TokenObjectStart
	TokenObjectEnd
	TokenStreamStart
	TokenStreamEnd
)
