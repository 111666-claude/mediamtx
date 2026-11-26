package stream

import (
	"sync"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/unit"
)

const (
	alignTestVideoClockRate = 90000
	alignTestAudioClockRate = 48000
	alignTestGOP            = 4 * time.Second
	alignTestTimeout        = 5 * time.Second
)

// alignEnv is a fixed H264 + AAC stream with a GOP of 4 seconds.
type alignEnv struct {
	t          *testing.T
	strm       *Stream
	subStream  *SubStream
	desc       *description.Session
	videoMedia *description.Media
	audioMedia *description.Media
}

func newAlignEnv(t *testing.T, alignStart bool) *alignEnv {
	t.Helper()
	return newAlignEnvWithQueueSize(t, alignStart, 512)
}

func newAlignEnvWithQueueSize(t *testing.T, alignStart bool, queueSize int) *alignEnv {
	t.Helper()

	desc := &description.Session{Medias: []*description.Media{
		{
			Type: description.MediaTypeVideo,
			Formats: []format.Format{&format.H264{
				PayloadTyp:        96,
				PacketizationMode: 1,
			}},
		},
		{
			Type: description.MediaTypeAudio,
			Formats: []format.Format{&format.MPEG4Audio{
				PayloadTyp: 97,
				Config: &mpeg4audio.AudioSpecificConfig{
					Type:          mpeg4audio.ObjectTypeAACLC,
					SampleRate:    alignTestAudioClockRate,
					ChannelConfig: 2,
					ChannelCount:  2, //nolint:staticcheck
				},
			}},
		},
	}}

	strm := &Stream{
		OrigDesc:          desc,
		WriteQueueSize:    queueSize,
		RTPMaxPayloadSize: 1450,
		AlignStart:        alignStart,
		Parent:            &nilLogger{},
	}
	require.NoError(t, strm.Initialize())
	t.Cleanup(strm.Close)

	subStream := &SubStream{
		Stream:        strm,
		UseRTPPackets: false,
	}
	require.NoError(t, subStream.Initialize())

	return &alignEnv{
		t:          t,
		strm:       strm,
		subStream:  subStream,
		desc:       desc,
		videoMedia: desc.Medias[0],
		audioMedia: desc.Medias[1],
	}
}

func (e *alignEnv) videoFormat() format.Format {
	return e.videoMedia.Formats[0]
}

func (e *alignEnv) audioFormat() format.Format {
	return e.audioMedia.Formats[0]
}

// writeVideo writes one H264 access unit at the given PTS.
func (e *alignEnv) writeVideo(pts time.Duration, keyFrame bool) {
	au := unit.PayloadH264{{1, 2}} // non-IDR NALU
	if keyFrame {
		au = unit.PayloadH264{{5, 2}} // IDR NALU
	}

	e.subStream.WriteUnit(e.videoMedia, e.videoFormat(), &unit.Unit{
		PTS:     ptsToClock(pts, alignTestVideoClockRate),
		Payload: au,
	})
}

// writeAudio writes one AAC access unit at the given PTS.
func (e *alignEnv) writeAudio(pts time.Duration) {
	e.subStream.WriteUnit(e.audioMedia, e.audioFormat(), &unit.Unit{
		PTS:     ptsToClock(pts, alignTestAudioClockRate),
		Payload: unit.PayloadMPEG4Audio{{0x12, 0x10}},
	})
}

func ptsToClock(d time.Duration, clockRate int) int64 {
	return int64(d) * int64(clockRate) / int64(time.Second)
}

// alignReceiver collects the PTSs delivered to a reader and signals every
// reception through buffered channels.
type alignReceiver struct {
	mu       sync.Mutex
	videoPTS []time.Duration
	audioPTS []time.Duration
	videoCh  chan time.Duration
	audioCh  chan time.Duration
}

func newAlignReceiver() *alignReceiver {
	return &alignReceiver{
		videoCh: make(chan time.Duration, 8192),
		audioCh: make(chan time.Duration, 8192),
	}
}

func (r *alignReceiver) onVideo(u *unit.Unit) error {
	pts := ptsToDuration(u.PTS, alignTestVideoClockRate)
	r.mu.Lock()
	r.videoPTS = append(r.videoPTS, pts)
	r.mu.Unlock()
	r.videoCh <- pts
	return nil
}

func (r *alignReceiver) onAudio(u *unit.Unit) error {
	pts := ptsToDuration(u.PTS, alignTestAudioClockRate)
	r.mu.Lock()
	r.audioPTS = append(r.audioPTS, pts)
	r.mu.Unlock()
	r.audioCh <- pts
	return nil
}

func (r *alignReceiver) waitVideo(t *testing.T, n int) []time.Duration {
	t.Helper()
	for range n {
		select {
		case <-r.videoCh:
		case <-time.After(alignTestTimeout):
			t.Fatalf("timed out waiting for video unit %d", n)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]time.Duration, len(r.videoPTS))
	copy(out, r.videoPTS)
	return out
}

func (r *alignReceiver) waitAudio(t *testing.T, n int) []time.Duration {
	t.Helper()
	for range n {
		select {
		case <-r.audioCh:
		case <-time.After(alignTestTimeout):
			t.Fatalf("timed out waiting for audio unit %d", n)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]time.Duration, len(r.audioPTS))
	copy(out, r.audioPTS)
	return out
}

func (r *alignReceiver) snapshot() (video []time.Duration, audio []time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	video = make([]time.Duration, len(r.videoPTS))
	copy(video, r.videoPTS)
	audio = make([]time.Duration, len(r.audioPTS))
	copy(audio, r.audioPTS)
	return
}

func assertMonotonic(t *testing.T, values []time.Duration) {
	t.Helper()
	for i := 1; i < len(values); i++ {
		require.GreaterOrEqual(t, values[i], values[i-1])
	}
}

// newReader creates and registers a reader on the environment stream.
func (e *alignEnv) newReader(rcv *alignReceiver) *Reader {
	r := &Reader{Parent: &nilLogger{}}
	r.OnData(e.videoMedia, e.videoFormat(), rcv.onVideo)
	r.OnData(e.audioMedia, e.audioFormat(), rcv.onAudio)
	e.strm.AddReader(r)
	return r
}

// TestReaderAlignStart compares the startup offset of new readers between the
// default behavior and the aligned behavior, using a fixed stream with a GOP of
// 4 seconds. All units are written after the reader registration, as the
// stream does not replay history to new readers.
func TestReaderAlignStart(t *testing.T) {
	for _, align := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[align], func(t *testing.T) {
			env := newAlignEnvWithQueueSize(t, align, 4096)

			// the reader joins while the publisher is already running, right
			// after a keyframe, so the next one is 4 seconds away
			rcv := newAlignReceiver()
			r := env.newReader(rcv)
			defer env.strm.RemoveReader(r)

			// units received between two keyframes
			videoPTSs := make([]time.Duration, 15)
			for i := range videoPTSs {
				videoPTSs[i] = time.Duration(i+1) * 250 * time.Millisecond
			}
			audioPTSs := []time.Duration{
				500 * time.Millisecond,
				1500 * time.Millisecond,
				2500 * time.Millisecond,
				3500 * time.Millisecond,
			}

			for _, d := range videoPTSs {
				env.writeVideo(d, false)
			}
			for _, d := range audioPTSs {
				env.writeAudio(d)
			}
			// let the reader goroutine drain its queue, as a real-time
			// publisher naturally would
			time.Sleep(100 * time.Millisecond)

			if align {
				// nothing must be delivered before the first keyframe
				time.Sleep(100 * time.Millisecond)
				v, a := rcv.snapshot()
				require.Empty(t, v)
				require.Empty(t, a)
			} else {
				// default behavior: everything is delivered immediately, even
				// non-decodable video frames and audio preceding any video
				require.Eventually(t, func() bool {
					v, _ := rcv.snapshot()
					return len(v) == len(videoPTSs)
				}, alignTestTimeout, 5*time.Millisecond)
				_, earlyAudio := rcv.snapshot()
				require.Equal(t, 500*time.Millisecond, earlyAudio[0])
			}

			// next keyframe at t=4s, plus an audio unit at the same time
			env.writeVideo(alignTestGOP, true)
			env.writeAudio(alignTestGOP)

			// keep the stream going and check monotonicity on both tracks
			for i := range 15 {
				env.writeVideo(alignTestGOP+time.Duration(i+1)*250*time.Millisecond, false)
			}
			env.writeVideo(2*alignTestGOP, true)
			env.writeAudio(2 * alignTestGOP)
			time.Sleep(100 * time.Millisecond)

			var video, audio []time.Duration
			if align {
				// keyframe at 4s + 15 P-frames + keyframe at 8s, and audio
				// only at 4s and 8s
				require.Eventually(t, func() bool {
					v, a := rcv.snapshot()
					return len(v) == 17 && len(a) == 2
				}, alignTestTimeout, 5*time.Millisecond)
				video, audio = rcv.snapshot()
			} else {
				// all 30 P-frames plus the two keyframes, all 6 audio units
				require.Eventually(t, func() bool {
					v, a := rcv.snapshot()
					return len(v) == 32 && len(a) == 6
				}, alignTestTimeout, 5*time.Millisecond)
				video, audio = rcv.snapshot()
			}

			assertMonotonic(t, video)
			assertMonotonic(t, audio)

			if align {
				// first audio and first video share the keyframe PTS: the
				// startup offset is zero
				require.Equal(t, video[0], audio[0])
				require.Equal(t, alignTestGOP, video[0])
			} else {
				// audio starts 3.5 seconds before the first decodable frame
				require.Equal(t, 500*time.Millisecond, audio[0])
				require.Equal(t, alignTestGOP, video[15])
				require.Equal(t, alignTestGOP-500*time.Millisecond, video[15]-audio[0])
			}
		})
	}
}

// TestReaderAlignStartAudioOnly verifies that audio-only readers do not wait
// for any video keyframe, even when the stream contains a video track.
func TestReaderAlignStartAudioOnly(t *testing.T) {
	env := newAlignEnv(t, true)

	rcv := newAlignReceiver()
	r := &Reader{Parent: &nilLogger{}}
	r.OnData(env.audioMedia, env.audioFormat(), rcv.onAudio)
	env.strm.AddReader(r)
	defer env.strm.RemoveReader(r)

	// only non-keyframe video is ever published
	env.writeVideo(250*time.Millisecond, false)
	env.writeAudio(0)
	env.writeVideo(time.Second+250*time.Millisecond, false)
	env.writeAudio(time.Second)
	env.writeAudio(2 * time.Second)

	audio := rcv.waitAudio(t, 3)
	require.Equal(t, []time.Duration{0, time.Second, 2 * time.Second}, audio)

	video, _ := rcv.snapshot()
	require.Empty(t, video)
}

// TestReaderAlignStartLateKeyframe verifies that pre-keyframe video frames are
// dropped and buffered audio is trimmed to the keyframe PTS, including units
// arriving just before the keyframe.
func TestReaderAlignStartLateKeyframe(t *testing.T) {
	env := newAlignEnv(t, true)

	rcv := newAlignReceiver()
	r := env.newReader(rcv)

	env.writeVideo(250*time.Millisecond, false)
	for _, d := range []time.Duration{
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		3 * time.Second,
		3500 * time.Millisecond,
	} {
		env.writeVideo(d, false)
		env.writeAudio(d)
	}

	time.Sleep(200 * time.Millisecond)
	v, a := rcv.snapshot()
	require.Empty(t, v)
	require.Empty(t, a)

	// keyframe at t=4s: buffered audio strictly before it must be discarded,
	// the unit at t=4s written right after must be the first audio delivered
	env.writeVideo(alignTestGOP, true)
	env.writeAudio(alignTestGOP)

	firstVideo := rcv.waitVideo(t, 1)
	require.Equal(t, alignTestGOP, firstVideo[0])
	firstAudio := rcv.waitAudio(t, 1)
	require.Equal(t, alignTestGOP, firstAudio[0])

	env.strm.RemoveReader(r)
}

// TestReaderAlignStartMissingKeyframe verifies that the buffered-audio limit is
// bounded when no keyframe ever arrives, and that other readers are not
// blocked by the waiting reader.
func TestReaderAlignStartMissingKeyframe(t *testing.T) {
	env := newAlignEnvWithQueueSize(t, true, 1024)

	// an audio-only reader on the same stream keeps receiving every audio
	// unit, proving the waiting reader blocks nobody
	otherRcv := newAlignReceiver()
	other := &Reader{Parent: &nilLogger{}}
	other.OnData(env.audioMedia, env.audioFormat(), otherRcv.onAudio)
	env.strm.AddReader(other)
	defer env.strm.RemoveReader(other)

	// waiting reader: only non-keyframe video and audio are ever published
	waitingRcv := newAlignReceiver()
	waiting := env.newReader(waitingRcv)

	const totalUnits = maxBufferedAudioUnits + 128

	for i := range totalUnits {
		d := time.Duration(i) * 20 * time.Millisecond
		env.writeVideo(d, false)
		env.writeAudio(d)

		// let the reader goroutines drain their queues periodically
		if i%256 == 0 {
			time.Sleep(5 * time.Millisecond)
		}
	}

	// the audio-only reader must have received all units without being
	// blocked by the reader waiting for a keyframe
	require.Eventually(t, func() bool {
		_, a := otherRcv.snapshot()
		return len(a) == totalUnits
	}, alignTestTimeout, 10*time.Millisecond)

	// give the waiting reader time to drain its queue
	require.Eventually(t, func() bool {
		v, a := waitingRcv.snapshot()
		return len(v) == 0 && len(a) == 0
	}, alignTestTimeout, 10*time.Millisecond)

	// memory stays bounded: no more than the limit can be buffered, and the
	// reader must still be able to start once a keyframe finally arrives
	require.Equal(t, maxBufferedAudioUnits, waiting.aligner.bufferedAudioCount())
	require.Equal(t, uint64(totalUnits-maxBufferedAudioUnits), waiting.aligner.discardedAudioCount())

	env.writeVideo(alignTestGOP+time.Duration(totalUnits)*20*time.Millisecond, true)
	firstVideo := waitingRcv.waitVideo(t, 1)
	require.NotEmpty(t, firstVideo)

	env.strm.RemoveReader(waiting)
}

// TestReaderAlignStartExit verifies that a reader leaving while waiting for a
// keyframe terminates deterministically, releases its buffer and does not
// affect other readers.
func TestReaderAlignStartExit(t *testing.T) {
	env := newAlignEnvWithQueueSize(t, true, 512)

	// an audio-only reader on the same stream proves the leaving reader does
	// not affect anyone else
	otherRcv := newAlignReceiver()
	other := &Reader{Parent: &nilLogger{}}
	other.OnData(env.audioMedia, env.audioFormat(), otherRcv.onAudio)
	env.strm.AddReader(other)
	defer env.strm.RemoveReader(other)

	rcv := newAlignReceiver()
	r := env.newReader(rcv)

	for i := 0; i < 100; i++ {
		d := time.Duration(i) * 20 * time.Millisecond
		env.writeVideo(d, false)
		env.writeAudio(d)

		// let the reader goroutines drain their queues periodically
		if i%32 == 0 {
			time.Sleep(2 * time.Millisecond)
		}
	}

	// wait until some audio has actually been buffered
	require.Eventually(t, func() bool {
		return r.aligner.bufferedAudioCount() > 0
	}, alignTestTimeout, 10*time.Millisecond)

	done := make(chan struct{})
	go func() {
		env.strm.RemoveReader(r)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RemoveReader did not return while waiting for keyframe")
	}

	// the buffer has been released and the reader goroutine has terminated
	require.Zero(t, r.aligner.bufferedAudioCount())
	select {
	case err, ok := <-r.Error():
		require.False(t, ok)
		require.Nil(t, err)
	case <-time.After(time.Second):
		t.Fatal("reader did not terminate")
	}

	// the other reader is unaffected: it keeps receiving audio
	env.writeAudio(alignTestGOP)
	otherAudio := otherRcv.waitAudio(t, 101) // 100 during the loop + 1 now
	require.Len(t, otherAudio, 101)
}

// TestReaderAlignStartParameterSets verifies that parameter-set units do not
// count as keyframes and are dropped before start.
func TestReaderAlignStartParameterSets(t *testing.T) {
	env := newAlignEnv(t, true)

	rcv := newAlignReceiver()
	r := env.newReader(rcv)
	defer env.strm.RemoveReader(r)

	// SPS/PPS sent on their own, like some publishers do before an IDR
	env.subStream.WriteUnit(env.videoMedia, env.videoFormat(), &unit.Unit{
		PTS:     0,
		Payload: unit.PayloadH264{{7, 1}, {8, 1}},
	})
	env.writeAudio(0)

	time.Sleep(150 * time.Millisecond)
	v, a := rcv.snapshot()
	require.Empty(t, v)
	require.Empty(t, a)

	env.writeVideo(time.Second, true)
	firstVideo := rcv.waitVideo(t, 1)
	require.Equal(t, time.Second, firstVideo[0])
}

// TestStartAlignerFormats verifies keyframe detection and PTS trimming for a
// different video codec (H265) and several audio tracks with distinct clock
// rates, at the aligner level.
func TestStartAlignerFormats(t *testing.T) {
	videoMedia := &description.Media{
		Type:    description.MediaTypeVideo,
		Formats: []format.Format{&format.H265{}},
	}
	audioMedia1 := &description.Media{
		Type: description.MediaTypeAudio,
		Formats: []format.Format{&format.Opus{
			PayloadTyp:   96,
			ChannelCount: 2,
		}},
	}
	audioMedia2 := &description.Media{
		Type: description.MediaTypeAudio,
		Formats: []format.Format{&format.MPEG4Audio{
			PayloadTyp: 97,
			Config: &mpeg4audio.AudioSpecificConfig{
				Type:          mpeg4audio.ObjectTypeAACLC,
				SampleRate:    alignTestAudioClockRate,
				ChannelConfig: 2,
				ChannelCount:  2, //nolint:staticcheck
			},
		}},
	}

	videoFormat := videoMedia.Formats[0]
	opusFormat := audioMedia1.Formats[0]
	aacFormat := audioMedia2.Formats[0]

	aligner := newStartAligner(map[*description.Media]map[format.Format]OnDataFunc{
		videoMedia:  {videoFormat: func(*unit.Unit) error { return nil }},
		audioMedia1: {opusFormat: func(*unit.Unit) error { return nil }},
		audioMedia2: {aacFormat: func(*unit.Unit) error { return nil }},
	}, nil)

	require.True(t, aligner.hasVideo)

	// non-keyframe H265 frame is dropped
	out := aligner.process(videoFormat, &unit.Unit{
		PTS:     ptsToClock(250*time.Millisecond, 90000),
		Payload: unit.PayloadH265{{1, 2}}, // TRAIL_N, not a random access point
	})
	require.Nil(t, out)

	// audio from both tracks is buffered while waiting
	opusUnit := &unit.Unit{
		PTS:     ptsToClock(time.Second, opusFormat.ClockRate()),
		Payload: unit.PayloadOpus{{1, 2}},
	}
	require.Nil(t, aligner.process(opusFormat, opusUnit))

	aacUnit := &unit.Unit{
		PTS:     ptsToClock(2*time.Second, aacFormat.ClockRate()),
		Payload: unit.PayloadMPEG4Audio{{0x12, 0x10}},
	}
	require.Nil(t, aligner.process(aacFormat, aacUnit))
	require.Equal(t, 2, aligner.bufferedAudioCount())

	// H265 keyframe (IDR_W_RADL NALU type 19) flushes the buffered audio and
	// starts the stream; both buffered units are after the keyframe PTS and
	// are therefore delivered
	keyFrame := &unit.Unit{
		PTS:     ptsToClock(500*time.Millisecond, 90000),
		Payload: unit.PayloadH265{{(19 << 1) | 1, 2}},
	}
	out = aligner.process(videoFormat, keyFrame)
	require.Len(t, out, 3) // two buffered audio units + keyframe
	require.Same(t, opusUnit, out[0])
	require.Same(t, aacUnit, out[1])
	require.Same(t, keyFrame, out[2])

	// audio before the keyframe PTS is dropped after start
	early := &unit.Unit{
		PTS:     ptsToClock(400*time.Millisecond, aacFormat.ClockRate()),
		Payload: unit.PayloadMPEG4Audio{{0x12, 0x10}},
	}
	require.Nil(t, aligner.process(aacFormat, early))

	// audio at or after the keyframe PTS passes through
	later := &unit.Unit{
		PTS:     ptsToClock(time.Second, aacFormat.ClockRate()),
		Payload: unit.PayloadMPEG4Audio{{0x12, 0x10}},
	}
	require.Equal(t, []*unit.Unit{later}, aligner.process(aacFormat, later))
}
