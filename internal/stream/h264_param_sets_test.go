package stream_test

import (
	"testing"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/codecparams"
	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/unit"
)

func countH264NALUs(au unit.PayloadH264, typ h264.NALUType) int {
	n := 0
	for _, nalu := range au {
		if h264.NALUType(nalu[0]&0x1F) == typ {
			n++
		}
	}
	return n
}

// TestH264MultipleParameterSets verifies that a publisher using several
// PPS sets in the same stream does not lose a parameter set when another
// one is updated, and that readers receive decodable keyframes containing
// every parameter set referenced by the slices.
func TestH264MultipleParameterSets(t *testing.T) {
	fx := codecparams.NewH264MultiPPSFixture()

	forma := &format.H264{
		PayloadTyp:        96,
		PacketizationMode: 1,
		SPS:               fx.SPS,
		PPS:               fx.PPS[0],
	}

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
	defer strm.Close()

	subStream := &stream.SubStream{
		Stream:        strm,
		UseRTPPackets: false,
	}
	err = subStream.Initialize()
	require.NoError(t, err)

	r := &stream.Reader{}
	received := make(chan *unit.Unit, 16)

	r.OnData(media, forma, func(u *unit.Unit) error {
		received <- u
		return nil
	})

	strm.AddReader(r)
	defer strm.RemoveReader(r)

	write := func(pts int64, payload unit.PayloadH264) *unit.Unit {
		subStream.WriteUnit(media, forma, &unit.Unit{
			PTS:     pts,
			Payload: payload,
		})

		u := <-received
		require.Equal(t, pts, u.PTS)
		return u
	}

	// 1. first keyframe: SPS + PPS 0 + IDR referencing PPS 0
	u := write(0, unit.PayloadH264{fx.SPS, fx.PPS[0], fx.IDR[0]})
	out := u.Payload.(unit.PayloadH264)
	require.Equal(t, 1, countH264NALUs(out, h264.NALUTypeSPS))
	require.Equal(t, 1, countH264NALUs(out, h264.NALUTypePPS))
	require.Equal(t, 1, countH264NALUs(out, h264.NALUTypeIDR))
	require.Equal(t, unit.PayloadH264{fx.SPS, fx.PPS[0], fx.IDR[0]}, out)

	// 2. inter frame referencing PPS 0: no parameter set is injected
	u = write(3000, unit.PayloadH264{fx.NonIDR[0]})
	out = u.Payload.(unit.PayloadH264)
	require.Equal(t, 0, countH264NALUs(out, h264.NALUTypeSPS))
	require.Equal(t, 0, countH264NALUs(out, h264.NALUTypePPS))
	require.Equal(t, unit.PayloadH264{fx.NonIDR[0]}, out)

	// 3. the publisher introduces PPS 1 and sends a slice referencing it.
	// PPS 0 must be retained even though the format now points at PPS 1.
	u = write(6000, unit.PayloadH264{fx.PPS[1], fx.NonIDR[1]})
	require.Equal(t, unit.PayloadH264{fx.NonIDR[1]}, u.Payload.(unit.PayloadH264))

	outDesc := strm.OutDescCopy()
	outFormat := outDesc.Medias[0].Formats[0].(*format.H264)
	require.Equal(t, fx.SPS, outFormat.SPS)
	require.Equal(t, fx.PPS[1], outFormat.PPS)

	// 4. keyframe with slices referencing both PPS sets: both parameter
	// sets must be prepended so any reader can decode every slice
	u = write(9000, unit.PayloadH264{
		fx.SPS, fx.PPS[0], fx.PPS[1], fx.IDR[0], fx.IDR[1],
	})
	out = u.Payload.(unit.PayloadH264)
	require.Equal(t, 1, countH264NALUs(out, h264.NALUTypeSPS))
	require.Equal(t, 2, countH264NALUs(out, h264.NALUTypePPS))
	require.Equal(t, 2, countH264NALUs(out, h264.NALUTypeIDR))
	require.Equal(t, unit.PayloadH264{
		fx.SPS, fx.PPS[0], fx.PPS[1], fx.IDR[0], fx.IDR[1],
	}, out)

	// every slice must find the PPS it references among the prepended sets
	ppsByID := map[uint32]bool{}
	for _, nalu := range out {
		if h264.NALUType(nalu[0]&0x1F) == h264.NALUTypePPS {
			id, ok := codecparams.H264PPSID(nalu)
			require.True(t, ok)
			ppsByID[id] = true
		}
	}
	for _, nalu := range out {
		if h264.NALUType(nalu[0]&0x1F) == h264.NALUTypeIDR {
			id, ok := codecparams.H264SlicePPSID(nalu)
			require.True(t, ok)
			require.True(t, ppsByID[id], "missing PPS %d referenced by slice", id)
		}
	}

	// 5. a slice still referencing PPS 0 must keep working: PPS 0 was
	// not dropped when PPS 1 arrived
	u = write(12000, unit.PayloadH264{fx.NonIDR[0]})
	require.Equal(t, unit.PayloadH264{fx.NonIDR[0]}, u.Payload.(unit.PayloadH264))

	// 6. duplicate parameter sets are neither stored twice nor cause a
	// description update
	u = write(15000, unit.PayloadH264{fx.SPS, fx.PPS[0], fx.PPS[1], fx.IDR[0]})
	out = u.Payload.(unit.PayloadH264)
	require.Equal(t, 1, countH264NALUs(out, h264.NALUTypeSPS))
	require.Equal(t, 2, countH264NALUs(out, h264.NALUTypePPS))
	require.Equal(t, unit.PayloadH264{
		fx.SPS, fx.PPS[0], fx.PPS[1], fx.IDR[0],
	}, out)

	outDesc = strm.OutDescCopy()
	outFormat = outDesc.Medias[0].Formats[0].(*format.H264)
	require.Equal(t, fx.PPS[1], outFormat.PPS)
}

// TestH264ParameterSetsReconnect verifies that a publisher reconnecting
// (the new publisher restarts the stream with a fresh initial keyframe)
// keeps decodable output: the parameter set store survives, duplicate sets
// are inserted only once and timestamps are preserved.
func TestH264ParameterSetsReconnect(t *testing.T) {
	fx := codecparams.NewH264MultiPPSFixture()

	forma := &format.H264{
		PayloadTyp:        96,
		PacketizationMode: 1,
		SPS:               fx.SPS,
		PPS:               fx.PPS[0],
	}
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
	defer strm.Close()

	subStream := &stream.SubStream{Stream: strm, UseRTPPackets: false}
	err = subStream.Initialize()
	require.NoError(t, err)

	received := make(chan *unit.Unit, 8)
	r := &stream.Reader{}
	r.OnData(media, forma, func(u *unit.Unit) error {
		received <- u
		return nil
	})
	strm.AddReader(r)
	defer strm.RemoveReader(r)

	// first publisher introduces both PPS sets
	subStream.WriteUnit(media, forma, &unit.Unit{
		PTS:     0,
		Payload: unit.PayloadH264{fx.SPS, fx.PPS[0], fx.PPS[1], fx.IDR[0], fx.IDR[1]},
	})
	<-received

	// the publisher reconnects and restarts at PTS 0, declaring only PPS 0
	subStream.WriteUnit(media, forma, &unit.Unit{
		PTS:     0,
		Payload: unit.PayloadH264{fx.SPS, fx.PPS[0], fx.IDR[0]},
	})
	u := <-received
	require.Equal(t, int64(0), u.PTS)

	out := u.Payload.(unit.PayloadH264)
	// both parameter sets remain available, the duplicate SPS/PPS 0 are
	// inserted only once
	require.Equal(t, unit.PayloadH264{
		fx.SPS, fx.PPS[0], fx.PPS[1], fx.IDR[0],
	}, out)
}

// TestH264SingleParameterSet verifies the common single-PPS case keeps the
// exact pre-fix behavior: one SPS and one PPS on keyframes, nothing on
// inter frames.
func TestH264SingleParameterSet(t *testing.T) {
	fx := codecparams.NewH264MultiPPSFixture()

	forma := &format.H264{
		PayloadTyp:        96,
		PacketizationMode: 1,
		SPS:               fx.SPS,
		PPS:               fx.PPS[0],
	}
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
	defer strm.Close()

	subStream := &stream.SubStream{Stream: strm, UseRTPPackets: false}
	err = subStream.Initialize()
	require.NoError(t, err)

	received := make(chan *unit.Unit, 4)
	r := &stream.Reader{}
	r.OnData(media, forma, func(u *unit.Unit) error {
		received <- u
		return nil
	})
	strm.AddReader(r)
	defer strm.RemoveReader(r)

	write := func(pts int64, payload unit.PayloadH264) *unit.Unit {
		subStream.WriteUnit(media, forma, &unit.Unit{PTS: pts, Payload: payload})
		u := <-received
		require.Equal(t, pts, u.PTS)
		return u
	}

	u := write(0, unit.PayloadH264{fx.SPS, fx.PPS[0], fx.IDR[0]})
	require.Equal(t, unit.PayloadH264{fx.SPS, fx.PPS[0], fx.IDR[0]},
		u.Payload.(unit.PayloadH264))

	u = write(3000, unit.PayloadH264{fx.NonIDR[0]})
	require.Equal(t, unit.PayloadH264{fx.NonIDR[0]}, u.Payload.(unit.PayloadH264))

	outDesc := strm.OutDescCopy()
	outFormat := outDesc.Medias[0].Formats[0].(*format.H264)
	require.Equal(t, fx.SPS, outFormat.SPS)
	require.Equal(t, fx.PPS[0], outFormat.PPS)
}
