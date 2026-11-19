package playback

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/test"
)

func newCoverageServer(t *testing.T, dir string) *Server {
	t.Helper()

	s := &Server{
		Address:      "127.0.0.1:9996",
		ReadTimeout:  conf.Duration(10 * time.Second),
		WriteTimeout: conf.Duration(10 * time.Second),
		PathConfs: map[string]*conf.Path{
			"mypath": {
				Name:         "mypath",
				RecordPath:   filepath.Join(dir, "%path/%Y-%m-%d/%Y-%m-%d_%H-%M-%S-%f"),
				RecordFormat: conf.RecordFormatFMP4,
			},
			"mpegts": {
				Name:         "mpegts",
				RecordPath:   filepath.Join(dir, "%path/%Y-%m-%d_%H-%M-%S-%f"),
				RecordFormat: conf.RecordFormatMPEGTS,
			},
		},
		AuthManager: &test.AuthManager{
			AuthenticateImpl: func(req *auth.Request) (*auth.Result, *auth.Error) {
				require.Equal(t, conf.AuthActionPlayback, req.Action)
				return &auth.Result{User: req.Credentials.User}, nil
			},
		},
		Parent: test.NilLogger,
	}

	err := s.Initialize()
	require.NoError(t, err)

	t.Cleanup(s.Close)

	return s
}

func coverageRequest(t *testing.T, query url.Values) (int, []byte) {
	t.Helper()

	u, err := url.Parse("http://localhost:9996/coverage")
	require.NoError(t, err)
	u.RawQuery = query.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return res.StatusCode, body
}

func TestOnCoverage(t *testing.T) {
	dir := t.TempDir()
	newCoverageServer(t, dir)

	segA := filepath.Join(dir, "mypath", "2024-03-10", "2024-03-10_23-55-00-000000.mp4")
	segB := filepath.Join(dir, "mypath", "2024-03-11", "2024-03-11_00-05-30-000000.mp4")

	require.NoError(t, os.MkdirAll(filepath.Dir(segA), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(segB), 0o755))
	test.WriteRecordingSegment(t, segA, 10*time.Minute, true)
	test.WriteRecordingSegment(t, segB, 5*time.Minute, true)

	query := url.Values{}
	query.Set("path", "mypath")
	query.Set("start", time.Date(2024, 3, 10, 23, 50, 0, 0, time.Local).Format(time.RFC3339))
	query.Set("end", time.Date(2024, 3, 11, 0, 20, 0, 0, time.Local).Format(time.RFC3339))

	res, body := coverageRequest(t, query)
	require.Equal(t, http.StatusOK, res)

	var out any
	require.NoError(t, json.Unmarshal(body, &out))

	require.Equal(t, map[string]any{
		"start": time.Date(2024, 3, 10, 23, 50, 0, 0, time.Local).Format(time.RFC3339Nano),
		"end":   time.Date(2024, 3, 11, 0, 20, 0, 0, time.Local).Format(time.RFC3339Nano),
		"intervals": []any{
			map[string]any{
				"start": time.Date(2024, 3, 10, 23, 55, 0, 0, time.Local).Format(time.RFC3339Nano),
				"end":   time.Date(2024, 3, 11, 0, 5, 0, 0, time.Local).Format(time.RFC3339Nano),
				"segments": []any{map[string]any{
					"path":  segA,
					"start": time.Date(2024, 3, 10, 23, 55, 0, 0, time.Local).Format(time.RFC3339Nano),
					"end":   time.Date(2024, 3, 11, 0, 5, 0, 0, time.Local).Format(time.RFC3339Nano),
				}},
			},
			map[string]any{
				"start": time.Date(2024, 3, 11, 0, 5, 30, 0, time.Local).Format(time.RFC3339Nano),
				"end":   time.Date(2024, 3, 11, 0, 10, 30, 0, time.Local).Format(time.RFC3339Nano),
				"segments": []any{map[string]any{
					"path":  segB,
					"start": time.Date(2024, 3, 11, 0, 5, 30, 0, time.Local).Format(time.RFC3339Nano),
					"end":   time.Date(2024, 3, 11, 0, 10, 30, 0, time.Local).Format(time.RFC3339Nano),
				}},
			},
		},
		"gaps": []any{
			map[string]any{
				"start": time.Date(2024, 3, 10, 23, 50, 0, 0, time.Local).Format(time.RFC3339Nano),
				"end":   time.Date(2024, 3, 10, 23, 55, 0, 0, time.Local).Format(time.RFC3339Nano),
			},
			map[string]any{
				"start": time.Date(2024, 3, 11, 0, 5, 0, 0, time.Local).Format(time.RFC3339Nano),
				"end":   time.Date(2024, 3, 11, 0, 5, 30, 0, time.Local).Format(time.RFC3339Nano),
			},
			map[string]any{
				"start": time.Date(2024, 3, 11, 0, 10, 30, 0, time.Local).Format(time.RFC3339Nano),
				"end":   time.Date(2024, 3, 11, 0, 20, 0, 0, time.Local).Format(time.RFC3339Nano),
			},
		},
		"skipped": []any{},
	}, out)
}

func TestOnCoverageErrors(t *testing.T) {
	dir := t.TempDir()
	newCoverageServer(t, dir)

	start := time.Date(2024, 3, 10, 23, 50, 0, 0, time.Local)
	end := start.Add(time.Hour)

	for _, ca := range []struct {
		name   string
		query  url.Values
		status int
	}{
		{
			name: "missing start",
			query: url.Values{
				"path": {"mypath"},
				"end":  {end.Format(time.RFC3339)},
			},
			status: http.StatusBadRequest,
		},
		{
			name: "invalid end",
			query: url.Values{
				"path":  {"mypath"},
				"start": {start.Format(time.RFC3339)},
				"end":   {"not-a-date"},
			},
			status: http.StatusBadRequest,
		},
		{
			name: "invalid interval",
			query: url.Values{
				"path":  {"mypath"},
				"start": {end.Format(time.RFC3339)},
				"end":   {start.Format(time.RFC3339)},
			},
			status: http.StatusBadRequest,
		},
		{
			name: "invalid path name",
			query: url.Values{
				"path":  {"group/../cam"},
				"start": {start.Format(time.RFC3339)},
				"end":   {end.Format(time.RFC3339)},
			},
			status: http.StatusBadRequest,
		},
		{
			name: "unknown path",
			query: url.Values{
				"path":  {"unknown"},
				"start": {start.Format(time.RFC3339)},
				"end":   {end.Format(time.RFC3339)},
			},
			status: http.StatusBadRequest,
		},
		{
			name: "unsupported format",
			query: url.Values{
				"path":  {"mpegts"},
				"start": {start.Format(time.RFC3339)},
				"end":   {end.Format(time.RFC3339)},
			},
			status: http.StatusBadRequest,
		},
		{
			name: "no segments",
			query: url.Values{
				"path":  {"mypath"},
				"start": {start.Format(time.RFC3339)},
				"end":   {end.Format(time.RFC3339)},
			},
			status: http.StatusNotFound,
		},
	} {
		t.Run(ca.name, func(t *testing.T) {
			res, _ := coverageRequest(t, ca.query)
			require.Equal(t, ca.status, res)
		})
	}
}
