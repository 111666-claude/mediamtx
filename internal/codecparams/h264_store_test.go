package codecparams

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestH264IDs(t *testing.T) {
	fx := NewH264MultiPPSFixture()

	id, ok := H264SPSID(fx.SPS)
	require.True(t, ok)
	require.Equal(t, uint32(0), id)

	id, ok = H264PPSID(fx.PPS[0])
	require.True(t, ok)
	require.Equal(t, uint32(0), id)

	id, ok = H264PPSID(fx.PPS[1])
	require.True(t, ok)
	require.Equal(t, uint32(1), id)

	for i, expected := range []uint32{0, 1} {
		id, ok = H264SlicePPSID(fx.NonIDR[i])
		require.True(t, ok)
		require.Equal(t, expected, id)

		id, ok = H264SlicePPSID(fx.IDR[i])
		require.True(t, ok)
		require.Equal(t, expected, id)
	}
}

func TestH264IDsErrors(t *testing.T) {
	// wrong NALU types
	_, ok := H264SPSID([]byte{0x68, 0x80})
	require.False(t, ok)

	_, ok = H264PPSID([]byte{0x67, 0x42})
	require.False(t, ok)

	_, ok = H264SlicePPSID([]byte{0x06, 0x00}) // SEI
	require.False(t, ok)

	// truncated NALUs
	_, ok = H264SPSID([]byte{0x67})
	require.False(t, ok)

	_, ok = H264PPSID([]byte{0x68})
	require.False(t, ok)

	_, ok = H264SlicePPSID([]byte{0x65})
	require.False(t, ok)

	_, ok = H264SlicePPSID(nil)
	require.False(t, ok)
}

func TestH264StoreKeepsEveryParameterSet(t *testing.T) {
	fx := NewH264MultiPPSFixture()
	store := NewH264Store(nil, nil)

	// first keyframe introduces the SPS and the first PPS
	sps, pps, changed := store.Update([][]byte{fx.SPS, fx.PPS[0], fx.IDR[0]})
	require.True(t, changed)
	require.Equal(t, fx.SPS, sps)
	require.Equal(t, fx.PPS[0], pps)

	// re-sending the same parameter sets is a no-op
	_, _, changed = store.Update([][]byte{fx.SPS, fx.PPS[0], fx.NonIDR[0]})
	require.False(t, changed)

	// the second PPS updates the format without removing the first one
	sps, pps, changed = store.Update([][]byte{fx.PPS[1], fx.NonIDR[1]})
	require.True(t, changed)
	require.Equal(t, fx.SPS, sps)
	require.Equal(t, fx.PPS[1], pps)

	got0, ok := store.PPS(0)
	require.True(t, ok)
	require.Equal(t, fx.PPS[0], got0)

	got1, ok := store.PPS(1)
	require.True(t, ok)
	require.Equal(t, fx.PPS[1], got1)

	// keyframe injection must contain the SPS and both PPS sets
	require.Equal(t, [][]byte{fx.SPS, fx.PPS[0], fx.PPS[1]}, store.ParameterSets())

	// replacing a parameter set with the same identifier must not duplicate it
	pps1Replacement := []byte{0x68, 0x44} // still pps_id 1, different payload
	_, _, changed = store.Update([][]byte{pps1Replacement})
	require.True(t, changed)
	require.Equal(t, [][]byte{fx.SPS, fx.PPS[0], pps1Replacement}, store.ParameterSets())
}

func TestH264StoreInitialSets(t *testing.T) {
	fx := NewH264MultiPPSFixture()

	store := NewH264Store(fx.SPS, fx.PPS[0])
	require.Equal(t, [][]byte{fx.SPS, fx.PPS[0]}, store.ParameterSets())

	got, ok := store.PPS(0)
	require.True(t, ok)
	require.Equal(t, fx.PPS[0], got)

	// receiving the same initial sets again must not mark anything as changed
	_, _, changed := store.Update([][]byte{fx.SPS, fx.PPS[0]})
	require.False(t, changed)
}

func TestH264StoreInvalidFallback(t *testing.T) {
	// a truncated PPS cannot be parsed and is kept as a fallback only
	invalidPPS := []byte{0x68}
	store := NewH264Store(nil, invalidPPS)

	_, ok := store.PPS(0)
	require.False(t, ok)
	require.Equal(t, [][]byte{invalidPPS}, store.ParameterSets())

	// once a valid set arrives, the fallback is no longer injected
	fx := NewH264MultiPPSFixture()
	_, _, changed := store.Update([][]byte{fx.PPS[0]})
	require.True(t, changed)
	require.Equal(t, [][]byte{fx.PPS[0]}, store.ParameterSets())

	// repeated invalid sets are ignored: they are neither re-inserted
	// nor reported as a change
	_, _, changed = store.Update([][]byte{invalidPPS})
	require.False(t, changed)
	require.Equal(t, [][]byte{fx.PPS[0]}, store.ParameterSets())

	// a different invalid set replaces the fallback mirror but is still
	// not injected while valid sets exist
	otherInvalidPPS := []byte{0x68, 0x01}
	_, pps, changed := store.Update([][]byte{otherInvalidPPS})
	require.True(t, changed)
	require.Equal(t, otherInvalidPPS, pps)
	require.Equal(t, [][]byte{fx.PPS[0]}, store.ParameterSets())
}
