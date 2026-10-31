package pdf

import (
	"bytes"
	"io"

	"github.com/klauspost/compress/zlib"
)

// ImageCodecInput returns a reader over the bytes that feed the image codec
// ending dict's filter chain, and the codec. A Flate filter without a
// predictor is inflated as the reader is read, so a caller that needs only
// the start of the data, such as an image header, reads only that much.
// Any other filter before the codec is decoded whole when the reader is
// built. codec is nil for a chain that ends in no image codec. A bad zlib
// header, and a filter decoded whole that fails, are returned as
// err; damage later in lazily inflated data is returned by Read after the
// bytes before it.
func ImageCodecInput(dict PDFDict) (r io.Reader, codec *FilterInfo, err error) {
	if !dict.IsStream() {
		return nil, nil, ErrNotAStream
	}
	steps, err := filterChain(dict)
	if err != nil {
		return nil, nil, err
	}
	r = bytes.NewReader(dict.Stream())
	for _, s := range steps {
		switch {
		case s.info.Image:
			return r, &s.info, nil
		case s.info.Kind == FilterFlate && DictInt(s.parms, "Predictor", 1) == 1:
			zr, err := zlib.NewReader(r)
			if err != nil {
				return nil, nil, err
			}
			r = io.LimitReader(zr, effectiveDecodedCap())
		default:
			data, err := io.ReadAll(r)
			if err != nil && len(data) == 0 {
				return nil, nil, err
			}
			if data, err = s.decode(data, DecodeOptions{}); err != nil {
				return nil, nil, err
			}
			r = bytes.NewReader(data)
		}
	}
	return r, nil, nil
}
