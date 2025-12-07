package pdfrab

type Cursor struct {
	data []byte
	pos  int
}

func NewCursor(data []byte) *Cursor {
	return &Cursor{data: data, pos: 0}
}

func (c *Cursor) ReadLine() (string, bool) {
	if c.pos >= len(c.data) {
		return "", false
	}
	start := c.pos
	for c.pos < len(c.data) && c.data[c.pos] != '\r' && c.data[c.pos] != '\n' {
		c.pos++
	}
	line := string(c.data[start:c.pos])

	switch c.data[c.pos] {
	case '\r':
		c.pos++
		if c.data[c.pos] == '\n' {
			c.pos++
		}
	case '\n':
		c.pos++
	}

	return line, true
}
