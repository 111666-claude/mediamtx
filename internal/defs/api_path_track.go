package defs

import (
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/av1"

	"github.com/bluenviron/mediamtx/internal/formatlabel"
)

// APIPathTrack is a track.
type APIPathTrack struct {
	Codec      APIPathTrackCodec      `json:"codec"`
	CodecProps APIPathTrackCodecProps `json:"codecProps"`
}

func formatToTrack(outFormat format.Format, av1SequenceHeader *av1.SequenceHeader) APIPathTrack {
	return APIPathTrack{
		Codec:      formatlabel.FormatToLabel(outFormat),
		CodecProps: formatToTrackCodecProps(outFormat, av1SequenceHeader),
	}
}

func gatherFormats(medias []*description.Media) []format.Format {
	n := 0
	for _, media := range medias {
		n += len(media.Formats)
	}

	if n == 0 {
		return nil
	}

	formats := make([]format.Format, n)
	n = 0

	for _, media := range medias {
		n += copy(formats[n:], media.Formats)
	}

	return formats
}

// MediasToTracks returns tracks of given medias.
// av1SequenceHeaders contains the last valid AV1 sequence headers,
// indexed by media index and format index.
func MediasToTracks(
	outMedias []*description.Media,
	av1SequenceHeaders map[[2]int]*av1.SequenceHeader,
) []APIPathTrack {
	outFormats := gatherFormats(outMedias)
	ret := make([]APIPathTrack, len(outFormats))

	i := 0

	for mediaIdx, media := range outMedias {
		for formatIdx, outFormat := range media.Formats {
			ret[i] = formatToTrack(outFormat, av1SequenceHeaders[[2]int{mediaIdx, formatIdx}])
			i++
		}
	}

	return ret
}
