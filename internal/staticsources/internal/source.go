// Package internal contains the internal path static source.
package internal

import (
	"fmt"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/unit"
)

// checkCodecs verifies that the source only contains codecs supported by the
// internal source. Currently only H264 video and MPEG-4 Audio (AAC) are
// supported.
func checkCodecs(desc *description.Session) error {
	for _, medi := range desc.Medias {
		for _, forma := range medi.Formats {
			switch forma.(type) {
			case *format.H264, *format.MPEG4Audio:
			default:
				return fmt.Errorf("internal source only supports H264 and MPEG-4 Audio (AAC), found %T", forma)
			}
		}
	}
	return nil
}

// cloneUnit returns a deep copy of a unit.
// A unit received from the source stream is shared by every internal source
// reading that stream, while the target sub stream decodes RTP packets in
// place; each target therefore needs its own copy of the packets and payloads.
// This is a memory copy, not a re-encoding.
func cloneUnit(in *unit.Unit) *unit.Unit {
	out := &unit.Unit{
		PTS: in.PTS,
		NTP: in.NTP,
	}

	if in.RTPPackets != nil {
		out.RTPPackets = make([]*rtp.Packet, len(in.RTPPackets))
		for i, pkt := range in.RTPPackets {
			out.RTPPackets[i] = pkt.Clone()
		}
	}

	if !in.NilPayload() {
		switch payload := in.Payload.(type) {
		case unit.PayloadH264:
			cloned := make(unit.PayloadH264, len(payload))
			for i, au := range payload {
				cloned[i] = append([]byte(nil), au...)
			}
			out.Payload = cloned

		case unit.PayloadMPEG4Audio:
			cloned := make(unit.PayloadMPEG4Audio, len(payload))
			for i, au := range payload {
				cloned[i] = append([]byte(nil), au...)
			}
			out.Payload = cloned

		default:
			// only H264 and AAC are supported by the internal source
			out.Payload = in.Payload
		}
	}

	return out
}

type parent interface {
	logger.Writer
	SetReady(req defs.PathSourceStaticSetReadyReq) defs.PathSourceStaticSetReadyRes
	SetNotReady(req defs.PathSourceStaticSetNotReadyReq)
}

type pathManager interface {
	AddReader(req defs.PathAddReaderReq) (*defs.PathAddReaderRes, error)
}

// Source is an internal path static source.
// It reads the stream of another path of the same instance,
// without opening any network connection and without re-encoding.
type Source struct {
	PathManager pathManager
	Parent      parent
}

// Log implements logger.Writer.
func (s *Source) Log(level logger.Level, format string, args ...any) {
	s.Parent.Log(level, "[internal source] "+format, args...)
}

// Info implements StaticSource.
func (*Source) Info() defs.StaticSourceInfo {
	return defs.StaticSourceInfo{}
}

// APISourceDescribe implements StaticSource.
func (*Source) APISourceDescribe() *defs.APIPathSource {
	return &defs.APIPathSource{
		Type: defs.APIPathSourceTypeInternalSource,
		ID:   "",
	}
}

// Run implements StaticSource.
func (s *Source) Run(params defs.StaticSourceRunParams) error {
	sourcePathName := conf.InternalSourcePath(params.ResolvedSource)

	s.Log(logger.Debug, "reading path '%s'", sourcePathName)

	author := &sourceReader{
		parent: s,
	}

	res, err := s.PathManager.AddReader(defs.PathAddReaderReq{
		Author: author,
		AccessRequest: defs.PathAccessRequest{
			Name:     sourcePathName,
			SkipAuth: true,
		},
	})
	if err != nil {
		return err
	}
	defer res.Path.RemoveReader(defs.PathRemoveReaderReq{Author: author})

	sourceStream := res.Stream
	desc := sourceStream.OrigDesc

	err = checkCodecs(desc)
	if err != nil {
		return err
	}

	setReadyRes := s.Parent.SetReady(defs.PathSourceStaticSetReadyReq{
		Desc:          desc,
		UseRTPPackets: true,
		ReplaceNTP:    !params.Conf.UseAbsoluteTimestamp,
	})
	if setReadyRes.Err != nil {
		return setReadyRes.Err
	}

	subStream := setReadyRes.SubStream

	ready := true
	defer func() {
		if ready {
			s.Parent.SetNotReady(defs.PathSourceStaticSetNotReadyReq{})
		}
	}()

	reader := &stream.Reader{
		SkipOutboundBytes: true,
		Parent:            s,
	}

	// Register a callback for every media/format of the source stream.
	// The same description is passed to SetReady, so media and format
	// pointers are shared between the source stream and the target substream.
	for _, medi := range desc.Medias {
		for _, forma := range medi.Formats {
			cmedi := medi
			cforma := forma

			reader.OnData(cmedi, cforma, func(u *unit.Unit) error {
				subStream.WriteUnit(cmedi, cforma, cloneUnit(u))
				return nil
			})
		}
	}

	sourceStream.AddReader(reader)
	defer sourceStream.RemoveReader(reader)

	s.Log(logger.Info, "is reading from path '%s', %s",
		sourcePathName, defs.MediasInfo(desc.Medias))

	select {
	case <-reader.Error():
		return fmt.Errorf("source path '%s' has been closed", sourcePathName)

	case <-sourceStream.Terminated():
		return fmt.Errorf("source path '%s' has been closed", sourcePathName)

	case <-params.ReloadConf:
		return nil

	case <-params.Context.Done():
		return nil
	}
}

// sourceReader is the defs.Reader used to read the source path.
type sourceReader struct {
	parent logger.Writer
}

// Log implements logger.Writer.
func (r *sourceReader) Log(level logger.Level, format string, args ...any) {
	r.parent.Log(level, format, args...)
}

// Close implements defs.Reader.
func (*sourceReader) Close() {
}

// APIReaderDescribe implements defs.Reader.
func (*sourceReader) APIReaderDescribe() *defs.APIPathReader {
	return &defs.APIPathReader{
		Type: defs.APIPathReaderTypeHidden,
		ID:   "",
	}
}
