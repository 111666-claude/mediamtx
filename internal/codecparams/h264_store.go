package codecparams

import (
	"bytes"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
)

// H264Store keeps all the H264 SPS and PPS parameter sets seen in a stream,
// indexed by their parameter set identifier.
//
// Unlike a single SPS/PPS pair, updating one parameter set never removes
// another one, so slices referencing any previously received set stay
// decodable.
type H264Store struct {
	sps      map[uint32][]byte
	pps      map[uint32][]byte
	spsOrder []uint32
	ppsOrder []uint32

	// last parameter sets that could not be parsed or identified.
	// They are only used as a fallback when no valid set is available,
	// in order to preserve the previous single-set behavior.
	legacySPS []byte
	legacyPPS []byte
}

// NewH264Store allocates a H264Store and imports the parameter sets
// contained in the initial format configuration, when present.
func NewH264Store(initialSPS []byte, initialPPS []byte) *H264Store {
	s := &H264Store{
		sps: make(map[uint32][]byte),
		pps: make(map[uint32][]byte),
	}

	if initialSPS != nil {
		if id, ok := H264SPSID(initialSPS); ok {
			s.addSPS(id, initialSPS)
		} else {
			s.legacySPS = initialSPS
		}
	}

	if initialPPS != nil {
		if id, ok := H264PPSID(initialPPS); ok {
			s.addPPS(id, initialPPS)
		} else {
			s.legacyPPS = initialPPS
		}
	}

	return s
}

func (s *H264Store) addSPS(id uint32, nalu []byte) {
	if _, ok := s.sps[id]; !ok {
		s.spsOrder = append(s.spsOrder, id)
	}
	s.sps[id] = nalu
}

func (s *H264Store) addPPS(id uint32, nalu []byte) {
	if _, ok := s.pps[id]; !ok {
		s.ppsOrder = append(s.ppsOrder, id)
	}
	s.pps[id] = nalu
}

// Update ingests the parameter sets contained in an access unit.
// It ignores invalid and duplicate sets.
// It returns the most recently received SPS and PPS (meant to be mirrored
// into the stream format) and true when one of them changed.
func (s *H264Store) Update(au [][]byte) (sps []byte, pps []byte, changed bool) {
	sps = s.latestSPS()
	pps = s.latestPPS()

	for _, nalu := range au {
		if len(nalu) == 0 {
			continue
		}

		switch h264.NALUType(nalu[0] & 0x1F) {
		case h264.NALUTypeSPS:
			if id, ok := H264SPSID(nalu); ok {
				if existing, ok := s.sps[id]; ok && bytes.Equal(existing, nalu) {
					continue
				}

				s.addSPS(id, nalu)
			} else {
				if bytes.Equal(s.legacySPS, nalu) {
					continue
				}

				s.legacySPS = nalu
			}

			sps = nalu
			changed = true

		case h264.NALUTypePPS:
			if id, ok := H264PPSID(nalu); ok {
				if existing, ok := s.pps[id]; ok && bytes.Equal(existing, nalu) {
					continue
				}

				s.addPPS(id, nalu)
			} else {
				if bytes.Equal(s.legacyPPS, nalu) {
					continue
				}

				s.legacyPPS = nalu
			}

			pps = nalu
			changed = true
		}
	}

	return sps, pps, changed
}

func (s *H264Store) latestSPS() []byte {
	if len(s.spsOrder) != 0 {
		return s.sps[s.spsOrder[len(s.spsOrder)-1]]
	}
	return s.legacySPS
}

func (s *H264Store) latestPPS() []byte {
	if len(s.ppsOrder) != 0 {
		return s.pps[s.ppsOrder[len(s.ppsOrder)-1]]
	}
	return s.legacyPPS
}

// ParameterSets returns every SPS followed by every PPS currently tracked,
// in reception order. It is meant to be prepended to random access frames.
// Fallback sets that could not be identified are only returned when the
// store contains no valid set of the corresponding type.
func (s *H264Store) ParameterSets() [][]byte {
	out := make([][]byte, 0, len(s.spsOrder)+len(s.ppsOrder)+2)

	if len(s.spsOrder) != 0 {
		for _, id := range s.spsOrder {
			out = append(out, s.sps[id])
		}
	} else if s.legacySPS != nil {
		out = append(out, s.legacySPS)
	}

	if len(s.ppsOrder) != 0 {
		for _, id := range s.ppsOrder {
			out = append(out, s.pps[id])
		}
	} else if s.legacyPPS != nil {
		out = append(out, s.legacyPPS)
	}

	return out
}

// PPS returns the PPS with the given identifier, or false when it is unknown.
func (s *H264Store) PPS(id uint32) ([]byte, bool) {
	nalu, ok := s.pps[id]
	return nalu, ok
}
