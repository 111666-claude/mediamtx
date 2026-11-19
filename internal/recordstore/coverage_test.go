package recordstore_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/recordstore"
	"github.com/bluenviron/mediamtx/internal/test"
)

func writeCoverageSegment(
	t *testing.T,
	fpath string,
	duration time.Duration,
) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(fpath), 0o755)
	require.NoError(t, err)

	test.WriteRecordingSegment(t, fpath, duration, true)
}

func fmp4PathConf(dir string, recordPath string) *conf.Path {
	return &conf.Path{
		Name:         "mypath",
		RecordPath:   filepath.Join(dir, recordPath),
		RecordFormat: conf.RecordFormatFMP4,
	}
}

func coverageSegmentPaths(interval recordstore.CoverageInterval) []string {
	out := make([]string, len(interval.Segments))
	for i, seg := range interval.Segments {
		out[i] = seg.Fpath
	}
	return out
}

func TestFindCoverage(t *testing.T) {
	dir := t.TempDir()
	pathConf := fmp4PathConf(dir, "%path/%Y-%m-%d_%H-%M-%S-%f")

	segA := filepath.Join(dir, "mypath", "2024-03-09_22-15-00-000000.mp4")
	segB := filepath.Join(dir, "mypath", "2024-03-09_22-25-00-000000.mp4")
	segC := filepath.Join(dir, "mypath", "2024-03-09_22-50-00-000000.mp4")
	segD := filepath.Join(dir, "mypath", "2024-03-09_22-55-00-000000.mp4")
	segZero := filepath.Join(dir, "mypath", "2024-03-09_22-40-00-000000.mp4")

	writeCoverageSegment(t, segA, 10*time.Minute)
	writeCoverageSegment(t, segB, 10*time.Minute)
	writeCoverageSegment(t, segC, 5*time.Minute)
	writeCoverageSegment(t, segD, 5*time.Minute)
	writeCoverageSegment(t, segZero, 0)

	start := time.Date(2024, 3, 9, 22, 0, 0, 0, time.Local)
	end := time.Date(2024, 3, 9, 23, 10, 0, 0, time.Local)

	coverage, err := recordstore.FindCoverage(pathConf, "mypath", start, end)
	require.NoError(t, err)

	require.Equal(t, start, coverage.Start)
	require.Equal(t, end, coverage.End)

	require.Equal(t, []recordstore.CoverageInterval{
		{
			Start: time.Date(2024, 3, 9, 22, 15, 0, 0, time.Local),
			End:   time.Date(2024, 3, 9, 22, 35, 0, 0, time.Local),
			Segments: []recordstore.CoverageSegment{
				{
					Fpath: segA,
					Start: time.Date(2024, 3, 9, 22, 15, 0, 0, time.Local),
					End:   time.Date(2024, 3, 9, 22, 25, 0, 0, time.Local),
				},
				{
					Fpath: segB,
					Start: time.Date(2024, 3, 9, 22, 25, 0, 0, time.Local),
					End:   time.Date(2024, 3, 9, 22, 35, 0, 0, time.Local),
				},
			},
		},
		{
			Start: time.Date(2024, 3, 9, 22, 50, 0, 0, time.Local),
			End:   time.Date(2024, 3, 9, 23, 0, 0, 0, time.Local),
			Segments: []recordstore.CoverageSegment{
				{
					Fpath: segC,
					Start: time.Date(2024, 3, 9, 22, 50, 0, 0, time.Local),
					End:   time.Date(2024, 3, 9, 22, 55, 0, 0, time.Local),
				},
				{
					Fpath: segD,
					Start: time.Date(2024, 3, 9, 22, 55, 0, 0, time.Local),
					End:   time.Date(2024, 3, 9, 23, 0, 0, 0, time.Local),
				},
			},
		},
	}, coverage.Intervals)

	require.Equal(t, []recordstore.CoverageGap{
		{
			Start: time.Date(2024, 3, 9, 22, 0, 0, 0, time.Local),
			End:   time.Date(2024, 3, 9, 22, 15, 0, 0, time.Local),
		},
		{
			Start: time.Date(2024, 3, 9, 22, 35, 0, 0, time.Local),
			End:   time.Date(2024, 3, 9, 22, 50, 0, 0, time.Local),
		},
		{
			Start: time.Date(2024, 3, 9, 23, 0, 0, 0, time.Local),
			End:   time.Date(2024, 3, 9, 23, 10, 0, 0, time.Local),
		},
	}, coverage.Gaps)

	require.Len(t, coverage.Skipped, 1)
	require.Equal(t, segZero, coverage.Skipped[0].Fpath)
	require.ErrorIs(t, coverage.Skipped[0].Reason, recordstore.ErrZeroDuration)
}

func TestFindCoverageOverlapping(t *testing.T) {
	dir := t.TempDir()
	pathConf := fmp4PathConf(dir, "%path/%Y-%m-%d_%H-%M-%S-%f")

	segA := filepath.Join(dir, "mypath", "2024-03-09_10-00-00-000000.mp4")
	segB := filepath.Join(dir, "mypath", "2024-03-09_10-02-00-000000.mp4")
	segC := filepath.Join(dir, "mypath", "2024-03-09_10-05-00-000000.mp4")

	writeCoverageSegment(t, segA, 4*time.Minute)
	writeCoverageSegment(t, segB, 4*time.Minute)
	writeCoverageSegment(t, segC, 2*time.Minute)

	start := time.Date(2024, 3, 9, 9, 0, 0, 0, time.Local)
	end := time.Date(2024, 3, 9, 11, 0, 0, 0, time.Local)

	coverage, err := recordstore.FindCoverage(pathConf, "mypath", start, end)
	require.NoError(t, err)

	require.Equal(t, []recordstore.CoverageInterval{{
		Start: time.Date(2024, 3, 9, 10, 0, 0, 0, time.Local),
		End:   time.Date(2024, 3, 9, 10, 7, 0, 0, time.Local),
		Segments: []recordstore.CoverageSegment{
			{
				Fpath: segA,
				Start: time.Date(2024, 3, 9, 10, 0, 0, 0, time.Local),
				End:   time.Date(2024, 3, 9, 10, 4, 0, 0, time.Local),
			},
			{
				Fpath: segB,
				Start: time.Date(2024, 3, 9, 10, 2, 0, 0, time.Local),
				End:   time.Date(2024, 3, 9, 10, 6, 0, 0, time.Local),
			},
			{
				Fpath: segC,
				Start: time.Date(2024, 3, 9, 10, 5, 0, 0, time.Local),
				End:   time.Date(2024, 3, 9, 10, 7, 0, 0, time.Local),
			},
		},
	}}, coverage.Intervals)

	require.Equal(t, []recordstore.CoverageGap{
		{
			Start: start,
			End:   time.Date(2024, 3, 9, 10, 0, 0, 0, time.Local),
		},
		{
			Start: time.Date(2024, 3, 9, 10, 7, 0, 0, time.Local),
			End:   end,
		},
	}, coverage.Gaps)
}

func TestFindCoverageCrossDay(t *testing.T) {
	dir := t.TempDir()

	// the path template places each segment in a folder named after its day.
	pathConf := fmp4PathConf(dir, "%path/%Y-%m-%d/%Y-%m-%d_%H-%M-%S-%f")

	segA := filepath.Join(dir, "mypath", "2024-03-10", "2024-03-10_23-55-00-000000.mp4")
	segB := filepath.Join(dir, "mypath", "2024-03-11", "2024-03-11_00-05-30-000000.mp4")

	// this segment starts before midnight and ends after it.
	writeCoverageSegment(t, segA, 10*time.Minute)
	writeCoverageSegment(t, segB, 5*time.Minute)

	start := time.Date(2024, 3, 10, 23, 50, 0, 0, time.Local)
	end := time.Date(2024, 3, 11, 0, 20, 0, 0, time.Local)

	coverage, err := recordstore.FindCoverage(pathConf, "mypath", start, end)
	require.NoError(t, err)

	require.Equal(t, []recordstore.CoverageInterval{
		{
			Start: time.Date(2024, 3, 10, 23, 55, 0, 0, time.Local),
			End:   time.Date(2024, 3, 11, 0, 5, 0, 0, time.Local),
			Segments: []recordstore.CoverageSegment{{
				Fpath: segA,
				Start: time.Date(2024, 3, 10, 23, 55, 0, 0, time.Local),
				End:   time.Date(2024, 3, 11, 0, 5, 0, 0, time.Local),
			}},
		},
		{
			Start: time.Date(2024, 3, 11, 0, 5, 30, 0, time.Local),
			End:   time.Date(2024, 3, 11, 0, 10, 30, 0, time.Local),
			Segments: []recordstore.CoverageSegment{{
				Fpath: segB,
				Start: time.Date(2024, 3, 11, 0, 5, 30, 0, time.Local),
				End:   time.Date(2024, 3, 11, 0, 10, 30, 0, time.Local),
			}},
		},
	}, coverage.Intervals)

	require.Equal(t, []recordstore.CoverageGap{
		{
			Start: start,
			End:   time.Date(2024, 3, 10, 23, 55, 0, 0, time.Local),
		},
		{
			Start: time.Date(2024, 3, 11, 0, 5, 0, 0, time.Local),
			End:   time.Date(2024, 3, 11, 0, 5, 30, 0, time.Local),
		},
		{
			Start: time.Date(2024, 3, 11, 0, 10, 30, 0, time.Local),
			End:   end,
		},
	}, coverage.Gaps)
}

func TestFindCoverageUnixTemplate(t *testing.T) {
	dir := t.TempDir()
	pathConf := fmp4PathConf(dir, "%path/%s")

	start := time.Date(2024, 3, 9, 10, 0, 0, 0, time.UTC)

	seg := filepath.Join(dir, "mypath", "1709978400.mp4")
	require.Equal(t, start.Unix(), int64(1709978400))

	writeCoverageSegment(t, seg, 2*time.Minute)

	coverage, err := recordstore.FindCoverage(
		pathConf,
		"mypath",
		start.Add(-time.Minute),
		start.Add(3*time.Minute),
	)
	require.NoError(t, err)

	require.Len(t, coverage.Intervals, 1)
	require.True(t, start.Equal(coverage.Intervals[0].Start))
	require.True(t, start.Add(2*time.Minute).Equal(coverage.Intervals[0].End))
	require.Equal(t, []string{seg}, coverageSegmentPaths(coverage.Intervals[0]))
	require.Len(t, coverage.Gaps, 2)
}

func TestFindCoverageClipsToRange(t *testing.T) {
	dir := t.TempDir()
	pathConf := fmp4PathConf(dir, "%path/%Y-%m-%d_%H-%M-%S-%f")

	seg := filepath.Join(dir, "mypath", "2024-03-09_22-15-00-000000.mp4")
	writeCoverageSegment(t, seg, 10*time.Minute)

	start := time.Date(2024, 3, 9, 22, 20, 0, 0, time.Local)
	end := time.Date(2024, 3, 9, 22, 30, 0, 0, time.Local)

	coverage, err := recordstore.FindCoverage(pathConf, "mypath", start, end)
	require.NoError(t, err)

	// both interval and its source segment are intersected with the range.
	require.Equal(t, []recordstore.CoverageInterval{{
		Start: start,
		End:   time.Date(2024, 3, 9, 22, 25, 0, 0, time.Local),
		Segments: []recordstore.CoverageSegment{{
			Fpath: seg,
			Start: time.Date(2024, 3, 9, 22, 15, 0, 0, time.Local),
			End:   time.Date(2024, 3, 9, 22, 25, 0, 0, time.Local),
		}},
	}}, coverage.Intervals)
	require.Equal(t, []recordstore.CoverageGap{{
		Start: time.Date(2024, 3, 9, 22, 25, 0, 0, time.Local),
		End:   end,
	}}, coverage.Gaps)
}

func TestFindCoverageRangeOutsideSegments(t *testing.T) {
	dir := t.TempDir()
	pathConf := fmp4PathConf(dir, "%path/%Y-%m-%d_%H-%M-%S-%f")

	writeCoverageSegment(t,
		filepath.Join(dir, "mypath", "2024-03-09_22-15-00-000000.mp4"), time.Minute)

	for _, ca := range []struct {
		name  string
		start time.Time
		end   time.Time
		gap   recordstore.CoverageGap
	}{
		{
			name:  "range before segments",
			start: time.Date(2024, 3, 9, 20, 0, 0, 0, time.Local),
			end:   time.Date(2024, 3, 9, 21, 0, 0, 0, time.Local),
			gap: recordstore.CoverageGap{
				Start: time.Date(2024, 3, 9, 20, 0, 0, 0, time.Local),
				End:   time.Date(2024, 3, 9, 21, 0, 0, 0, time.Local),
			},
		},
		{
			name:  "range after segments",
			start: time.Date(2024, 3, 9, 23, 0, 0, 0, time.Local),
			end:   time.Date(2024, 3, 10, 0, 0, 0, 0, time.Local),
			gap: recordstore.CoverageGap{
				Start: time.Date(2024, 3, 9, 23, 0, 0, 0, time.Local),
				End:   time.Date(2024, 3, 10, 0, 0, 0, 0, time.Local),
			},
		},
	} {
		t.Run(ca.name, func(t *testing.T) {
			coverage, err := recordstore.FindCoverage(pathConf, "mypath", ca.start, ca.end)
			require.NoError(t, err)
			require.Empty(t, coverage.Intervals)
			require.Equal(t, []recordstore.CoverageGap{ca.gap}, coverage.Gaps)
		})
	}
}

func TestFindCoverageInvalidInterval(t *testing.T) {
	dir := t.TempDir()
	pathConf := fmp4PathConf(dir, "%path/%Y-%m-%d_%H-%M-%S-%f")

	now := time.Date(2024, 3, 9, 22, 15, 0, 0, time.Local)

	for _, ca := range []struct {
		name  string
		start time.Time
		end   time.Time
	}{
		{"equal", now, now},
		{"reversed", now, now.Add(-time.Minute)},
	} {
		t.Run(ca.name, func(t *testing.T) {
			_, err := recordstore.FindCoverage(pathConf, "mypath", ca.start, ca.end)
			require.ErrorIs(t, err, recordstore.ErrInvalidInterval)
		})
	}
}

func TestFindCoverageNoSegments(t *testing.T) {
	dir := t.TempDir()
	pathConf := fmp4PathConf(dir, "%path/%Y-%m-%d_%H-%M-%S-%f")

	now := time.Date(2024, 3, 9, 22, 15, 0, 0, time.Local)

	// the common directory does not exist.
	_, err := recordstore.FindCoverage(pathConf, "mypath", now, now.Add(time.Hour))
	require.ErrorIs(t, err, recordstore.ErrNoSegmentsFound)
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.Contains(t, err.Error(), "mypath")
	require.Contains(t, err.Error(), dir)

	// the common directory exists but contains no segments.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "mypath"), 0o755))

	_, err = recordstore.FindCoverage(pathConf, "mypath", now, now.Add(time.Hour))
	require.ErrorIs(t, err, recordstore.ErrNoSegmentsFound)
	require.Contains(t, err.Error(), "mypath")
}

func TestFindCoverageInvalidPath(t *testing.T) {
	dir := t.TempDir()
	pathConf := fmp4PathConf(dir, "%path/%Y-%m-%d_%H-%M-%S-%f")

	now := time.Date(2024, 3, 9, 22, 15, 0, 0, time.Local)

	_, err := recordstore.FindCoverage(pathConf, "group/../cam", now, now.Add(time.Hour))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid path name")
}

func TestFindCoverageUnsupportedFormat(t *testing.T) {
	dir := t.TempDir()

	pathConf := &conf.Path{
		Name:         "mypath",
		RecordPath:   filepath.Join(dir, "%path/%Y-%m-%d_%H-%M-%S-%f"),
		RecordFormat: conf.RecordFormatMPEGTS,
	}

	now := time.Date(2024, 3, 9, 22, 15, 0, 0, time.Local)

	_, err := recordstore.FindCoverage(pathConf, "mypath", now, now.Add(time.Hour))
	require.ErrorIs(t, err, recordstore.ErrUnsupportedRecordFormat)
}

func TestFindCoverageInvalidHeaders(t *testing.T) {
	dir := t.TempDir()
	pathConf := fmp4PathConf(dir, "%path/%Y-%m-%d_%H-%M-%S-%f")

	bad := filepath.Join(dir, "mypath", "2024-03-09_22-00-00-000000.mp4")
	err := os.MkdirAll(filepath.Dir(bad), 0o755)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bad, []byte("not an mp4 file"), 0o644))

	// a dangling symlink stands for a file that vanished between the directory
	// scan and the header read.
	vanish := filepath.Join(dir, "mypath", "2024-03-09_22-30-00-000000.mp4")
	require.NoError(t, os.Symlink(filepath.Join(dir, "missing.mp4"), vanish))

	// a cached duration followed by garbage proves that the media payload is
	// not read, since the garbage is never parsed.
	trailing := filepath.Join(dir, "mypath", "2024-03-09_23-00-00-000000.mp4")
	writeCoverageSegment(t, trailing, 5*time.Minute)
	f, err := os.OpenFile(trailing, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.Write(make([]byte, 1<<20))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	start := time.Date(2024, 3, 9, 21, 0, 0, 0, time.Local)
	end := time.Date(2024, 3, 10, 0, 0, 0, 0, time.Local)

	coverage, err := recordstore.FindCoverage(pathConf, "mypath", start, end)
	require.NoError(t, err)

	require.Equal(t, []recordstore.CoverageInterval{{
		Start: time.Date(2024, 3, 9, 23, 0, 0, 0, time.Local),
		End:   time.Date(2024, 3, 9, 23, 5, 0, 0, time.Local),
		Segments: []recordstore.CoverageSegment{{
			Fpath: trailing,
			Start: time.Date(2024, 3, 9, 23, 0, 0, 0, time.Local),
			End:   time.Date(2024, 3, 9, 23, 5, 0, 0, time.Local),
		}},
	}}, coverage.Intervals)

	require.Len(t, coverage.Skipped, 2)
	require.Equal(t, bad, coverage.Skipped[0].Fpath)
	require.ErrorIs(t, coverage.Skipped[0].Reason, recordstore.ErrInvalidSegmentHeader)
	require.Equal(t, vanish, coverage.Skipped[1].Fpath)
	require.ErrorIs(t, coverage.Skipped[1].Reason, fs.ErrNotExist)
}

func TestFindCoverageTooManySegments(t *testing.T) {
	dir := t.TempDir()
	pathConf := fmp4PathConf(dir, "%path/%Y-%m-%d_%H-%M-%S-%f")

	for i := range 3 {
		writeCoverageSegment(t,
			filepath.Join(dir, "mypath",
				time.Date(2024, 3, 9, 10, i, 0, 0, time.Local).Format("2006-01-02_15-04-05-000000")+".mp4"),
			time.Minute)
	}

	start := time.Date(2024, 3, 9, 9, 0, 0, 0, time.Local)
	end := time.Date(2024, 3, 9, 11, 0, 0, 0, time.Local)

	_, err := recordstore.FindCoverageWithLimit(pathConf, "mypath", start, end, 2)
	require.ErrorIs(t, err, recordstore.ErrTooManySegments)
	require.Contains(t, err.Error(), "limit is 2")
	require.Contains(t, err.Error(), "mypath")

	// exactly at the limit the query succeeds.
	coverage, err := recordstore.FindCoverageWithLimit(pathConf, "mypath", start, end, 3)
	require.NoError(t, err)
	require.Len(t, coverage.Intervals, 1)
	require.Len(t, coverage.Intervals[0].Segments, 3)
}

func TestFindCoverageSegmentsFromParts(t *testing.T) {
	dir := t.TempDir()
	pathConf := fmp4PathConf(dir, "%path/%Y-%m-%d_%H-%M-%S-%f")

	// the duration is not cached in the header, it has to be computed from the
	// duration of the parts.
	seg := filepath.Join(dir, "mypath", "2024-03-09_10-00-00-000000.mp4")
	require.NoError(t, os.MkdirAll(filepath.Dir(seg), 0o755))
	test.WriteRecordingSegment(t, seg, 1500*time.Millisecond, false)

	start := time.Date(2024, 3, 9, 9, 0, 0, 0, time.Local)
	end := time.Date(2024, 3, 9, 11, 0, 0, 0, time.Local)

	coverage, err := recordstore.FindCoverage(pathConf, "mypath", start, end)
	require.NoError(t, err)
	require.Len(t, coverage.Intervals, 1)
	require.Equal(t,
		time.Date(2024, 3, 9, 10, 0, 0, 0, time.Local),
		coverage.Intervals[0].Start)
	require.Equal(t,
		time.Date(2024, 3, 9, 10, 0, 0, 0, time.Local).Add(1500*time.Millisecond),
		coverage.Intervals[0].End)
}

func TestFindCoverageLargeDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	dir := t.TempDir()
	pathConf := fmp4PathConf(dir, "%path/%Y-%m-%d_%H-%M-%S-%f")

	first := time.Date(2024, 3, 9, 0, 0, 0, 0, time.Local)
	segDir := filepath.Join(dir, "mypath")
	require.NoError(t, os.MkdirAll(segDir, 0o755))

	// one day of 5-minute segments, each one a real but small fMP4 file.
	for i := range 24 * 12 {
		start := first.Add(time.Duration(i) * 5 * time.Minute)
		test.WriteRecordingSegment(t,
			filepath.Join(segDir, start.Format("2006-01-02_15-04-05-000000")+".mp4"),
			time.Minute, true)
	}

	start := first.Add(20 * time.Hour)
	end := start.Add(30 * time.Minute)

	coverage, err := recordstore.FindCoverage(pathConf, "mypath", start, end)
	require.NoError(t, err)

	// one gap of 4 minutes between each segment, all inside the requested range.
	require.Len(t, coverage.Intervals, 6)
	require.Len(t, coverage.Gaps, 6)
	require.Empty(t, coverage.Skipped)

	for _, interval := range coverage.Intervals {
		require.Len(t, interval.Segments, 1)
		require.Equal(t, time.Minute, interval.End.Sub(interval.Start))
	}
}
