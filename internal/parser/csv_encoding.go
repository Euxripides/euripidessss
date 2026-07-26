package parser

import (
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func csvReaderForEncoding(r io.Reader, name string) io.Reader {
	decoder := csvDecoder(name)
	if decoder == nil {
		return r
	}
	return transform.NewReader(r, decoder)
}

func csvDecoder(name string) transform.Transformer {
	var decoder *encoding.Decoder
	switch strings.ToLower(name) {
	case "utf-8-sig":
		return unicode.BOMOverride(encoding.Nop.NewDecoder())
	case "gb18030", "gbk":
		decoder = simplifiedchinese.GB18030.NewDecoder()
	default:
		return nil
	}
	return decoder
}

func rowsAreDecodedText(rows [][]string) bool {
	for _, row := range rows {
		for _, cell := range row {
			if !utf8.ValidString(cell) || strings.ContainsRune(cell, utf8.RuneError) {
				return false
			}
		}
	}
	return true
}
