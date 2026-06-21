package dunetools

import (
	"io"
	"mime/quotedprintable"
)

func quotedPrintableReader(reader io.Reader) io.Reader {
	return quotedprintable.NewReader(reader)
}
