package stream

import (
	"fmt"
	"reflect"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"

	"github.com/bluenviron/mediamtx/internal/formatlabel"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/unit"
)

func formatMPEG4AudioConfig(asc *mpeg4audio.AudioSpecificConfig) string {
	return fmt.Sprintf("type=%d, sampleRate=%d, channelCount=%d",
		asc.Type, asc.SampleRate, asc.ChannelConfig)
}

func formatG711Config(f *format.G711) string {
	return fmt.Sprintf("MULaw=%v, sampleRate=%d, channelCount=%d",
		f.MULaw, f.SampleRate, f.ChannelCount)
}

func formatLPCMConfig(f *format.LPCM) string {
	return fmt.Sprintf("bitDepth=%d, sampleRate=%d, channelCount=%d",
		f.BitDepth, f.SampleRate, f.ChannelCount)
}

// mediaIsGeneric returns true when a media only contains generic (data) formats.
func mediaIsGeneric(medi *description.Media) bool {
	if len(medi.Formats) == 0 {
		return false
	}

	for _, forma := range medi.Formats {
		if _, ok := forma.(*format.Generic); !ok {
			return false
		}
	}

	return true
}

// filterGenericMedias removes medias that only contain generic (data) formats.
func filterGenericMedias(medias []*description.Media) []*description.Media {
	out := make([]*description.Media, 0, len(medias))

	for _, medi := range medias {
		if !mediaIsGeneric(medi) {
			out = append(out, medi)
		}
	}

	return out
}

func mediasAreCompatible(medias1 []*description.Media, medias2 []*description.Media) error {
	if len(medias1) != len(medias2) {
		return fmt.Errorf("wants to publish %v, but stream expects %v",
			formatlabel.MediasToLabels(medias2), formatlabel.MediasToLabels(medias1))
	}

	for i := range medias1 {
		if len(medias1[i].Formats) != len(medias2[i].Formats) {
			return fmt.Errorf("wants to publish %v, but stream expects %v",
				formatlabel.MediasToLabels(medias2), formatlabel.MediasToLabels(medias1))
		}

		for j := range medias1[i].Formats {
			if reflect.TypeOf(medias1[i].Formats[j]) != reflect.TypeOf(medias2[i].Formats[j]) {
				return fmt.Errorf("wants to publish %v, but stream expects %v",
					formatlabel.MediasToLabels(medias2), formatlabel.MediasToLabels(medias1))
			}
		}
	}

	for i := range medias1 {
		for j := range medias1[i].Formats {
			switch format1 := medias1[i].Formats[j].(type) {
			case *format.MPEG4Audio:
				format2 := medias2[i].Formats[j].(*format.MPEG4Audio)

				if !reflect.DeepEqual(format1.Config, format2.Config) {
					return fmt.Errorf("MPEG-4 audio configuration does not match, is %s, but stream expects %s",
						formatMPEG4AudioConfig(format2.Config), formatMPEG4AudioConfig(format1.Config))
				}

			case *format.G711:
				format2 := medias2[i].Formats[j].(*format.G711)

				if format1.MULaw != format2.MULaw ||
					format1.SampleRate != format2.SampleRate ||
					format1.ChannelCount != format2.ChannelCount {
					return fmt.Errorf("G711 configuration does not match, is %s, but stream expects %s",
						formatG711Config(format2), formatG711Config(format1))
				}

			case *format.LPCM:
				format2 := medias2[i].Formats[j].(*format.LPCM)

				if format1.BitDepth != format2.BitDepth ||
					format1.SampleRate != format2.SampleRate ||
					format1.ChannelCount != format2.ChannelCount {
					return fmt.Errorf("LPCM configuration does not match, is %s, but stream expects %s",
						formatLPCMConfig(format2), formatLPCMConfig(format1))
				}
			}
		}
	}

	return nil
}

// SubStream is a Stream without interruptions.
type SubStream struct {
	Stream        *Stream
	InDesc        *description.Session
	UseRTPPackets bool

	medias map[*description.Media]*subStreamMedia
}

// Initialize initializes the SubStream.
func (ss *SubStream) Initialize() error {
	var inMedias []*description.Media

	if !ss.Stream.AlwaysAvailable {
		if ss.Stream.subStream != nil {
			panic("should not happen")
		}

		if ss.InDesc != nil {
			panic("should not happen")
		}

		ss.InDesc = ss.Stream.OrigDesc
		inMedias = ss.InDesc.Medias
	} else {
		if ss.InDesc == nil {
			panic("should not happen")
		}

		inMedias = ss.InDesc.Medias

		if ss.Stream.AlwaysAvailableIgnoreGenericTracks {
			// ignore additional generic (data) tracks of the source,
			// that are not part of the declared tracks.
			inMedias = filterGenericMedias(inMedias)
		}

		err := mediasAreCompatible(ss.Stream.OrigDesc.Medias, inMedias)
		if err != nil {
			return err
		}
	}

	ss.medias = make(map[*description.Media]*subStreamMedia)

	for i, inMedia := range inMedias {
		origMedia := ss.Stream.OrigDesc.Medias[i]

		ssm := &subStreamMedia{
			inMedia:       inMedia,
			streamMedia:   ss.Stream.medias[origMedia],
			useRTPPackets: ss.UseRTPPackets,
		}
		err := ssm.initialize()
		if err != nil {
			return err
		}
		ss.medias[inMedia] = ssm
	}

	if ss.Stream.AlwaysAvailable {
		if ss.Stream.offlineSubStream != nil {
			ss.Stream.Parent.Log(logger.Info, "stream is online")

			// wait for the entire duration of the last sample of the offline sub stream
			// to minimize errors in clients.
			// TODO: it would be better in the future to wait for the last sample
			// of normal sub streams as well (this is currently impossible because
			// we don't know the duration of their samples).
			ss.Stream.offlineSubStream.close(true)
			ss.Stream.offlineSubStream = nil
		}
	}

	// keep mutex open to use writeUnit() inside initialize2()
	ss.Stream.mutex.Lock()
	defer ss.Stream.mutex.Unlock()

	ss.Stream.subStream = ss

	for _, ssm := range ss.medias {
		for _, ssf := range ssm.formats {
			ssf.initialize2(ss.Stream.firstTimeReceived, ss.Stream.lastPTS, ss.Stream.lastSystemTime)
		}
	}

	return nil
}

// WriteUnit writes a Unit.
func (ss *SubStream) WriteUnit(inMedia *description.Media, inFormat format.Format, u *unit.Unit) {
	ss.Stream.mutex.RLock()
	defer ss.Stream.mutex.RUnlock()

	if ss.Stream.subStream != ss {
		return
	}

	ssm, ok := ss.medias[inMedia]
	if !ok {
		// the media is not part of the sub stream:
		// this happens with generic (data) tracks ignored through
		// alwaysAvailableIgnoreGenericTracks.
		return
	}

	ssf := ssm.formats[inFormat]

	ssf.writeUnit(u)
}
