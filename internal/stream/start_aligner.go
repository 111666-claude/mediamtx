package stream

import (
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/av1"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/vp8"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/vp9"

	"github.com/bluenviron/mediamtx/internal/unit"
)

// maxBufferedAudioUnits is the maximum number of audio units buffered while
// waiting for the first video keyframe. When the limit is reached, the oldest
// units are discarded, so memory usage stays bounded even if no keyframe ever
// arrives (for instance in case of a misconfigured publisher).
const maxBufferedAudioUnits = 4096

// ptsToDuration converts a PTS expressed in the clock rate of a format into
// a duration, so that PTSs of tracks with different clock rates can be
// compared.
func ptsToDuration(pts int64, clockRate int) time.Duration {
	secs := pts / int64(clockRate)
	dec := pts % int64(clockRate)
	return time.Duration(secs)*time.Second +
		time.Duration(dec)*time.Second/time.Duration(clockRate)
}

// isVideoKeyFrame checks whether a unit contains a decodable video keyframe
// (random access point). Units without a payload (parameter sets sent on their
// own) are not keyframes.
func isVideoKeyFrame(forma format.Format, u *unit.Unit) bool {
	if u.NilPayload() {
		return false
	}

	switch forma.(type) {
	case *format.H264:
		return h264.IsRandomAccess(u.Payload.(unit.PayloadH264))

	case *format.H265:
		return h265.IsRandomAccess(u.Payload.(unit.PayloadH265))

	case *format.AV1:
		return av1.IsRandomAccess2(u.Payload.(unit.PayloadAV1))

	case *format.VP9:
		return vp9.IsRandomAccess(u.Payload.(unit.PayloadVP9))

	case *format.VP8:
		return vp8.IsRandomAccess(u.Payload.(unit.PayloadVP8))

	case *format.MJPEG:
		// every JPEG frame is self-contained and therefore decodable
		return true

	default:
		// formats without a reliable keyframe detection (MPEG-1/2 Video,
		// MPEG-4 Video) are assumed to start with a decodable frame
		return true
	}
}

type alignedFormat struct {
	mediaType description.MediaType
	clockRate int
}

type bufferedAudioUnit struct {
	format format.Format
	unit   *unit.Unit
}

// startAligner delays audio units until the first decodable video keyframe is
// received, so that readers of streams with long GOPs do not receive seconds
// of audio before the first video frame. Process and the accessors are safe to
// call concurrently.
type startAligner struct {
	formats map[format.Format]*alignedFormat

	mutex          sync.Mutex
	hasVideo       bool
	started        bool
	keyFramePTS    time.Duration
	bufferedAudio  []*bufferedAudioUnit
	discardedAudio uint64
	onDiscard      func(uint64)
}

func newStartAligner(
	onDatas map[*description.Media]map[format.Format]OnDataFunc,
	onDiscard func(uint64),
) *startAligner {
	formats := make(map[format.Format]*alignedFormat)
	hasVideo := false

	for media, mediaFormats := range onDatas {
		if media.Type == description.MediaTypeVideo {
			hasVideo = true
		}

		for forma := range mediaFormats {
			formats[forma] = &alignedFormat{
				mediaType: media.Type,
				clockRate: forma.ClockRate(),
			}
		}
	}

	return &startAligner{
		formats:   formats,
		hasVideo:  hasVideo,
		onDiscard: onDiscard,
	}
}

// process returns the units that must be delivered to the reader callback for
// the given unit. A nil result means the unit must be dropped.
func (a *startAligner) process(forma format.Format, u *unit.Unit) []*unit.Unit {
	f := a.formats[forma]

	// tracks outside the alignment logic (application tracks) and audio-only
	// readers are passed through
	if f == nil || !a.hasVideo {
		return []*unit.Unit{u}
	}

	if f.mediaType == description.MediaTypeVideo {
		a.mutex.Lock()
		defer a.mutex.Unlock()

		if a.started {
			return []*unit.Unit{u}
		}

		if !isVideoKeyFrame(forma, u) {
			// the frame cannot be decoded without a previous keyframe
			return nil
		}

		a.keyFramePTS = ptsToDuration(u.PTS, f.clockRate)
		out := a.flushBufferedAudio()
		a.started = true
		return append(out, u)
	}

	// audio track
	a.mutex.Lock()
	defer a.mutex.Unlock()

	if a.started {
		if ptsToDuration(u.PTS, f.clockRate) < a.keyFramePTS {
			// audio preceding the first keyframe is not playable
			return nil
		}
		return []*unit.Unit{u}
	}

	// waiting for the first keyframe: buffer the audio unit, keeping the
	// buffer bounded
	if len(a.bufferedAudio) >= maxBufferedAudioUnits {
		a.bufferedAudio = a.bufferedAudio[1:]
		a.discardedAudio++
		if a.onDiscard != nil {
			a.onDiscard(a.discardedAudio)
		}
	}

	a.bufferedAudio = append(a.bufferedAudio, &bufferedAudioUnit{
		format: forma,
		unit:   u,
	})
	return nil
}

// bufferedAudioCount returns the number of audio units currently buffered
// while waiting for the first keyframe.
func (a *startAligner) bufferedAudioCount() int {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return len(a.bufferedAudio)
}

// discardedAudioCount returns the number of audio units discarded because the
// buffer limit was reached before the first keyframe.
func (a *startAligner) discardedAudioCount() uint64 {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.discardedAudio
}

// reset releases the buffered audio units.
func (a *startAligner) reset() {
	a.mutex.Lock()
	a.bufferedAudio = nil
	a.mutex.Unlock()
}

// flushBufferedAudio returns buffered audio units whose PTS is at or after
// the first keyframe PTS, in arrival order. Older units are discarded.
func (a *startAligner) flushBufferedAudio() []*unit.Unit {
	if len(a.bufferedAudio) == 0 {
		return nil
	}

	out := make([]*unit.Unit, 0, len(a.bufferedAudio))

	for _, bu := range a.bufferedAudio {
		f := a.formats[bu.format]
		if ptsToDuration(bu.unit.PTS, f.clockRate) >= a.keyFramePTS {
			out = append(out, bu.unit)
		}
	}

	a.bufferedAudio = nil
	return out
}
