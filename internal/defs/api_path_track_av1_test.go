package defs_test

import (
	"testing"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/av1"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/formatlabel"
)

func av1Media(forma *format.AV1) []*description.Media {
	return []*description.Media{{
		Type:    description.MediaTypeVideo,
		Formats: []format.Format{forma},
	}}
}

func TestMediasToTracksAV1Unknown(t *testing.T) {
	forma := &format.AV1{PayloadTyp: 96}

	tracks := defs.MediasToTracks(av1Media(forma), nil)

	// no valid sequence header yet: dimensions remain in an explicit unknown state
	require.Equal(t, []defs.APIPathTrack{{
		Codec: formatlabel.AV1,
		CodecProps: &defs.APIPathTrackCodecPropsAV1{
			Width:   0,
			Height:  0,
			Profile: 0,
			Level:   0,
			Tier:    0,
		},
	}}, tracks)
}

func intPtr(v int) *int {
	return &v
}

func TestMediasToTracksAV1NegotiatedParams(t *testing.T) {
	forma := &format.AV1{
		PayloadTyp: 96,
		Profile:    intPtr(1),
		LevelIdx:   intPtr(8),
		Tier:       intPtr(1),
	}

	tracks := defs.MediasToTracks(av1Media(forma), nil)

	// negotiated parameters are still exposed while no sequence header was seen
	require.Equal(t, &defs.APIPathTrackCodecPropsAV1{
		Width:   0,
		Height:  0,
		Profile: 1,
		Level:   8,
		Tier:    1,
	}, tracks[0].CodecProps)
}

func TestMediasToTracksAV1SequenceHeader(t *testing.T) {
	forma := &format.AV1{PayloadTyp: 96}

	// 1920x1080, profile 0, level 8, tier 0
	obu := []byte{0x0a, 0x0b, 0x00, 0x00, 0x00, 0x42, 0xab, 0xbf, 0xc3, 0x73, 0x2b, 0xe4, 0x01}

	var sh av1.SequenceHeader
	require.NoError(t, sh.Unmarshal(obu))

	tracks := defs.MediasToTracks(av1Media(forma), map[[2]int]*av1.SequenceHeader{
		{0, 0}: &sh,
	})

	require.Equal(t, &defs.APIPathTrackCodecPropsAV1{
		Width:   1920,
		Height:  1080,
		Profile: 0,
		Level:   8,
		Tier:    0,
	}, tracks[0].CodecProps)
}
