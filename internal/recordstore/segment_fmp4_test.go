package recordstore_test

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	amp4 "github.com/abema/go-mp4"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/recordstore"
	"github.com/bluenviron/mediamtx/internal/test"
)

func TestSegmentFMP4ReadHeaderAndDuration(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(map[bool]string{false: "parts", true: "header"}[cached], func(t *testing.T) {
			fpath := filepath.Join(t.TempDir(), "segment.mp4")
			test.WriteRecordingSegment(t, fpath, 2250*time.Millisecond, cached)

			init, duration, err := recordstore.SegmentFMP4ReadHeaderAndDuration(fpath)
			require.NoError(t, err)
			require.Len(t, init.Tracks, 1)
			require.Equal(t, 2250*time.Millisecond, duration)
		})
	}
}

func TestSegmentFMP4ReadHeaderAndDurationInvalid(t *testing.T) {
	fpath := filepath.Join(t.TempDir(), "segment.mp4")

	_, _, err := recordstore.SegmentFMP4ReadHeaderAndDuration(fpath)
	require.Error(t, err)

	err = os.WriteFile(fpath, []byte{}, 0o644)
	require.NoError(t, err)

	_, _, err = recordstore.SegmentFMP4ReadHeaderAndDuration(fpath)
	require.Error(t, err)

	// header with a zero timescale. It must not cause a division by zero.
	test.WriteRecordingSegment(t, fpath, time.Second, true)

	byts, err := os.ReadFile(fpath)
	require.NoError(t, err)

	boxes, err := amp4.ExtractBoxWithPayload(bytes.NewReader(byts), nil,
		amp4.BoxPath{amp4.BoxTypeMoov(), amp4.BoxTypeMvhd()})
	require.NoError(t, err)
	require.Len(t, boxes, 1)

	mvhd := boxes[0].Payload.(*amp4.Mvhd)
	mvhd.Timescale = 0

	f, err := os.OpenFile(fpath, os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer f.Close()

	_, err = f.Seek(int64(boxes[0].Info.Offset+boxes[0].Info.HeaderSize), io.SeekStart)
	require.NoError(t, err)

	_, err = amp4.Marshal(f, mvhd, boxes[0].Info.Context)
	require.NoError(t, err)

	_, _, err = recordstore.SegmentFMP4ReadHeaderAndDuration(fpath)
	require.Error(t, err)
	require.Contains(t, err.Error(), "timescale")
}

func TestSegmentFMP4ReadHeaderAndDurationZero(t *testing.T) {
	fpath := filepath.Join(t.TempDir(), "segment.mp4")
	test.WriteRecordingSegment(t, fpath, 0, true)

	_, _, err := recordstore.SegmentFMP4ReadHeaderAndDuration(fpath)
	require.ErrorIs(t, err, recordstore.ErrSegmentEmpty)
}

func TestSegmentFMP4ReadHeaderAndDurationMissing(t *testing.T) {
	_, _, err := recordstore.SegmentFMP4ReadHeaderAndDuration(
		filepath.Join(t.TempDir(), "missing.mp4"))
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestSegmentFMP4ReadHeader(t *testing.T) {
	fpath := filepath.Join(t.TempDir(), "segment.mp4")
	test.WriteRecordingSegment(t, fpath, 1500*time.Millisecond, true)

	buf, err := os.ReadFile(fpath)
	require.NoError(t, err)

	init, duration, err := recordstore.SegmentFMP4ReadHeader(bytes.NewReader(buf))
	require.NoError(t, err)
	require.Len(t, init.Tracks, 1)
	require.Equal(t, 1500*time.Millisecond, duration)
}
