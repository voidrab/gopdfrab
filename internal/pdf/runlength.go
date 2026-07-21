package pdf

// maxRunLengthOutput caps decoded output so a crafted run cannot OOM. Var,
// not const, only so tests can lower it.
var maxRunLengthOutput = 256 << 20

// DecodeRunLength decodes a RunLengthDecode stream (ISO 32000-1 7.4.5): a
// length byte L is followed by L+1 literal bytes when L < 128, or by a single
// byte repeated 257-L times when L > 128. L == 128 is the EOD marker.
//
// A stream truncated mid-run yields the bytes decoded so far with a nil
// error, and a missing EOD is likewise not an error -- matching InflateZlib's
// and DecodeLZW's leniency toward the damaged files these filters turn up in.
// Exceeding maxRunLengthOutput is an error.
func DecodeRunLength(data []byte) ([]byte, error) {
	out := make([]byte, 0, len(data)*2)
	for i := 0; i < len(data); {
		n := int(data[i])
		i++
		switch {
		case n == 128:
			return out, nil // EOD
		case n < 128:
			end := min(i+n+1, len(data))
			out = append(out, data[i:end]...)
			i = end
		default:
			if i >= len(data) {
				return out, nil // truncated repeat header
			}
			for range 257 - n {
				out = append(out, data[i])
			}
			i++
		}
		if len(out) > maxRunLengthOutput {
			return nil, ErrOutputTooLarge
		}
	}
	return out, nil
}
