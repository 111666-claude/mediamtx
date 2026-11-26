package internal_test

import (
	"context"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/externalcmd"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/staticsources/internal"
	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/test"
	"github.com/bluenviron/mediamtx/internal/unit"
)

type dummyPath struct{}

func (*dummyPath) Name() string                                  { return "source" }
func (*dummyPath) SafeConf() *conf.Path                          { return nil }
func (*dummyPath) ExternalCmdEnv() externalcmd.Environment       { return nil }
func (*dummyPath) RemovePublisher(_ defs.PathRemovePublisherReq) {}
func (*dummyPath) RemoveReader(_ defs.PathRemoveReaderReq)       {}

type parentMock struct {
	setReady    chan defs.PathSourceStaticSetReadyReq
	setNotReady chan struct{}
	stream      *stream.Stream
	subStream   *stream.SubStream
}

func newParentMock() *parentMock {
	return &parentMock{
		setReady:    make(chan defs.PathSourceStaticSetReadyReq, 1),
		setNotReady: make(chan struct{}, 1),
	}
}

func (*parentMock) Log(logger.Level, string, ...any) {}

func (m *parentMock) SetReady(req defs.PathSourceStaticSetReadyReq) defs.PathSourceStaticSetReadyRes {
	m.stream = &stream.Stream{
		OrigDesc:          req.Desc,
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		ReplaceNTP:        req.ReplaceNTP,
		Parent:            test.NilLogger,
	}
	err := m.stream.Initialize()
	if err != nil {
		panic(err)
	}

	m.subStream = &stream.SubStream{
		Stream:        m.stream,
		UseRTPPackets: req.UseRTPPackets,
	}
	err = m.subStream.Initialize()
	if err != nil {
		panic(err)
	}

	m.setReady <- req
	return defs.PathSourceStaticSetReadyRes{SubStream: m.subStream}
}

func (m *parentMock) SetNotReady(_ defs.PathSourceStaticSetNotReadyReq) {
	m.setNotReady <- struct{}{}
}

type pathManagerMock struct {
	res *defs.PathAddReaderRes
	err error
}

func (pm *pathManagerMock) AddReader(_ defs.PathAddReaderReq) (*defs.PathAddReaderRes, error) {
	return pm.res, pm.err
}

func TestSourceRun(t *testing.T) {
	for _, ca := range []string{"standard", "use absolute timestamp"} {
		t.Run(ca, func(t *testing.T) {
			desc := &description.Session{Medias: []*description.Media{
				{
					Type:    description.MediaTypeVideo,
					Formats: []format.Format{test.FormatH264},
				},
				{
					Type:    description.MediaTypeAudio,
					Formats: []format.Format{test.FormatMPEG4Audio},
				},
			}}

			sourceStream := &stream.Stream{
				OrigDesc:          desc,
				WriteQueueSize:    512,
				RTPMaxPayloadSize: 1450,
				ReplaceNTP:        true,
				Parent:            test.NilLogger,
			}
			err := sourceStream.Initialize()
			require.NoError(t, err)

			sourceSubStream := &stream.SubStream{
				Stream:        sourceStream,
				UseRTPPackets: true,
			}
			err = sourceSubStream.Initialize()
			require.NoError(t, err)

			parent := newParentMock()

			pathManager := &pathManagerMock{
				res: &defs.PathAddReaderRes{
					Path:   &dummyPath{},
					Stream: sourceStream,
				},
			}

			so := &internal.Source{
				PathManager: pathManager,
				Parent:      parent,
			}

			ctx, ctxCancel := context.WithCancel(context.Background())
			defer ctxCancel()

			runDone := make(chan struct{})
			go func() {
				so.Run(defs.StaticSourceRunParams{ //nolint:errcheck
					Context:        ctx,
					ResolvedSource: "internal://source",
					Conf: &conf.Path{
						UseAbsoluteTimestamp: ca == "use absolute timestamp",
					},
					ReloadConf: make(chan *conf.Path),
				})
				close(runDone)
			}()

			<-parent.setReady

			// attach a reader to the target stream and verify units are forwarded
			received := make(chan *unit.Unit, 8)
			targetReader := &stream.Reader{Parent: test.NilLogger}
			targetReader.OnData(desc.Medias[0], desc.Medias[0].Formats[0], func(u *unit.Unit) error {
				received <- u
				return nil
			})
			parent.stream.AddReader(targetReader)
			defer parent.stream.RemoveReader(targetReader)

			videoPkt := &rtp.Packet{
				Version:        2,
				Marker:         true,
				PayloadType:    96,
				SequenceNumber: 54321,
				Timestamp:      12345678,
				SSRC:           424242,
				Payload:        []byte{5},
			}

			sourceSubStream.WriteUnit(desc.Medias[0], desc.Medias[0].Formats[0], &unit.Unit{
				PTS:        90000,
				NTP:        time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
				RTPPackets: []*rtp.Packet{videoPkt},
			})

			got := <-received
			require.Equal(t, int64(90000), got.PTS)
			require.Len(t, got.RTPPackets, 1)
			// RTP packet passes through untouched: no re-encoding, timestamps preserved
			require.Equal(t, videoPkt.SequenceNumber, got.RTPPackets[0].SequenceNumber)
			require.Equal(t, videoPkt.Timestamp, got.RTPPackets[0].Timestamp)
			require.Equal(t, videoPkt.SSRC, got.RTPPackets[0].SSRC)
			require.Equal(t, videoPkt.Payload, got.RTPPackets[0].Payload)

			// stopping the source (context cancellation) triggers SetNotReady
			// and makes Run return, after removing the reader from the source
			ctxCancel()

			select {
			case <-parent.setNotReady:
			case <-time.After(time.Second):
				t.Fatal("SetNotReady not called")
			}

			select {
			case <-runDone:
			case <-time.After(time.Second):
				t.Fatal("Run did not return")
			}

			parent.stream.Close()
			sourceStream.Close()
		})
	}
}

func TestSourceContextCancel(t *testing.T) {
	desc := &description.Session{Medias: []*description.Media{
		{
			Type:    description.MediaTypeVideo,
			Formats: []format.Format{test.FormatH264},
		},
	}}

	sourceStream := &stream.Stream{
		OrigDesc:          desc,
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		ReplaceNTP:        true,
		Parent:            test.NilLogger,
	}
	err := sourceStream.Initialize()
	require.NoError(t, err)

	sourceSubStream := &stream.SubStream{
		Stream:        sourceStream,
		UseRTPPackets: true,
	}
	err = sourceSubStream.Initialize()
	require.NoError(t, err)

	parent := newParentMock()

	pm := &pathManagerMock{
		res: &defs.PathAddReaderRes{
			Path:   &dummyPath{},
			Stream: sourceStream,
		},
	}

	so := &internal.Source{
		PathManager: pm,
		Parent:      parent,
	}

	ctx, ctxCancel := context.WithCancel(context.Background())

	runDone := make(chan struct{})
	go func() {
		so.Run(defs.StaticSourceRunParams{ //nolint:errcheck
			Context:        ctx,
			ResolvedSource: "internal://source",
			Conf:           &conf.Path{},
			ReloadConf:     make(chan *conf.Path),
		})
		close(runDone)
	}()

	<-parent.setReady

	ctxCancel()

	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}

	select {
	case <-parent.setNotReady:
	case <-time.After(time.Second):
		t.Fatal("SetNotReady not called")
	}

	parent.stream.Close()
	sourceStream.Close()
}

func TestSourceMultipleTargets(t *testing.T) {
	desc := &description.Session{Medias: []*description.Media{
		{
			Type:    description.MediaTypeVideo,
			Formats: []format.Format{test.FormatH264},
		},
	}}

	sourceStream := &stream.Stream{
		OrigDesc:          desc,
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		ReplaceNTP:        true,
		Parent:            test.NilLogger,
	}
	err := sourceStream.Initialize()
	require.NoError(t, err)

	sourceSubStream := &stream.SubStream{
		Stream:        sourceStream,
		UseRTPPackets: true,
	}
	err = sourceSubStream.Initialize()
	require.NoError(t, err)

	pm := &pathManagerMock{
		res: &defs.PathAddReaderRes{
			Path:   &dummyPath{},
			Stream: sourceStream,
		},
	}
	parents := []*parentMock{newParentMock(), newParentMock()}
	sources := make([]*internal.Source, 2)
	runDones := make([]chan struct{}, 2)

	for i := range sources {
		sources[i] = &internal.Source{
			PathManager: pm,
			Parent:      parents[i],
		}

		ctx, ctxCancel := context.WithCancel(context.Background())
		defer ctxCancel()

		runDone := make(chan struct{})
		runDones[i] = runDone

		go func(so *internal.Source) {
			so.Run(defs.StaticSourceRunParams{ //nolint:errcheck
				Context:        ctx,
				ResolvedSource: "internal://source",
				Conf:           &conf.Path{},
				ReloadConf:     make(chan *conf.Path),
			})
			close(runDone)
		}(sources[i])
	}

	// both targets attach to the same source stream
	for _, parent := range parents {
		<-parent.setReady
	}

	received := make([]chan *unit.Unit, 2)
	for i, parent := range parents {
		received[i] = make(chan *unit.Unit, 8)
		targetReader := &stream.Reader{Parent: test.NilLogger}
		targetReader.OnData(desc.Medias[0], desc.Medias[0].Formats[0], func(u *unit.Unit) error {
			received[i] <- u
			return nil
		})
		parent.stream.AddReader(targetReader)
		defer parent.stream.RemoveReader(targetReader)
	}

	// emit several units; each target must receive every one of them,
	// independently, proving units are deep-copied and not shared
	for i := range 5 {
		sourceSubStream.WriteUnit(desc.Medias[0], desc.Medias[0].Formats[0], &unit.Unit{
			PTS: int64(90000 * (i + 1)),
			RTPPackets: []*rtp.Packet{{
				Version:        2,
				Marker:         true,
				PayloadType:    96,
				SequenceNumber: uint16(100 + i),
				Timestamp:      uint32(1000 + i),
				SSRC:           424242,
				Payload:        []byte{byte(i)},
			}},
		})
	}

	for i, parent := range parents {
		for j := range 5 {
			select {
			case u := <-received[i]:
				require.Equal(t, int64(90000*(j+1)), u.PTS)
				require.Len(t, u.RTPPackets, 1)
				require.Equal(t, uint16(100+j), u.RTPPackets[0].SequenceNumber)
			case <-time.After(time.Second):
				t.Fatalf("target %d did not receive unit %d", i, j)
			}
		}
		parent.stream.Close()
	}

	sourceStream.Close()
}

func TestSourceUnsupportedCodec(t *testing.T) {
	desc := &description.Session{Medias: []*description.Media{
		{
			Type:    description.MediaTypeVideo,
			Formats: []format.Format{test.FormatH265},
		},
	}}

	sourceStream := &stream.Stream{
		OrigDesc:          desc,
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		ReplaceNTP:        true,
		Parent:            test.NilLogger,
	}
	err := sourceStream.Initialize()
	require.NoError(t, err)
	defer sourceStream.Close()

	pm := &pathManagerMock{
		res: &defs.PathAddReaderRes{
			Path:   &dummyPath{},
			Stream: sourceStream,
		},
	}
	parent := newParentMock()

	so := &internal.Source{
		PathManager: pm,
		Parent:      parent,
	}

	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	runErr := make(chan error, 1)
	go func() {
		runErr <- so.Run(defs.StaticSourceRunParams{
			Context:        ctx,
			ResolvedSource: "internal://source",
			Conf:           &conf.Path{},
			ReloadConf:     make(chan *conf.Path),
		})
	}()

	select {
	case err := <-runErr:
		require.Error(t, err)
		require.Contains(t, err.Error(), "only supports H264 and MPEG-4 Audio (AAC)")
	case <-time.After(time.Second):
		t.Fatal("Run did not return on unsupported codec")
	}
}
