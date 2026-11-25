package codecparams

// H264MultiPPSFixture is a fixed, minimal H264 sample made of one SPS and
// two PPS parameter sets, plus slices that reference each PPS in turn.
//
// All NALUs are syntactically valid enough for parameter set identifiers
// and slice PPS references to be parsed:
//   - SPS id 0 (baseline profile, level 4.0)
//   - PPS id 0 and PPS id 1
//   - non-IDR and IDR slices, each group referencing both PPS identifiers
type H264MultiPPSFixture struct {
	SPS    []byte
	PPS    [2][]byte
	NonIDR [2][]byte // non-IDR slices, indexed by the referenced PPS id
	IDR    [2][]byte // IDR slices, indexed by the referenced PPS id
}

// NewH264MultiPPSFixture builds the fixed sample.
func NewH264MultiPPSFixture() *H264MultiPPSFixture {
	sps := []byte{
		0x67, 0x42, 0xc0, 0x28, 0xd9, 0x00, 0x78, 0x02,
		0x27, 0xe5, 0x84, 0x00, 0x00, 0x03, 0x00, 0x04,
		0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60, 0xc9, 0x20,
	}

	// PPS payload 0x80 starts with ue(0), so its parameter set id is 0.
	pps0 := []byte{0x68, 0x80}
	// PPS payload 0x40 starts with ue(1) (bits "010"), so its id is 1.
	pps1 := []byte{0x68, 0x40}

	// Slice headers encode first_mb_in_slice=0, slice_type=2 (I-slice)
	// and pic_parameter_set_id.
	nonIDR0 := []byte{0x41, 0xb8} // pps_id 0
	nonIDR1 := []byte{0x41, 0xb4} // pps_id 1
	idr0 := []byte{0x65, 0xb8}    // pps_id 0
	idr1 := []byte{0x65, 0xb4}    // pps_id 1

	return &H264MultiPPSFixture{
		SPS:    sps,
		PPS:    [2][]byte{pps0, pps1},
		NonIDR: [2][]byte{nonIDR0, nonIDR1},
		IDR:    [2][]byte{idr0, idr1},
	}
}
