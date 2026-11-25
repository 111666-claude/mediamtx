// Package codecparams contains utilities to maintain codec parameter sets
// while forwarding streams.
package codecparams

import (
	"github.com/bluenviron/mediacommon/v2/pkg/bits"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
)

// H264SPSID returns the ID of a H264 SPS NALU.
// It returns false when the NALU is not a parseable SPS.
func H264SPSID(nalu []byte) (uint32, bool) {
	if len(nalu) < 1 || h264.NALUType(nalu[0]&0x1F) != h264.NALUTypeSPS {
		return 0, false
	}

	buf := h264.EmulationPreventionRemove(nalu[1:])
	pos := 0

	// profile_idc, constraint flags, reserved bits and level_idc
	_, err := bits.ReadBits(buf, &pos, 24)
	if err != nil {
		return 0, false
	}

	id, err := bits.ReadGolombUnsigned(buf, &pos)
	if err != nil {
		return 0, false
	}

	return id, true
}

// H264PPSID returns the ID of a H264 PPS NALU.
// It returns false when the NALU is not a parseable PPS.
func H264PPSID(nalu []byte) (uint32, bool) {
	if len(nalu) < 1 || h264.NALUType(nalu[0]&0x1F) != h264.NALUTypePPS {
		return 0, false
	}

	buf := h264.EmulationPreventionRemove(nalu[1:])
	pos := 0

	id, err := bits.ReadGolombUnsigned(buf, &pos)
	if err != nil {
		return 0, false
	}

	return id, true
}

// H264SlicePPSID returns the ID of the PPS referenced by a H264 slice NALU.
// It returns false when the NALU is not a slice or cannot be parsed.
func H264SlicePPSID(nalu []byte) (uint32, bool) {
	if len(nalu) < 1 {
		return 0, false
	}

	switch h264.NALUType(nalu[0] & 0x1F) {
	case h264.NALUTypeNonIDR,
		h264.NALUTypeDataPartitionA,
		h264.NALUTypeIDR,
		h264.NALUTypeSliceLayerWithoutPartitioning:

	default:
		return 0, false
	}

	buf := h264.EmulationPreventionRemove(nalu[1:])
	pos := 0

	// first_mb_in_slice
	_, err := bits.ReadGolombUnsigned(buf, &pos)
	if err != nil {
		return 0, false
	}

	// slice_type
	_, err = bits.ReadGolombUnsigned(buf, &pos)
	if err != nil {
		return 0, false
	}

	// pic_parameter_set_id
	id, err := bits.ReadGolombUnsigned(buf, &pos)
	if err != nil {
		return 0, false
	}

	return id, true
}
