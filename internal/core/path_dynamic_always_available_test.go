package core

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"
)

// startRTSPReader connects an RTSP reader to the given path, starts playback and
// returns the client together with an atomic counter of received packets.
func startRTSPReader(t *testing.T, pathName string) (*gortsplib.Client, *atomic.Int64) {
	t.Helper()

	u, err := base.ParseURL("rtsp://127.0.0.1:8554/" + pathName)
	require.NoError(t, err)

	reader := &gortsplib.Client{
		Scheme: u.Scheme,
		Host:   u.Host,
	}

	require.NoError(t, reader.Start())
	t.Cleanup(reader.Close)

	desc, _, err := reader.Describe(u)
	require.NoError(t, err)

	require.NoError(t, reader.SetupAll(desc.BaseURL, desc.Medias))

	var received atomic.Int64
	reader.OnPacketRTP(desc.Medias[0], desc.Medias[0].Formats[0], func(_ *rtp.Packet) {
		received.Add(1)
	})

	_, err = reader.Play(nil)
	require.NoError(t, err)

	return reader, &received
}

func TestPathDynamicAlwaysAvailable(t *testing.T) {
	// shorten the idle grace period so the test does not wait for the default value
	prevIdleCloseAfter := idleCloseAfter
	idleCloseAfter = 300 * time.Millisecond
	t.Cleanup(func() { idleCloseAfter = prevIdleCloseAfter })

	p, ok := newInstance(t, "api: yes\n"+
		"rtmp: no\n"+
		"hls: no\n"+
		"webrtc: no\n"+
		"srt: no\n"+
		"moq: no\n"+
		"paths:\n"+
		"  ~^cam_(.+)$:\n"+
		"    alwaysAvailable: yes\n"+
		"    alwaysAvailableTracks:\n"+
		"      - codec: H264\n"+
		"  fixed:\n"+
		"    alwaysAvailable: yes\n"+
		"    alwaysAvailableTracks:\n"+
		"      - codec: H264\n")
	require.True(t, ok)
	defer p.Close()

	// a path that does not match the regular expression must not create an instance
	_, err := p.pathManager.APIPathsGet("other_thing")
	require.Error(t, err)

	// a regular fixed path is created at startup and is never removed
	_, err = p.pathManager.APIPathsGet("fixed")
	require.NoError(t, err)

	// two different names matching the regular expression create two isolated
	// instances, each one immediately serving its offline stream
	reader1, received1 := startRTSPReader(t, "cam_one")
	reader2, received2 := startRTSPReader(t, "cam_two")

	require.Eventually(t, func() bool { return received1.Load() > 0 },
		5*time.Second, 50*time.Millisecond, "cam_one did not receive the offline stream")
	require.Eventually(t, func() bool { return received2.Load() > 0 },
		5*time.Second, 50*time.Millisecond, "cam_two did not receive the offline stream")

	data1, err := p.pathManager.APIPathsGet("cam_one")
	require.NoError(t, err)
	require.True(t, data1.Ready, "offline stream must make the path ready")
	require.False(t, data1.Online, "path must be offline without a publisher")

	data2, err := p.pathManager.APIPathsGet("cam_two")
	require.NoError(t, err)
	require.True(t, data2.Ready)
	require.False(t, data2.Online)

	// the two instances are independent: only cam_one gets a publisher
	source1, write1 := publishH264(t, "cam_one", 90000)
	require.NoError(t, write1())

	require.Eventually(t, func() bool {
		data, err2 := p.pathManager.APIPathsGet("cam_one")
		return err2 == nil && data.Online
	}, 5*time.Second, 50*time.Millisecond, "cam_one did not become online")

	// cam_two keeps serving its offline stream and stays offline
	data2, err = p.pathManager.APIPathsGet("cam_two")
	require.NoError(t, err)
	require.False(t, data2.Online)
	require.True(t, data2.Ready)

	// readers were never torn down by the online/offline switches: they keep
	// receiving packets from the same stream on both instances
	prev1 := received1.Load()
	prev2 := received2.Load()
	require.Eventually(t, func() bool { return received1.Load() > prev1 },
		5*time.Second, 50*time.Millisecond, "cam_one reader stopped receiving while online")
	require.Eventually(t, func() bool { return received2.Load() > prev2 },
		5*time.Second, 50*time.Millisecond, "cam_two reader stopped receiving offline")

	// when the publisher disconnects, cam_one goes back to its offline stream,
	// while its reader stays connected
	source1.Close()

	require.Eventually(t, func() bool {
		data, err2 := p.pathManager.APIPathsGet("cam_one")
		return err2 == nil && !data.Online && data.Ready
	}, 5*time.Second, 50*time.Millisecond, "cam_one did not switch back to the offline stream")

	prev1 = received1.Load()
	require.Eventually(t, func() bool { return received1.Load() > prev1 },
		5*time.Second, 50*time.Millisecond, "cam_one reader stopped receiving after going offline")

	// a new publisher can come online again on the same surviving instance
	source1b, write1b := publishH264(t, "cam_one", 90000)
	require.NoError(t, write1b())
	require.Eventually(t, func() bool {
		data, err2 := p.pathManager.APIPathsGet("cam_one")
		return err2 == nil && data.Online
	}, 5*time.Second, 50*time.Millisecond, "cam_one did not become online again")
	source1b.Close()

	// disconnecting the readers leaves both instances without source and readers;
	// both instances and their offline resources are destroyed after the grace period
	reader1.Close()
	reader2.Close()

	require.Eventually(t, func() bool {
		_, err1 := p.pathManager.APIPathsGet("cam_one")
		_, err2 := p.pathManager.APIPathsGet("cam_two")
		return err1 != nil && err2 != nil
	}, 5*time.Second, 50*time.Millisecond, "idle dynamic instances were not destroyed")

	// the fixed path, which has no readers, is still present: its behavior is unchanged
	_, err = p.pathManager.APIPathsGet("fixed")
	require.NoError(t, err)

	// a later request on a matching name recreates a fresh instance serving the
	// offline stream, proving destruction released every resource cleanly
	reader3, received3 := startRTSPReader(t, "cam_one")
	defer reader3.Close()

	require.Eventually(t, func() bool { return received3.Load() > 0 },
		5*time.Second, 50*time.Millisecond, "recreated cam_one did not serve its offline stream")
}
