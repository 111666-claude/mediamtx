package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtpav1"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/formatlabel"
	"github.com/bluenviron/mediamtx/internal/test"
)

// Sequence headers extracted from fixed AV1 samples generated with ffmpeg
// (libaom-av1, yuv420p) and cross-checked with ffprobe.
// See artifacts/av1scripts and artifacts/av1samples.
var (
	// 1920x1080, profile 0 (Main), level 8, tier 0
	coreAV1SequenceHeader1080p = []byte{0x0a, 0x0b, 0x00, 0x00, 0x00, 0x42, 0xab, 0xbf, 0xc3, 0x73, 0x2b, 0xe4, 0x01}
	// 1280x720, profile 0 (Main), level 5, tier 0
	coreAV1SequenceHeader720p = []byte{0x0a, 0x0b, 0x00, 0x00, 0x00, 0x2d, 0x4c, 0xff, 0xb3, 0xcc, 0xaf, 0x90, 0x04}
)

var coreAV1FrameOBU = []byte{0x32, 0x01, 0x00}

// custom ports are used to avoid clashing with unrelated local instances.
const (
	av1TestRTSPPort = "18554"
	av1TestAPIPort  = "19997"
)

type apiCodecPropsAV1 struct {
	Width   int `json:"width"`
	Height  int `json:"height"`
	Profile int `json:"profile"`
	Level   int `json:"level"`
	Tier    int `json:"tier"`
}

type apiCodecPropsH264 struct {
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Profile string `json:"profile"`
	Level   string `json:"level"`
}

type apiPathTrack struct {
	Codec      string          `json:"codec"`
	CodecProps json.RawMessage `json:"codecProps"`
}

type apiPathsGetResponse struct {
	Name    string         `json:"name"`
	Ready   bool           `json:"ready"`
	Tracks2 []apiPathTrack `json:"tracks2"`
}

func uint32Ptr(v uint32) *uint32 {
	return &v
}

// av1APITracks fetches the tracks of a path. It returns false when the path
// is not available yet, so it can be used inside require.Eventually.
func av1APITracks(t *testing.T, hc *http.Client, pathName string) ([]apiPathTrack, bool) {
	t.Helper()

	var out apiPathsGetResponse

	req, err := http.NewRequest(http.MethodGet,
		"http://localhost:"+av1TestAPIPort+"/v3/paths/get/"+pathName, nil)
	require.NoError(t, err)

	res, err := hc.Do(req)
	if err != nil {
		return nil, false
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, false
	}

	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, false
	}

	return out.Tracks2, out.Ready
}

func waitAV1Props(t *testing.T, hc *http.Client, pathName string, want apiCodecPropsAV1) {
	t.Helper()

	require.Eventually(t, func() bool {
		tracks, ready := av1APITracks(t, hc, pathName)
		if !ready || len(tracks) != 1 || tracks[0].Codec != string(formatlabel.AV1) {
			return false
		}

		var props apiCodecPropsAV1
		if err := json.Unmarshal(tracks[0].CodecProps, &props); err != nil {
			return false
		}

		return props == want
	}, 5*time.Second, 50*time.Millisecond)
}

// av1Publisher is an RTSP publisher with a persistent RTP encoder, so sequence
// numbers keep increasing across writes and the server never drops units as
// duplicated or out of order.
type av1Publisher struct {
	t       *testing.T
	client  *gortsplib.Client
	media   *description.Media
	encoder *rtpav1.Encoder
}

func newAV1Publisher(t *testing.T, pathName string) *av1Publisher {
	t.Helper()

	forma := &format.AV1{PayloadTyp: 96}
	media := &description.Media{
		Type:    description.MediaTypeVideo,
		Formats: []format.Format{forma},
	}

	client := &gortsplib.Client{}
	err := client.StartRecording(
		fmt.Sprintf("rtsp://localhost:%s/%s", av1TestRTSPPort, pathName),
		&description.Session{Medias: []*description.Media{media}})
	require.NoError(t, err)

	encoder := &rtpav1.Encoder{
		PayloadType: 96,
		SSRC:        uint32Ptr(0x12345678),
	}
	require.NoError(t, encoder.Init())

	return &av1Publisher{
		t:       t,
		client:  client,
		media:   media,
		encoder: encoder,
	}
}

func (pub *av1Publisher) close() {
	pub.client.Close()
}

// write sends a temporal unit made of a sequence header OBU and a frame OBU.
func (pub *av1Publisher) write(sequenceHeader []byte, ts uint32) {
	pub.t.Helper()

	pkts, err := pub.encoder.Encode([][]byte{sequenceHeader, coreAV1FrameOBU})
	require.NoError(pub.t, err)

	for _, pkt := range pkts {
		pkt.Timestamp = ts
		require.NoError(pub.t, pub.client.WritePacketRTP(pub.media, pkt))
	}
}

// writeFrame sends a temporal unit without any sequence header.
func (pub *av1Publisher) writeFrame(ts uint32) {
	pub.t.Helper()

	pkts, err := pub.encoder.Encode([][]byte{coreAV1FrameOBU})
	require.NoError(pub.t, err)

	for _, pkt := range pkts {
		pkt.Timestamp = ts
		require.NoError(pub.t, pub.client.WritePacketRTP(pub.media, pkt))
	}
}

func TestAPIPathsGetAV1CodecProps(t *testing.T) {
	p, ok := newInstance(t,
		"api: yes\n"+
			"apiAddress: :"+av1TestAPIPort+"\n"+
			"rtspAddress: :"+av1TestRTSPPort+"\n"+
			"rtmp: no\n"+
			"hls: no\n"+
			"webrtc: no\n"+
			"srt: no\n"+
			"paths:\n"+
			"  all_others:\n")
	require.Equal(t, true, ok)
	defer p.Close()

	hc := &http.Client{}

	t.Run("dimensions follow the real stream", func(t *testing.T) {
		pub := newAV1Publisher(t, "av1props")
		defer pub.close()

		// wait for the path to become available, then check the explicit
		// unknown state before any valid sequence header is received
		require.Eventually(t, func() bool {
			_, ready := av1APITracks(t, hc, "av1props")
			return ready
		}, 5*time.Second, 50*time.Millisecond)

		waitAV1Props(t, hc, "av1props", apiCodecPropsAV1{})

		pub.write(coreAV1SequenceHeader1080p, 0)

		waitAV1Props(t, hc, "av1props", apiCodecPropsAV1{
			Width:   1920,
			Height:  1080,
			Profile: 0,
			Level:   8,
			Tier:    0,
		})

		// resolution switch
		pub.write(coreAV1SequenceHeader720p, 90000)

		waitAV1Props(t, hc, "av1props", apiCodecPropsAV1{
			Width:   1280,
			Height:  720,
			Profile: 0,
			Level:   5,
			Tier:    0,
		})
	})

	t.Run("publisher reconnect does not keep the previous stream size", func(t *testing.T) {
		pub1 := newAV1Publisher(t, "av1reconnect")
		pub1.write(coreAV1SequenceHeader1080p, 0)

		waitAV1Props(t, hc, "av1reconnect", apiCodecPropsAV1{
			Width:   1920,
			Height:  1080,
			Profile: 0,
			Level:   8,
			Tier:    0,
		})

		pub1.close()

		// the path must disappear while no publisher is connected
		require.Eventually(t, func() bool {
			res, err := hc.Get("http://localhost:" + av1TestAPIPort + "/v3/paths/get/av1reconnect")
			if err != nil {
				return false
			}
			res.Body.Close()
			return res.StatusCode == http.StatusNotFound
		}, 5*time.Second, 50*time.Millisecond)

		// a new publisher, whose first units contain no sequence header yet
		pub2 := newAV1Publisher(t, "av1reconnect")
		defer pub2.close()

		pub2.writeFrame(0)

		// the 1920x1080 dimensions of the previous stream must not survive
		waitAV1Props(t, hc, "av1reconnect", apiCodecPropsAV1{})

		pub2.write(coreAV1SequenceHeader720p, 90000)

		waitAV1Props(t, hc, "av1reconnect", apiCodecPropsAV1{
			Width:   1280,
			Height:  720,
			Profile: 0,
			Level:   5,
			Tier:    0,
		})
	})

	t.Run("h264 properties are not regressed", func(t *testing.T) {
		mediaH264 := test.UniqueMediaH264()
		source := gortsplib.Client{}
		err := source.StartRecording(
			fmt.Sprintf("rtsp://localhost:%s/av1h264", av1TestRTSPPort),
			&description.Session{Medias: []*description.Media{mediaH264}})
		require.NoError(t, err)
		defer source.Close()

		require.Eventually(t, func() bool {
			tracks, ready := av1APITracks(t, hc, "av1h264")
			if !ready || len(tracks) != 1 || tracks[0].Codec != string(formatlabel.H264) {
				return false
			}

			var props apiCodecPropsH264
			if err := json.Unmarshal(tracks[0].CodecProps, &props); err != nil {
				return false
			}

			return props.Width != 0 && props.Height != 0
		}, 5*time.Second, 50*time.Millisecond)
	})
}
