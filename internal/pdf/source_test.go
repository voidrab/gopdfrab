package pdf

// memSource is a source holding data in memory.
func memSource(data []byte) source {
	return source{mem: data, size: int64(len(data))}
}
