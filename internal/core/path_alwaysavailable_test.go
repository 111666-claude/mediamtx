package core

import (
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"
)

func TestPathAlwaysAvailableIgnoreGenericTracks(t *testing.T) {
	newH265Media := func() *description.Media {
		return &description.Media{
			Type:    description.MediaTypeVideo,
			Formats: []format.Format{&format.H265{PayloadTyp: 96}},
		}
	}

	newG711Media := func() *description.Media {
		return &description.Media{
			Type: description.MediaTypeAudio,
			Formats: []format.Format{&format.G711{
				PayloadTyp:   0,
				MULaw:        true,
				SampleRate:   8000,
				ChannelCount: 1,
			}},
		}
	}

	newGenericMedia := func() *description.Media {
		return &description.Media{
			Type:    description.MediaTypeApplication,
			Formats: []format.Format{&format.Generic{PayloadTyp: 96, RTPMa: "private/90000"}},
		}
	}

	// use custom ports and disable unneeded protocols,
	// since default ports may be occupied by other processes.
	confPrefix := "rtspAddress: 127.0.0.1:18554\n" +
		"rtpAddress: 127.0.0.1:18000\n" +
		"rtcpAddress: 127.0.0.1:18001\n" +
		"rtmp: no\n" +
		"hls: no\n" +
		"webrtc: no\n" +
		"srt: no\n" +
		"moq: no\n"

	t.Run("source with extra generic track", func(t *testing.T) {
		p, ok := newInstance(t,
			confPrefix+
				"paths:\n"+
				"  mystream:\n"+
				"    alwaysAvailable: yes\n"+
				"    alwaysAvailableIgnoreGenericTracks: yes\n"+
				"    alwaysAvailableTracks:\n"+
				"      - codec: H265\n"+
				"      - codec: G711\n"+
				"        sampleRate: 8000\n"+
				"        channelCount: 1\n"+
				"        muLaw: true\n")
		require.Equal(t, true, ok)
		defer p.Close()

		mediH265 := newH265Media()
		mediGeneric := newGenericMedia()
		mediG711 := newG711Media()

		// connect the reader while the source is still offline:
		// it must stay connected through the offline->online switch
		u, err := base.ParseURL("rtsp://localhost:18554/mystream")
		require.NoError(t, err)

		c := gortsplib.Client{
			Scheme: u.Scheme,
			Host:   u.Host,
		}

		err = c.Start()
		require.NoError(t, err)
		defer c.Close()

		desc, _, err := c.Describe(u)
		require.NoError(t, err)

		// the generic track must not be exposed to readers
		require.Len(t, desc.Medias, 2)

		err = c.SetupAll(desc.BaseURL, desc.Medias)
		require.NoError(t, err)

		recvH265 := make(chan struct{})
		recvG711 := make(chan struct{})

		c.OnPacketRTP(desc.Medias[0], desc.Medias[0].Formats[0], func(pkt *rtp.Packet) {
			if string(pkt.Payload) == string([]byte{0x26, 0x01, 0xaf}) {
				select {
				case recvH265 <- struct{}{}:
				default:
				}
			}
		})

		c.OnPacketRTP(desc.Medias[1], desc.Medias[1].Formats[0], func(pkt *rtp.Packet) {
			if string(pkt.Payload) == string([]byte{0x11, 0x22, 0x33}) {
				select {
				case recvG711 <- struct{}{}:
				default:
				}
			}
		})

		_, err = c.Play(nil)
		require.NoError(t, err)

		// bring the source online: the declared tracks must match,
		// the additional generic track must be ignored
		pub := &gortsplib.Client{}
		err = pub.StartRecording(
			"rtsp://localhost:18554/mystream",
			&description.Session{Medias: []*description.Media{mediH265, mediGeneric, mediG711}})
		require.NoError(t, err)
		defer pub.Close()

		stop := make(chan struct{})
		defer close(stop)

		writeErr := make(chan error, 16)

		go func() {
			seq := uint16(1123)
			ts := uint32(45343)

			for {
				select {
				case <-stop:
					return
				case <-time.After(100 * time.Millisecond):
				}

				// packets of the generic track must be silently discarded
				err2 := pub.WritePacketRTP(mediGeneric, &rtp.Packet{
					Version:        2,
					PayloadType:    96,
					SequenceNumber: seq,
					Timestamp:      ts,
					SSRC:           563423,
					Payload:        []byte{1, 2, 3, 4},
				})
				if err2 != nil {
					select {
					case writeErr <- err2:
					default:
					}
				}

				err2 = pub.WritePacketRTP(mediH265, &rtp.Packet{
					Version:        2,
					Marker:         true,
					PayloadType:    96,
					SequenceNumber: seq,
					Timestamp:      ts,
					SSRC:           563423,
					Payload:        []byte{0x26, 0x01, 0xaf},
				})
				if err2 != nil {
					select {
					case writeErr <- err2:
					default:
					}
				}

				err2 = pub.WritePacketRTP(mediG711, &rtp.Packet{
					Version:        2,
					Marker:         true,
					PayloadType:    0,
					SequenceNumber: seq,
					Timestamp:      ts,
					SSRC:           563423,
					Payload:        []byte{0x11, 0x22, 0x33},
				})
				if err2 != nil {
					select {
					case writeErr <- err2:
					default:
					}
				}

				seq++
				ts += 90000
			}
		}()

		for range 2 {
			select {
			case <-recvH265:
			case <-recvG711:
			case werr := <-writeErr:
				t.Fatalf("publisher write error: %v", werr)
			case <-time.After(10 * time.Second):
				t.Fatal("timed out waiting for audio and video packets")
			}
		}
	})

	t.Run("missing required track", func(t *testing.T) {
		p, ok := newInstance(t,
			confPrefix+
				"paths:\n"+
				"  mystream:\n"+
				"    alwaysAvailable: yes\n"+
				"    alwaysAvailableIgnoreGenericTracks: yes\n"+
				"    alwaysAvailableTracks:\n"+
				"      - codec: H265\n"+
				"      - codec: G711\n"+
				"        sampleRate: 8000\n"+
				"        channelCount: 1\n"+
				"        muLaw: true\n")
		require.Equal(t, true, ok)
		defer p.Close()

		pub := &gortsplib.Client{}
		err := pub.StartRecording(
			"rtsp://localhost:18554/mystream",
			&description.Session{Medias: []*description.Media{newH265Media(), newGenericMedia()}})
		require.Error(t, err)
	})

	t.Run("generic track rejected without option", func(t *testing.T) {
		p, ok := newInstance(t,
			confPrefix+
				"paths:\n"+
				"  mystream:\n"+
				"    alwaysAvailable: yes\n"+
				"    alwaysAvailableTracks:\n"+
				"      - codec: H265\n"+
				"      - codec: G711\n"+
				"        sampleRate: 8000\n"+
				"        channelCount: 1\n"+
				"        muLaw: true\n")
		require.Equal(t, true, ok)
		defer p.Close()

		pub := &gortsplib.Client{}
		err := pub.StartRecording(
			"rtsp://localhost:18554/mystream",
			&description.Session{Medias: []*description.Media{newH265Media(), newGenericMedia(), newG711Media()}})
		require.Error(t, err)
	})
}
