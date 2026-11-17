// Package upload accepts a book from a phone in resumable chunks and files it
// into the library. It is the only place Bard writes new media.
package upload

import (
	"bytes"

	"github.com/not-emily/bardbookserver/internal/model"
)

// SniffBytes is how much of the head a format can be recognised from.
const SniffBytes = 256

// Sniff reads the content rather than the filename or the client's
// Content-Type, either of which a client can get wrong or lie about.
func Sniff(head []byte) (model.Format, bool) {
	switch {
	case isEPUB(head):
		return model.FormatEPUB, true
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return model.FormatPDF, true
	case isMP4(head):
		return model.FormatM4B, true
	case isMP3(head):
		return model.FormatMP3, true
	}
	return "", false
}

// isEPUB requires the mimetype entry a conformant EPUB stores first and
// uncompressed, so a plain zip is not mistaken for a book.
func isEPUB(head []byte) bool {
	return bytes.HasPrefix(head, []byte("PK\x03\x04")) &&
		bytes.Contains(head, []byte("application/epub+zip"))
}

func isMP4(head []byte) bool {
	return len(head) >= 12 && bytes.Equal(head[4:8], []byte("ftyp"))
}

func isMP3(head []byte) bool {
	if bytes.HasPrefix(head, []byte("ID3")) {
		return true
	}
	// A frame sync is eleven set bits, and the layer bits must not be the
	// reserved zero.
	return len(head) >= 2 && head[0] == 0xFF &&
		head[1]&0xE0 == 0xE0 && head[1]&0x06 != 0x00
}
