// Package mp4box contains custom MP4 box definitions and helpers shared by
// the configuration, stream and recordstore packages.
package mp4box

import (
	"encoding/binary"
	"fmt"
	"io"

	amp4 "github.com/abema/go-mp4"
)

// BoxTypeMtxi returns the box type of Mtxi.
func BoxTypeMtxi() amp4.BoxType { return amp4.StrToBoxType("mtxi") }

func init() { //nolint:gochecknoinits
	amp4.AddBoxDef(&Mtxi{}, 0)
}

// Mtxi is a MediaMTX segment info box.
type Mtxi struct {
	amp4.FullBox  `mp4:"0,extend"`
	StreamID      [16]byte `mp4:"1,size=8"`
	SegmentNumber uint64   `mp4:"2,size=64"`
	DTS           int64    `mp4:"3,size=64"`
	NTP           int64    `mp4:"4,size=64"`
}

// GetType implements amp4.IBox.
func (*Mtxi) GetType() amp4.BoxType {
	return BoxTypeMtxi()
}

// IsFragmented reports whether r is a fragmented MP4 (fMP4) instead of a
// regular, sample-table based MP4.
//
// A file is considered fragmented when it contains at least one "moof" box or
// its "moov" box contains an "mvex" box. The latter catches initialization
// segments that do not contain any fragment yet.
//
// IsFragmented seeks to the beginning of r before and after scanning.
func IsFragmented(r io.ReadSeeker) (bool, error) {
	_, err := r.Seek(0, io.SeekStart)
	if err != nil {
		return false, err
	}

	fragmented := false

	for {
		boxStart, err := r.Seek(0, io.SeekCurrent)
		if err != nil {
			return false, err
		}

		boxType, headerSize, boxSize, err := readBoxHeader(r)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return false, err
		}

		if boxSize != 0 && boxSize < headerSize {
			return false, fmt.Errorf("invalid MP4 box size %d for box %q", boxSize, boxType)
		}

		switch boxType {
		case "moof":
			fragmented = true

		case "moov":
			var hasMvex bool
			hasMvex, err = boxContainsType(r, boxStart, boxSize, headerSize, "mvex")
			if err != nil {
				return false, err
			}
			if hasMvex {
				fragmented = true
			}
		}

		if fragmented {
			_, err = r.Seek(0, io.SeekStart)
			return true, err
		}

		if boxSize == 0 {
			// box extends to the end of the file
			break
		}

		_, err = r.Seek(boxStart+boxSize, io.SeekStart)
		if err != nil {
			return false, err
		}
	}

	_, err = r.Seek(0, io.SeekStart)
	return false, err
}

// readBoxHeader reads an MP4 box header at the current position.
// It returns the box type, the header size and the box size. A box size of
// zero means that the box extends to the end of the file.
func readBoxHeader(r io.Reader) (string, int64, int64, error) {
	header := make([]byte, 8)

	_, err := io.ReadFull(r, header)
	if err != nil {
		return "", 0, 0, err
	}

	size32 := binary.BigEndian.Uint32(header[:4])
	boxType := string(header[4:8])
	headerSize := int64(8)
	boxSize := int64(size32)

	if size32 == 1 {
		extended := make([]byte, 8)

		_, err = io.ReadFull(r, extended)
		if err != nil {
			return "", 0, 0, err
		}

		boxSize = int64(binary.BigEndian.Uint64(extended))
		headerSize = 16
	}

	return boxType, headerSize, boxSize, nil
}

// boxContainsType reports whether a direct child of the box that starts at
// boxStart is of type wantedType.
func boxContainsType(
	r io.ReadSeeker,
	boxStart int64,
	boxSize int64,
	headerSize int64,
	wantedType string,
) (bool, error) {
	if boxSize == 0 {
		// box extends to the end of the file, its end is unknown.
		return false, nil
	}

	end := boxStart + boxSize
	pos := boxStart + headerSize

	for (pos + 8) <= end {
		_, err := r.Seek(pos, io.SeekStart)
		if err != nil {
			return false, err
		}

		boxType, childHeaderSize, childSize, err := readBoxHeader(r)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return false, nil
			}
			return false, err
		}

		if boxType == wantedType {
			return true, nil
		}

		if childSize == 0 {
			// child extends to the end of the file
			break
		}

		if childSize < childHeaderSize {
			return false, fmt.Errorf("invalid MP4 box size %d for box %q", childSize, boxType)
		}

		pos += childSize
	}

	return false, nil
}
