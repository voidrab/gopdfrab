package pdf

import (
	"bytes"
	"fmt"
	"io"

	"github.com/klauspost/compress/zlib"
)

// ImageCodecInput returns a reader over the bytes that feed the image codec
// ending dict's filter chain, and the codec. A Flate filter without a
// predictor is inflated as the reader is read, so a caller that needs only
// the start of the data, such as an image header, reads only that much.
// Any other filter before the codec is decoded whole when the reader is
// built. codec is nil for a chain that ends in no image codec. A bad zlib
// header, and a filter decoded whole that yields nothing, are returned as
// err; damage later in lazily inflated data is returned by Read after the
// bytes before it.
func ImageCodecInput(dict PDFDict) (r io.Reader, codec *FilterInfo, err error) {
	if !dict.IsStream() {
		return nil, nil, ErrNotAStream
	}
	names := FilterNames(dict.Entries.Get("Filter"))
	r = bytes.NewReader(dict.Stream())
	for i, name := range names {
		info, ok := LookupFilter(name)
		if !ok {
			return nil, nil, fmt.Errorf("%w %q", ErrUnsupportedFilter, name)
		}
		parms := FilterDecodeParms(dict, i, len(names))
		switch {
		case info.Image:
			if i != len(names)-1 {
				return nil, nil, fmt.Errorf("%w: %s must be the last filter", ErrUnsupportedFilter, info.Name)
			}
			return r, &info, nil
		case info.Kind == FilterFlate && DictInt(parms, "Predictor", 1) == 1:
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
			out, err := applyFilter(info, data, parms)
			if err == nil && info.Predictor {
				out, err = UndoStreamPredictor(out, parms, DecodeOptions{})
			}
			if err != nil {
				return nil, nil, err
			}
			r = bytes.NewReader(out)
		}
	}
	return r, nil, nil
}
