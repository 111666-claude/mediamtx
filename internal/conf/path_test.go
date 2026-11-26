package conf

import (
	"fmt"
	"testing"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/logger"
)

func TestPathClone(t *testing.T) {
	original := &Path{
		Name:                "example",
		RTSPTransport:       RTSPTransport{new(gortsplib.ProtocolUDP)},
		SourceAnyPortEnable: new(true),
		RecordPath:          "/var/recordings",
	}

	clone := original.Clone()
	require.Equal(t, original, clone)
}

func TestIsValidPathName(t *testing.T) {
	for _, ca := range []struct {
		name   string
		path   string
		errMsg string
	}{
		{
			name: "valid nested path",
			path: "group/cam1",
		},
		{
			name: "valid dots inside segment",
			path: "cam.v1/main",
		},
		{
			name:   "parent directory",
			path:   "../cam1",
			errMsg: "can't contain dot path segments",
		},
		{
			name:   "embedded parent directory",
			path:   "group/../cam1",
			errMsg: "can't contain dot path segments",
		},
		{
			name:   "current directory",
			path:   "./cam1",
			errMsg: "can't contain dot path segments",
		},
	} {
		t.Run(ca.name, func(t *testing.T) {
			err := IsValidPathName(ca.path)
			if ca.errMsg != "" {
				require.EqualError(t, err, ca.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRecordPathTimeZoneWarning(t *testing.T) {
	for _, ca := range []struct {
		name       string
		record     string
		recordPath string
		warn       bool
	}{
		{
			"without %z",
			"yes",
			"./recordings/%path/%Y-%m-%d_%H-%M-%S-%f",
			true,
		},
		{
			"with %z",
			"yes",
			"./recordings/%path/%Y-%m-%d_%H-%M-%S-%f_%z",
			false,
		},
		{
			"unix epoch",
			"yes",
			"./recordings/%path/%s.%f",
			false,
		},
		{
			"recording disabled",
			"no",
			"./recordings/%path/%Y-%m-%d_%H-%M-%S-%f",
			false,
		},
	} {
		t.Run(ca.name, func(t *testing.T) {
			tmpf := createTempFile(t, []byte(
				"paths:\n"+
					"  test:\n"+
					"    record: "+ca.record+"\n"+
					"    recordPath: "+ca.recordPath+"\n"))

			l := &warningCounter{}
			_, _, err := Load(tmpf, nil, l)
			require.NoError(t, err)

			if ca.warn {
				require.Equal(t, 1, l.count)
				require.Contains(t, l.last, "path 'test'")
				require.Contains(t, l.last, "%z")
			} else {
				require.Equal(t, 0, l.count)
			}
		})
	}
}

type warningCounter struct {
	count int
	last  string
}

// Log implements logger.Writer.
func (w *warningCounter) Log(level logger.Level, format string, args ...any) {
	if level == logger.Warn {
		w.count++
		w.last = fmt.Sprintf(format, args...)
	}
}

func TestInternalSource(t *testing.T) {
	for _, ca := range []struct {
		name string
		yml  string
		err  string
	}{
		{
			name: "valid",
			yml: "paths:\n" +
				"  source:\n" +
				"  target:\n" +
				"    source: internal://source\n",
		},
		{
			name: "valid chain",
			yml: "paths:\n" +
				"  source:\n" +
				"  middle:\n" +
				"    source: internal://source\n" +
				"  target:\n" +
				"    source: internal://middle\n",
		},
		{
			name: "invalid path name",
			yml: "paths:\n" +
				"  target:\n" +
				"    source: internal://bad path\n",
			err: "invalid internal source path 'bad path': can contain only alphanumeric characters, underscore, dot, minus, slash",
		},
		{
			name: "self reference",
			yml: "paths:\n" +
				"  target:\n" +
				"    source: internal://target\n",
			err: "path 'target' can't use itself as internal source",
		},
		{
			name: "two node cycle",
			yml: "paths:\n" +
				"  a:\n" +
				"    source: internal://b\n" +
				"  b:\n" +
				"    source: internal://a\n",
			err: "internal source cycle detected: a -> b -> a",
		},
		{
			name: "three node cycle",
			yml: "paths:\n" +
				"  a:\n" +
				"    source: internal://b\n" +
				"  b:\n" +
				"    source: internal://c\n" +
				"  c:\n" +
				"    source: internal://a\n",
			err: "internal source cycle detected: a -> b -> c -> a",
		},
		{
			name: "regexp path",
			yml: "paths:\n" +
				"  '~^(.*)$':\n" +
				"    source: internal://source\n" +
				"    sourceOnDemand: yes\n",
			err: "an internal path source can only be used in a path without a regular expression",
		},
	} {
		t.Run(ca.name, func(t *testing.T) {
			tmpf := createTempFile(t, []byte(ca.yml))
			_, _, err := Load(tmpf, nil, nil)
			if ca.err != "" {
				require.EqualError(t, err, ca.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
