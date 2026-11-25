package stream_test

import (
	"testing"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/av1"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/unit"
)

// Sequence headers extracted from fixed AV1 samples generated with ffmpeg
// (libaom-av1, yuv420p) and cross-checked with ffprobe.
// See artifacts/av1scripts and artifacts/av1samples.
var (
	// 1920x1080, profile 0 (Main), level 8, tier 0
	av1SequenceHeader1080p = []byte{0x0a, 0x0b, 0x00, 0x00, 0x00, 0x42, 0xab, 0xbf, 0xc3, 0x73, 0x2b, 0xe4, 0x01}
	// 1280x720, profile 0 (Main), level 5, tier 0
	av1SequenceHeader720p = []byte{0x0a, 0x0b, 0x00, 0x00, 0x00, 0x2d, 0x4c, 0xff, 0xb3, 0xcc, 0xaf, 0x90, 0x04}
)

// av1FrameOBU is a frame OBU (type 6).
var av1FrameOBU = []byte{0x32, 0x01, 0x00}

type av1TestEnv struct {
	strm      *stream.Stream
	subStream *stream.SubStream
	media     *description.Media
	forma     *format.AV1
	reader    *stream.Reader
	recv      chan struct{}
}

func newAV1TestEnv(t *testing.T) *av1TestEnv {
	t.Helper()

	forma := &format.AV1{PayloadTyp: 96}
	media := &description.Media{
		Type:    description.MediaTypeVideo,
		Formats: []format.Format{forma},
	}
	desc := &description.Session{Medias: []*description.Media{media}}

	strm := &stream.Stream{
		OrigDesc:          desc,
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
	}
	err := strm.Initialize()
	require.NoError(t, err)

	subStream := &stream.SubStream{
		Stream:        strm,
		UseRTPPackets: false,
	}
	err = subStream.Initialize()
	require.NoError(t, err)

	env := &av1TestEnv{
		strm:      strm,
		subStream: subStream,
		media:     media,
		forma:     forma,
		recv:      make(chan struct{}),
	}

	env.reader = &stream.Reader{}
	env.reader.OnData(media, forma, func(_ *unit.Unit) error {
		close(env.recv)
		return nil
	})
	strm.AddReader(env.reader)

	return env
}

func (e *av1TestEnv) close() {
	e.strm.RemoveReader(e.reader)
	e.strm.Close()
}

func (e *av1TestEnv) write(t *testing.T, payload unit.PayloadAV1) {
	t.Helper()

	e.recv = make(chan struct{})
	e.subStream.WriteUnit(e.media, e.forma, &unit.Unit{Payload: payload})
	<-e.recv
}

// sequenceHeader returns the sequence header of the only track of the test stream.
func (e *av1TestEnv) sequenceHeader() *av1.SequenceHeader {
	return e.strm.AV1SequenceHeaders()[[2]int{0, 0}]
}

func TestFormatUpdaterAV1(t *testing.T) {
	t.Run("unknown before first sequence header", func(t *testing.T) {
		env := newAV1TestEnv(t)
		defer env.close()

		require.Empty(t, env.strm.AV1SequenceHeaders())

		env.write(t, unit.PayloadAV1{av1FrameOBU})

		// a frame without sequence header must not populate any state
		require.Empty(t, env.strm.AV1SequenceHeaders())
	})

	t.Run("sequence header updates width height profile level tier", func(t *testing.T) {
		env := newAV1TestEnv(t)
		defer env.close()

		env.write(t, unit.PayloadAV1{
			{0x12, 0x00}, // temporal delimiter
			av1SequenceHeader1080p,
			av1FrameOBU,
		})

		require.Len(t, env.strm.AV1SequenceHeaders(), 1)

		sh := env.sequenceHeader()
		require.NotNil(t, sh)
		require.Equal(t, 1920, sh.Width())
		require.Equal(t, 1080, sh.Height())
		require.Equal(t, uint8(0), sh.SeqProfile)
		require.Equal(t, uint8(8), sh.SeqLevelIdx[0])
		require.False(t, sh.SeqTier[0])
	})

	t.Run("resolution switch", func(t *testing.T) {
		env := newAV1TestEnv(t)
		defer env.close()

		env.write(t, unit.PayloadAV1{av1SequenceHeader1080p, av1FrameOBU})
		env.write(t, unit.PayloadAV1{av1SequenceHeader720p, av1FrameOBU})

		sh := env.sequenceHeader()
		require.NotNil(t, sh)
		require.Equal(t, 1280, sh.Width())
		require.Equal(t, 720, sh.Height())
		require.Equal(t, uint8(5), sh.SeqLevelIdx[0])
	})

	t.Run("truncated or corrupted OBU is ignored", func(t *testing.T) {
		env := newAV1TestEnv(t)
		defer env.close()

		env.write(t, unit.PayloadAV1{av1SequenceHeader1080p, av1FrameOBU})

		for _, corrupted := range [][]byte{
			{0x0a, 0x0b, 0x00, 0x00, 0x00, 0x42}, // truncated sequence header
			{0x0a},                               // header only, no size
			{0x8a, 0x0b, 0x00},                   // forbidden bit set
		} {
			env.write(t, unit.PayloadAV1{corrupted, av1FrameOBU})
		}

		// the previous valid state is preserved
		sh := env.sequenceHeader()
		require.NotNil(t, sh)
		require.Equal(t, 1920, sh.Width())
		require.Equal(t, 1080, sh.Height())
	})

	t.Run("same sequence header is not reprocessed", func(t *testing.T) {
		env := newAV1TestEnv(t)
		defer env.close()

		env.write(t, unit.PayloadAV1{av1SequenceHeader1080p, av1FrameOBU})
		first := env.sequenceHeader()

		env.write(t, unit.PayloadAV1{av1SequenceHeader1080p, av1FrameOBU})

		// pointer stays stable when the sequence header did not change
		require.Same(t, first, env.sequenceHeader())
	})
}
