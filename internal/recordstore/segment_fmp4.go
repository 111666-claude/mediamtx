package recordstore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	amp4 "github.com/abema/go-mp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
)

// ErrSegmentEmpty is returned when a segment has a valid header but does not
// contain any media part, which is the case of a recording that was started and
// closed before receiving a single sample.
var ErrSegmentEmpty = errors.New("recording segment does not contain any media part")

func durationMp4ToGo(v int64, timeScale uint32) time.Duration {
	timeScale64 := int64(timeScale)
	secs := v / timeScale64
	dec := v % timeScale64
	return time.Duration(secs)*time.Second + time.Duration(dec)*time.Second/time.Duration(timeScale64)
}

func findInitTrack(tracks []*fmp4.InitTrack, id int) *fmp4.InitTrack {
	for _, track := range tracks {
		if track.ID == id {
			return track
		}
	}
	return nil
}

// SegmentFMP4ReadHeader reads the header of a recording segment in fMP4 format.
func SegmentFMP4ReadHeader(r io.ReadSeeker) (*fmp4.Init, time.Duration, error) {
	// check and skip ftyp

	buf := make([]byte, 8)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return nil, 0, err
	}

	if !bytes.Equal(buf[4:], []byte{'f', 't', 'y', 'p'}) {
		return nil, 0, fmt.Errorf("ftyp box not found")
	}

	ftypSize := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
	if ftypSize < 8 {
		return nil, 0, fmt.Errorf("invalid ftyp box size: %d", ftypSize)
	}

	_, err = r.Seek(int64(ftypSize), io.SeekStart)
	if err != nil {
		return nil, 0, err
	}

	// check moov

	_, err = io.ReadFull(r, buf)
	if err != nil {
		return nil, 0, err
	}

	if !bytes.Equal(buf[4:], []byte{'m', 'o', 'o', 'v'}) {
		return nil, 0, fmt.Errorf("moov box not found")
	}

	moovSize := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
	if moovSize < 8 {
		return nil, 0, fmt.Errorf("invalid moov box size: %d", moovSize)
	}

	// a corrupted size must not trigger a huge allocation.
	fileSize, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, 0, err
	}

	if int64(ftypSize)+int64(moovSize) > fileSize {
		return nil, 0, fmt.Errorf("moov box exceeds segment size: %d > %d",
			int64(ftypSize)+int64(moovSize), fileSize)
	}

	// find the mvhd box among the moov children

	mvhdPos := int64(-1)
	mvhdSize := uint32(0)
	moovEnd := int64(ftypSize) + int64(moovSize)

	_, err = r.Seek(int64(ftypSize)+8, io.SeekStart)
	if err != nil {
		return nil, 0, err
	}

	for {
		pos, err2 := r.Seek(0, io.SeekCurrent)
		if err2 != nil {
			return nil, 0, err2
		}

		if pos+8 > moovEnd {
			break
		}

		_, err = io.ReadFull(r, buf)
		if err != nil {
			return nil, 0, err
		}

		size := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
		if size < 8 {
			return nil, 0, fmt.Errorf("invalid box size: %d", size)
		}

		if bytes.Equal(buf[4:], []byte{'m', 'v', 'h', 'd'}) {
			mvhdPos = pos
			mvhdSize = size
			break
		}

		_, err = r.Seek(int64(size)-8, io.SeekCurrent)
		if err != nil {
			return nil, 0, err
		}
	}

	if mvhdPos < 0 {
		return nil, 0, fmt.Errorf("mvhd box not found")
	}

	if mvhdPos+int64(mvhdSize) > moovEnd {
		return nil, 0, fmt.Errorf("mvhd box exceeds moov box")
	}

	// read the mvhd payload

	var mvhd amp4.Mvhd
	_, err = r.Seek(mvhdPos+8, io.SeekStart)
	if err != nil {
		return nil, 0, err
	}

	_, err = amp4.Unmarshal(r, uint64(mvhdSize-8), &mvhd, amp4.Context{})
	if err != nil {
		return nil, 0, err
	}

	if mvhd.Timescale == 0 {
		return nil, 0, fmt.Errorf("invalid mvhd timescale: 0")
	}

	d := time.Duration(mvhd.GetDuration()) * time.Second / time.Duration(mvhd.Timescale)

	// read ftyp and moov

	_, err = r.Seek(0, io.SeekStart)
	if err != nil {
		return nil, 0, err
	}

	buf = make([]byte, uint64(ftypSize+moovSize))

	_, err = io.ReadFull(r, buf)
	if err != nil {
		return nil, 0, err
	}

	// pass ftyp and moov to fmp4.Init

	var init fmp4.Init
	err = init.Unmarshal(bytes.NewReader(buf))
	if err != nil {
		return nil, 0, err
	}

	return &init, d, nil
}

func segmentFMP4ReadDurationFromParts(
	r io.ReadSeeker,
	init *fmp4.Init,
) (time.Duration, error) {
	_, err := r.Seek(0, io.SeekStart)
	if err != nil {
		return 0, err
	}

	// check and skip ftyp

	buf := make([]byte, 8)
	_, err = io.ReadFull(r, buf)
	if err != nil {
		return 0, err
	}

	if !bytes.Equal(buf[4:], []byte{'f', 't', 'y', 'p'}) {
		return 0, fmt.Errorf("ftyp box not found")
	}

	ftypSize := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])

	_, err = r.Seek(int64(ftypSize), io.SeekStart)
	if err != nil {
		return 0, err
	}

	// check and skip moov

	_, err = io.ReadFull(r, buf)
	if err != nil {
		return 0, err
	}

	if !bytes.Equal(buf[4:], []byte{'m', 'o', 'o', 'v'}) {
		return 0, fmt.Errorf("moov box not found")
	}

	moovSize := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])

	_, err = r.Seek(int64(moovSize)-8, io.SeekCurrent)
	if err != nil {
		return 0, err
	}

	// find last valid moof and mdat

	lastMoofPos := int64(-1)

	for {
		var moofPos int64
		moofPos, err = r.Seek(0, io.SeekCurrent)
		if err != nil {
			return 0, err
		}

		_, err = io.ReadFull(r, buf)
		if err != nil {
			break
		}

		if !bytes.Equal(buf[4:], []byte{'m', 'o', 'o', 'f'}) {
			break
		}

		moofSize := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])

		// a box smaller than its own header, or a box with an extended size,
		// would move the read position backwards or leave it unchanged.
		if moofSize < 8 {
			if lastMoofPos < 0 {
				return 0, fmt.Errorf("invalid moof box size: %d", moofSize)
			}
			break
		}

		_, err = r.Seek(int64(moofSize)-8, io.SeekCurrent)
		if err != nil {
			break
		}

		_, err = io.ReadFull(r, buf)
		if err != nil {
			break
		}

		if !bytes.Equal(buf[4:], []byte{'m', 'd', 'a', 't'}) {
			break
		}

		mdatSize := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])

		if mdatSize < 8 {
			if lastMoofPos < 0 {
				return 0, fmt.Errorf("invalid mdat box size: %d", mdatSize)
			}
			break
		}

		_, err = r.Seek(int64(mdatSize)-8, io.SeekCurrent)
		if err != nil {
			break
		}

		lastMoofPos = moofPos
	}

	if lastMoofPos < 0 {
		return 0, ErrSegmentEmpty
	}

	// open last moof

	_, err = r.Seek(lastMoofPos+8, io.SeekStart)
	if err != nil {
		return 0, err
	}

	_, err = io.ReadFull(r, buf)
	if err != nil {
		return 0, err
	}

	// skip mfhd

	if !bytes.Equal(buf[4:], []byte{'m', 'f', 'h', 'd'}) {
		return 0, fmt.Errorf("mfhd box not found")
	}

	_, err = r.Seek(8, io.SeekCurrent)
	if err != nil {
		return 0, err
	}

	var maxElapsed time.Duration

	// foreach traf

outer:
	for {
		_, err = io.ReadFull(r, buf)
		if err != nil {
			return 0, err
		}

		switch {
		case bytes.Equal(buf[4:], []byte{'t', 'r', 'a', 'f'}):
		case bytes.Equal(buf[4:], []byte{'m', 'd', 'a', 't'}):
			break outer
		default:
			return 0, fmt.Errorf("unexpected box %x", buf[4:8])
		}

		// parse tfhd

		_, err = io.ReadFull(r, buf)
		if err != nil {
			return 0, err
		}

		if !bytes.Equal(buf[4:], []byte{'t', 'f', 'h', 'd'}) {
			return 0, fmt.Errorf("tfhd box not found")
		}

		tfhdSize := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])

		buf2 := make([]byte, tfhdSize-8)

		_, err = io.ReadFull(r, buf2)
		if err != nil {
			return 0, err
		}

		var tfhd amp4.Tfhd
		_, err = amp4.Unmarshal(bytes.NewReader(buf2), uint64(len(buf2)), &tfhd, amp4.Context{})
		if err != nil {
			return 0, fmt.Errorf("invalid tfhd box: %w", err)
		}

		track := findInitTrack(init.Tracks, int(tfhd.TrackID))
		if track == nil {
			return 0, fmt.Errorf("invalid track ID: %v", tfhd.TrackID)
		}

		// parse tfdt

		_, err = io.ReadFull(r, buf)
		if err != nil {
			return 0, err
		}

		if !bytes.Equal(buf[4:], []byte{'t', 'f', 'd', 't'}) {
			return 0, fmt.Errorf("tfdt box not found")
		}

		tfdtSize := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])

		buf2 = make([]byte, tfdtSize-8)

		_, err = io.ReadFull(r, buf2)
		if err != nil {
			return 0, err
		}

		var tfdt amp4.Tfdt
		_, err = amp4.Unmarshal(bytes.NewReader(buf2), uint64(len(buf2)), &tfdt, amp4.Context{})
		if err != nil {
			return 0, fmt.Errorf("invalid tfdt box: %w", err)
		}

		// parse trun

		_, err = io.ReadFull(r, buf)
		if err != nil {
			return 0, err
		}

		if !bytes.Equal(buf[4:], []byte{'t', 'r', 'u', 'n'}) {
			return 0, fmt.Errorf("trun box not found")
		}

		trunSize := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])

		buf2 = make([]byte, trunSize-8)

		_, err = io.ReadFull(r, buf2)
		if err != nil {
			return 0, err
		}

		var trun amp4.Trun
		_, err = amp4.Unmarshal(bytes.NewReader(buf2), uint64(len(buf2)), &trun, amp4.Context{})
		if err != nil {
			return 0, fmt.Errorf("invalid trun box: %w", err)
		}

		elapsed := int64(tfdt.BaseMediaDecodeTimeV1)

		for _, entry := range trun.Entries {
			elapsed += int64(entry.SampleDuration)
		}

		elapsedGo := durationMp4ToGo(elapsed, track.TimeScale)

		if elapsedGo > maxElapsed {
			maxElapsed = elapsedGo
		}
	}

	return maxElapsed, nil
}

// SegmentFMP4ReadHeaderAndDuration reads the header and duration of a recording segment in fMP4 format.
func SegmentFMP4ReadHeaderAndDuration(fpath string) (*fmp4.Init, time.Duration, error) {
	f, err := os.Open(fpath)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	init, duration, err := SegmentFMP4ReadHeader(f)
	if err != nil {
		return nil, 0, err
	}

	// if duration is not present in the header, compute it
	// by parsing each part
	if duration == 0 {
		duration, err = segmentFMP4ReadDurationFromParts(f, init)
		if err != nil {
			return nil, 0, err
		}
	}

	return init, duration, nil
}
