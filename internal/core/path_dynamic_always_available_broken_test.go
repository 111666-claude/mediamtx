package core

import (
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/test"
)

// TestPathDynamicAlwaysAvailableBrokenTemplate verifies that a dynamic path
// whose offline segment cannot be initialized does not crash the server and
// does not leak the instance: it stays briefly in an unavailable state and is
// removed during idle cleanup.
func TestPathDynamicAlwaysAvailableBrokenTemplate(t *testing.T) {
	// the file is missing at instance creation time, so building the offline
	// segment fails while the path itself still matches the configuration
	offlineFile := filepath.Join(t.TempDir(), "offline.mp4")

	pathConfs := map[string]*conf.Path{
		"cam": {
			Regexp:              regexp.MustCompile("^cam_(.+)$"),
			Name:                "cam",
			Source:              "publisher",
			AlwaysAvailable:     true,
			AlwaysAvailableFile: offlineFile,
		},
	}

	pm := &pathManager{
		authManager: test.NilAuthManager,
		pathConfs:   pathConfs,
		parent:      test.NilLogger,
	}
	pm.initialize()
	defer pm.close()

	// requesting a matching name must not panic: the reader gets an error
	var (
		panicked any
		err      error
	)
	func() {
		defer func() { panicked = recover() }()
		_, err = pm.AddReader(defs.PathAddReaderReq{
			Author: &dummyReader{},
			AccessRequest: defs.PathAccessRequest{
				Name: "cam_one",
			},
		})
	}()
	require.Nil(t, panicked, "a broken offline template must not crash the server")
	require.Error(t, err)

	// the instance exists while being unavailable, then is destroyed after the
	// idle grace period, leaving no leaked path or offline resource
	require.Eventually(t, func() bool {
		data, err2 := pm.APIPathsList()
		require.NoError(t, err2)
		return len(data.Items) == 0
	}, 5*time.Second, 50*time.Millisecond)
}
