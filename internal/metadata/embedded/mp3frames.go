package embedded

import (
	"encoding/binary"
	"os"
)

// Duration comes from the encoder's Xing/Info or VBRI header when there is
// one, which is exact, and otherwise from the first frame's bitrate, which is
// exact for CBR and an estimate for VBR. Summing every frame would always be
// exact but reads the whole file, and a cold scan of 5000 titles cannot
// afford that.

// Layer III only: Bard never sees Layer I or II in a book.
var bitrates = map[int][15]int{
	1:  {0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320},
	2:  {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160},
	25: {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160},
}

var sampleRates = map[int][3]int{
	1:  {44100, 48000, 32000},
	2:  {22050, 24000, 16000},
	25: {11025, 12000, 8000},
}

type mp3Frame struct {
	version    int // 1, 2 or 25 for MPEG 2.5
	bitrate    int // bits per second
	sampleRate int
	mono       bool
	samples    int // samples per frame
}

func mp3Duration(path string) *float64 {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil
	}

	audioStart := id3v2Size(f)
	if audioStart >= info.Size() {
		return nil
	}

	// One read is enough: the first frame holds any VBR header, and a sync
	// word this far from the tag end means the file is not an MP3 we can read.
	buf := make([]byte, 8192)
	n, err := f.ReadAt(buf, audioStart)
	if n < 4 {
		_ = err
		return nil
	}
	buf = buf[:n]

	offset, frame, ok := firstFrame(buf)
	if !ok {
		return nil
	}

	if frames, ok := vbrFrameCount(buf[offset:], frame); ok && frames > 0 {
		seconds := float64(frames) * float64(frame.samples) / float64(frame.sampleRate)
		return &seconds
	}

	audioBytes := info.Size() - audioStart - int64(offset)
	if audioBytes <= 0 || frame.bitrate <= 0 {
		return nil
	}
	seconds := float64(audioBytes) * 8 / float64(frame.bitrate)
	return &seconds
}

// id3v2Size reports where audio begins. The tag length is stored seven bits
// per byte so it can never contain a false sync word.
func id3v2Size(f *os.File) int64 {
	var hdr [10]byte
	if _, err := f.ReadAt(hdr[:], 0); err != nil {
		return 0
	}
	if string(hdr[0:3]) != "ID3" {
		return 0
	}
	size := int64(hdr[6])<<21 | int64(hdr[7])<<14 | int64(hdr[8])<<7 | int64(hdr[9])
	total := 10 + size
	if hdr[5]&0x10 != 0 {
		total += 10 // footer
	}
	return total
}

func firstFrame(buf []byte) (int, mp3Frame, bool) {
	for i := 0; i+4 <= len(buf); i++ {
		if buf[i] != 0xFF || buf[i+1]&0xE0 != 0xE0 {
			continue
		}
		if frame, ok := parseFrameHeader(buf[i:]); ok {
			return i, frame, true
		}
	}
	return 0, mp3Frame{}, false
}

func parseFrameHeader(b []byte) (mp3Frame, bool) {
	if len(b) < 4 || b[0] != 0xFF || b[1]&0xE0 != 0xE0 {
		return mp3Frame{}, false
	}

	var version int
	switch (b[1] >> 3) & 0x03 {
	case 0x03:
		version = 1
	case 0x02:
		version = 2
	case 0x00:
		version = 25
	default:
		return mp3Frame{}, false
	}

	if (b[1]>>1)&0x03 != 0x01 { // Layer III
		return mp3Frame{}, false
	}

	bitrateIdx := int((b[2] >> 4) & 0x0F)
	rateIdx := int((b[2] >> 2) & 0x03)
	if bitrateIdx == 0 || bitrateIdx == 15 || rateIdx == 3 {
		return mp3Frame{}, false
	}

	frame := mp3Frame{
		version:    version,
		bitrate:    bitrates[version][bitrateIdx] * 1000,
		sampleRate: sampleRates[version][rateIdx],
		mono:       (b[3]>>6)&0x03 == 0x03,
		samples:    576,
	}
	if version == 1 {
		frame.samples = 1152
	}
	if frame.bitrate == 0 || frame.sampleRate == 0 {
		return mp3Frame{}, false
	}
	return frame, true
}

// vbrFrameCount reads the frame total an encoder may have written inside the
// first frame. Its position is fixed by the side info size, so it has to be
// computed rather than searched for: an ID3 text field containing "Info" would
// otherwise match.
func vbrFrameCount(frameData []byte, frame mp3Frame) (uint32, bool) {
	sideInfo := 32
	switch {
	case frame.version == 1 && frame.mono:
		sideInfo = 17
	case frame.version != 1 && frame.mono:
		sideInfo = 9
	case frame.version != 1:
		sideInfo = 17
	}

	if at := 4 + sideInfo; len(frameData) >= at+12 {
		magic := string(frameData[at : at+4])
		if magic == "Xing" || magic == "Info" {
			flags := binary.BigEndian.Uint32(frameData[at+4:])
			if flags&0x0001 != 0 {
				return binary.BigEndian.Uint32(frameData[at+8:]), true
			}
		}
	}

	// VBRI sits at a fixed offset instead, and only Fraunhofer writes it.
	if at := 4 + 32; len(frameData) >= at+18 {
		if string(frameData[at:at+4]) == "VBRI" {
			return binary.BigEndian.Uint32(frameData[at+14:]), true
		}
	}
	return 0, false
}
