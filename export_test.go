package gopdfrab

import (
	"crypto/x509"
	"errors"
	"io"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// NoSystemCertPool makes the system trust store missing until restore is
// called.
func NoSystemCertPool() (restore func()) {
	systemCertPool = func() (*x509.CertPool, error) { return nil, errors.New("no system roots") }
	return func() { systemCertPool = x509.SystemCertPool }
}

// FailSignedBytesRead makes the nth read of a signature's signed bytes from
// now on fail, until restore is called.
func FailSignedBytesRead(n int) (restore func()) {
	calls := 0
	copyRange = func(r *pdf.Reader, w io.Writer, off, size int64) error {
		if calls++; calls == n {
			return errors.New("read failed")
		}
		return r.CopyRange(w, off, size)
	}
	return func() { copyRange = (*pdf.Reader).CopyRange }
}
