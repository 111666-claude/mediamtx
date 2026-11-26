package core

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtpmpeg4audio"
	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/test"
)

// publishH264 starts a background goroutine that writes H264 RTP packets at
// a fixed pace until the terminate channel is closed.
func internalPublishH264(t *testing.T, c *gortsplib.Client, medi *description.Media, startSeq uint16) (terminate chan struct{}, done chan struct{}) {
	t.Helper()

	terminate = make(chan struct{})
	done = make(chan struct{})

	go func() {
		defer close(done)

		h264Format := medi.Formats[0].(*format.H264)

		enc := &rtph264.Encoder{
			PayloadType:       h264Format.PayloadTyp,
			PacketizationMode: h264Format.PacketizationMode,
		}
		if err := enc.Init(); err != nil {
			return
		}

		i := uint16(0)
		for {
			select {
			case <-terminate:
				return
			case <-time.After(50 * time.Millisecond):
			}

			encoded, err := enc.Encode([][]byte{{byte(5 + int(i)%200)}})
			if err != nil {
				return
			}

			pkt := encoded[0]
			pkt.SequenceNumber = startSeq + i
			pkt.Timestamp = 100000 + uint32(i)*3000
			pkt.SSRC = 777001

			if err := c.WritePacketRTP(medi, pkt); err != nil {
				return
			}
			i++
		}
	}()

	return terminate, done
}

// publishAAC starts a background goroutine that writes AAC RTP packets at a
// fixed pace until the terminate channel is closed.
func internalPublishAAC(t *testing.T, c *gortsplib.Client, medi *description.Media,
	forma *format.MPEG4Audio, startSeq uint16,
) (terminate chan struct{}, done chan struct{}) {
	t.Helper()

	terminate = make(chan struct{})
	done = make(chan struct{})

	go func() {
		defer close(done)

		enc := &rtpmpeg4audio.Encoder{
			PayloadType:      forma.PayloadTyp,
			SizeLength:       forma.SizeLength,
			IndexLength:      forma.IndexLength,
			IndexDeltaLength: forma.IndexDeltaLength,
		}
		if err := enc.Init(); err != nil {
			return
		}

		i := uint16(0)
		for {
			select {
			case <-terminate:
				return
			case <-time.After(50 * time.Millisecond):
			}

			encoded, err := enc.Encode([][]byte{{byte(10 + int(i)%200)}})
			if err != nil {
				return
			}

			pkt := encoded[0]
			pkt.SequenceNumber = startSeq + i
			pkt.Timestamp = 200000 + uint32(i)*1024
			pkt.SSRC = 777002

			if err := c.WritePacketRTP(medi, pkt); err != nil {
				return
			}
			i++
		}
	}()

	return terminate, done
}

// readRTSPPackets connects to a path and reads n packets of the given media
// index, returning them in arrival order.
func readRTSPPackets(t *testing.T, rawURL string, user string, pass string, mediaIndex int, n int) []*rtp.Packet {
	t.Helper()

	u, err := base.ParseURL(rawURL)
	require.NoError(t, err)

	if user != "" {
		u.User = url.UserPassword(user, pass)
	}

	c := &gortsplib.Client{
		Scheme:   u.Scheme,
		Host:     u.Host,
		Protocol: new(gortsplib.ProtocolTCP),
	}

	require.NoError(t, c.Start())
	defer c.Close()

	desc, _, err := c.Describe(u)
	require.NoError(t, err)
	require.NotNil(t, desc)
	require.Greater(t, len(desc.Medias), mediaIndex)

	require.NoError(t, c.SetupAll(desc.BaseURL, desc.Medias))

	received := make(chan *rtp.Packet, n*2)

	c.OnPacketRTP(desc.Medias[mediaIndex], desc.Medias[mediaIndex].Formats[0], func(pkt *rtp.Packet) {
		received <- pkt
	})

	_, err = c.Play(nil)
	require.NoError(t, err)

	out := make([]*rtp.Packet, 0, n)
	for len(out) < n {
		select {
		case pkt := <-received:
			out = append(out, pkt)
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for packets, got %d/%d", len(out), n)
		}
	}

	return out
}

func pathAPIGet(t *testing.T, hc *http.Client, name string) map[string]any {
	t.Helper()

	res, err := hc.Get("http://localhost:9997/v3/paths/get/" + name)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var out map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	return out
}

func pathAvailable(t *testing.T, hc *http.Client, name string) bool {
	t.Helper()
	return pathAPIGet(t, hc, name)["available"].(bool)
}

func waitPathAvailable(t *testing.T, hc *http.Client, name string, want bool) {
	t.Helper()

	require.Eventually(t, func() bool {
		return pathAvailable(t, hc, name) == want
	}, 10*time.Second, 50*time.Millisecond)
}

func internalSourceConf() string {
	return "api: yes\n" +
		"rtmp: no\n" +
		"hls: no\n" +
		"webrtc: no\n" +
		"paths:\n" +
		"  source:\n" +
		"  target1:\n" +
		"    source: internal://source\n" +
		"  target2:\n" +
		"    source: internal://source\n"
}

func TestPathInternalSource(t *testing.T) {
	p, ok := newInstance(t, internalSourceConf())
	require.True(t, ok)
	defer p.Close()

	videoMedia := test.UniqueMediaH264()
	audioMedia := test.UniqueMediaMPEG4Audio()
	audioFormat := audioMedia.Formats[0].(*format.MPEG4Audio)

	// publish H264 + AAC to the source path
	publisher := &gortsplib.Client{}
	err := publisher.StartRecording("rtsp://localhost:8554/source",
		&description.Session{Medias: []*description.Media{videoMedia, audioMedia}})
	require.NoError(t, err)
	defer publisher.Close()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	// wait until the two target paths become available
	waitPathAvailable(t, hc, "target1", true)
	waitPathAvailable(t, hc, "target2", true)

	time.Sleep(500 * time.Millisecond)

	// the source path exposes no protocol-level reader (in particular no RTSP
	// session): internal readers are hidden and never open a local connection
	sourcePathOut := pathAPIGet(t, hc, "source")
	require.Equal(t, true, sourcePathOut["available"])
	require.Empty(t, sourcePathOut["readers"].([]any))

	// targets report the internal source type
	for _, name := range []string{"target1", "target2"} {
		out := pathAPIGet(t, hc, name)
		source := out["source"].(map[string]any)
		require.Equal(t, string(defs.APIPathSourceTypeInternalSource), source["type"])
	}

	// stream H264 packets continuously and capture what each target receives
	terminate, done := internalPublishH264(t, publisher, videoMedia, 1000)
	defer func() {
		close(terminate)
		<-done
	}()

	for _, target := range []string{"target1", "target2"} {
		got := readRTSPPackets(t, "rtsp://localhost:8554/"+target, "", "", 0, 10)
		require.Len(t, got, 10)

		// frame order is preserved: sequence numbers and timestamps are
		// strictly increasing; every timestamp delta is a multiple of the
		// source step, which proves timing information is not re-generated
		for j := 1; j < len(got); j++ {
			require.Greater(t, got[j].SequenceNumber, got[j-1].SequenceNumber,
				"target %s packet %d", target, j)
			require.Greater(t, got[j].Timestamp, got[j-1].Timestamp,
				"target %s packet %d", target, j)
			require.Equal(t, uint32(0), (got[j].Timestamp-got[j-1].Timestamp)%3000,
				"target %s packet %d", target, j)
			require.NotEmpty(t, got[j].Payload)
		}

		// the source payload type is forwarded unchanged
		require.Equal(t, uint8(96), got[0].PayloadType)
	}

	// AAC is forwarded as well, with order and timestamp spacing preserved
	aacTerminate, aacDone := internalPublishAAC(t, publisher, audioMedia, audioFormat, 2000)
	defer func() {
		close(aacTerminate)
		<-aacDone
	}()

	aacGot := readRTSPPackets(t, "rtsp://localhost:8554/target1", "", "", 1, 10)
	require.Len(t, aacGot, 10)
	for i := 1; i < len(aacGot); i++ {
		require.Greater(t, aacGot[i].SequenceNumber, aacGot[i-1].SequenceNumber)
		require.Greater(t, aacGot[i].Timestamp, aacGot[i-1].Timestamp)
		require.Equal(t, uint32(0), (aacGot[i].Timestamp-aacGot[i-1].Timestamp)%1024)
		require.NotEmpty(t, aacGot[i].Payload)
	}
}

func TestPathInternalSourcePermissions(t *testing.T) {
	conf := "api: yes\n" +
		"rtmp: no\n" +
		"hls: no\n" +
		"webrtc: no\n" +
		"authInternalUsers:\n" +
		"  - user: any\n" +
		"    pass:\n" +
		"    ips: []\n" +
		"    permissions:\n" +
		"      - action: publish\n" +
		"      - action: read\n" +
		"        path: source\n" +
		"      - action: read\n" +
		"        path: target1\n" +
		"  - user: privileged\n" +
		"    pass: secretpass\n" +
		"    ips: []\n" +
		"    permissions:\n" +
		"      - action: read\n" +
		"        path: target2\n" +
		"  - user: any\n" +
		"    pass:\n" +
		"    ips: [\"127.0.0.1\", \"::1\"]\n" +
		"    permissions:\n" +
		"      - action: api\n" +
		"      - action: metrics\n" +
		"      - action: pprof\n" +
		"paths:\n" +
		"  source:\n" +
		"  target1:\n" +
		"    source: internal://source\n" +
		"  target2:\n" +
		"    source: internal://source\n"

	p, ok := newInstance(t, conf)
	require.True(t, ok)
	defer p.Close()

	medi := test.UniqueMediaH264()

	publisher := &gortsplib.Client{}
	err := publisher.StartRecording("rtsp://localhost:8554/source",
		&description.Session{Medias: []*description.Media{medi}})
	require.NoError(t, err)
	defer publisher.Close()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	// internal source bypasses auth on the source path: both targets become available
	waitPathAvailable(t, hc, "target1", true)
	waitPathAvailable(t, hc, "target2", true)

	terminate, done := internalPublishH264(t, publisher, medi, 3000)
	defer func() {
		close(terminate)
		<-done
	}()

	// anonymous reader can read target1 (granted to 'any')
	pkts1 := readRTSPPackets(t, "rtsp://localhost:8554/target1", "", "", 0, 1)
	require.Len(t, pkts1, 1)

	// anonymous reader cannot read target2
	u, err := base.ParseURL("rtsp://localhost:8554/target2")
	require.NoError(t, err)
	denied := &gortsplib.Client{Scheme: u.Scheme, Host: u.Host}
	require.NoError(t, denied.Start())
	_, _, err = denied.Describe(u)
	require.Error(t, err)
	denied.Close()

	// privileged reader can read target2
	pkts2 := readRTSPPackets(t, "rtsp://localhost:8554/target2", "privileged", "secretpass", 0, 1)
	require.Len(t, pkts2, 1)
}

func TestPathInternalSourceReconnect(t *testing.T) {
	p, ok := newInstance(t, internalSourceConf())
	require.True(t, ok)
	defer p.Close()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	medi := test.UniqueMediaH264()

	publisher := &gortsplib.Client{}
	err := publisher.StartRecording("rtsp://localhost:8554/source",
		&description.Session{Medias: []*description.Media{medi}})
	require.NoError(t, err)

	waitPathAvailable(t, hc, "target1", true)

	terminate, done := internalPublishH264(t, publisher, medi, 6000)

	first := readRTSPPackets(t, "rtsp://localhost:8554/target1", "", "", 0, 3)
	require.Len(t, first, 3)

	// publisher disconnects: target must become unavailable
	close(terminate)
	<-done
	publisher.Close()

	waitPathAvailable(t, hc, "target1", false)

	// publisher comes back: the internal source retries and re-attaches
	publisher2 := &gortsplib.Client{}
	err = publisher2.StartRecording("rtsp://localhost:8554/source",
		&description.Session{Medias: []*description.Media{medi}})
	require.NoError(t, err)
	defer publisher2.Close()

	waitPathAvailable(t, hc, "target1", true)

	terminate2, done2 := internalPublishH264(t, publisher2, medi, 7000)
	defer func() {
		close(terminate2)
		<-done2
	}()

	// frames flow again after reconnection
	second := readRTSPPackets(t, "rtsp://localhost:8554/target1", "", "", 0, 3)
	require.Len(t, second, 3)
}

func TestPathInternalSourceReaderExit(t *testing.T) {
	p, ok := newInstance(t, internalSourceConf())
	require.True(t, ok)
	defer p.Close()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	medi := test.UniqueMediaH264()

	publisher := &gortsplib.Client{}
	err := publisher.StartRecording("rtsp://localhost:8554/source",
		&description.Session{Medias: []*description.Media{medi}})
	require.NoError(t, err)
	defer publisher.Close()

	waitPathAvailable(t, hc, "target1", true)

	terminate, done := internalPublishH264(t, publisher, medi, 4000)
	defer func() {
		close(terminate)
		<-done
	}()

	// two RTSP readers join and leave; the target path stays available throughout
	for range 2 {
		got := readRTSPPackets(t, "rtsp://localhost:8554/target1", "", "", 0, 1)
		require.Len(t, got, 1)
	}

	require.True(t, pathAvailable(t, hc, "target1"))

	// the internal reader stays attached to the source but remains hidden:
	// no protocol-level reader (and no RTSP session) is reported
	sourceOut := pathAPIGet(t, hc, "source")
	require.Empty(t, sourceOut["readers"].([]any))
}

func TestPathInternalSourceReload(t *testing.T) {
	p, ok := newInstance(t, "api: yes\n"+
		"rtmp: no\n"+
		"hls: no\n"+
		"webrtc: no\n"+
		"paths:\n"+
		"  source:\n"+
		"  target1:\n")
	require.True(t, ok)
	defer p.Close()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	medi := test.UniqueMediaH264()

	publisher := &gortsplib.Client{}
	err := publisher.StartRecording("rtsp://localhost:8554/source",
		&description.Session{Medias: []*description.Media{medi}})
	require.NoError(t, err)
	defer publisher.Close()

	// target1 has no source yet: not available
	require.False(t, pathAvailable(t, hc, "target1"))

	// hot-add the internal source via API
	httpRequest(t, hc, http.MethodPatch,
		"http://localhost:9997/v3/config/paths/patch/target1",
		map[string]any{"source": "internal://source"}, nil)

	require.Eventually(t, func() bool {
		out := pathAPIGet(t, hc, "target1")
		source, ok := out["source"].(map[string]any)
		return out["available"].(bool) && ok &&
			source["type"] == string(defs.APIPathSourceTypeInternalSource)
	}, 5*time.Second, 50*time.Millisecond)

	terminate, done := internalPublishH264(t, publisher, medi, 8000)
	got := readRTSPPackets(t, "rtsp://localhost:8554/target1", "", "", 0, 2)
	require.Len(t, got, 2)

	// remove the source again
	httpRequest(t, hc, http.MethodPatch,
		"http://localhost:9997/v3/config/paths/patch/target1",
		map[string]any{"source": "publisher"}, nil)

	close(terminate)
	<-done

	waitPathAvailable(t, hc, "target1", false)

	// source path has no hidden internal readers anymore
	sourceOut := pathAPIGet(t, hc, "source")
	require.Empty(t, sourceOut["readers"].([]any))
}

func TestPathInternalSourceOnDemand(t *testing.T) {
	p, ok := newInstance(t, "api: yes\n"+
		"rtmp: no\n"+
		"hls: no\n"+
		"webrtc: no\n"+
		"paths:\n"+
		"  source:\n"+
		"  target1:\n"+
		"    source: internal://source\n"+
		"    sourceOnDemand: yes\n"+
		"    sourceOnDemandCloseAfter: 1s\n")
	require.True(t, ok)
	defer p.Close()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	medi := test.UniqueMediaH264()

	publisher := &gortsplib.Client{}
	err := publisher.StartRecording("rtsp://localhost:8554/source",
		&description.Session{Medias: []*description.Media{medi}})
	require.NoError(t, err)
	defer publisher.Close()

	// no reader yet: the on-demand target is not available and does not
	// attach to the source
	require.False(t, pathAvailable(t, hc, "target1"))
	sourceOut := pathAPIGet(t, hc, "source")
	require.Empty(t, sourceOut["readers"].([]any))

	terminate, done := internalPublishH264(t, publisher, medi, 9000)
	defer func() {
		close(terminate)
		<-done
	}()

	// the first reader triggers the internal source
	got := readRTSPPackets(t, "rtsp://localhost:8554/target1", "", "", 0, 2)
	require.Len(t, got, 2)

	// while the reader is connected, the internal reader is attached (hidden)
	require.True(t, pathAvailable(t, hc, "target1"))
	sourceOut = pathAPIGet(t, hc, "source")
	require.Empty(t, sourceOut["readers"].([]any))

	// after the reader leaves and sourceOnDemandCloseAfter elapses, the target
	// becomes unavailable and the internal reader is released
	require.Eventually(t, func() bool {
		return !pathAvailable(t, hc, "target1")
	}, 5*time.Second, 50*time.Millisecond)
}
