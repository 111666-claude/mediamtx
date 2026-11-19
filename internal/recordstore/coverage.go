package recordstore

import (
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
)

// DefaultMaxCoverageSegments is the maximum number of recording segments that
// FindCoverage considers before giving up. It protects the server from
// unbounded work when the recordings directory contains a huge number of files.
const DefaultMaxCoverageSegments = 100000

var (
	// ErrInvalidInterval is returned when the requested time interval is invalid,
	// for instance when its end is not after its start.
	ErrInvalidInterval = errors.New("invalid time interval")

	// ErrTooManySegments is returned when the recordings directory contains more
	// segments than the configured limit.
	ErrTooManySegments = errors.New("too many recording segments")

	// ErrUnsupportedRecordFormat is returned when coverage is requested for a
	// record format whose segments do not carry a duration in their header.
	ErrUnsupportedRecordFormat = errors.New("record format does not support coverage queries")

	// ErrInvalidSegmentHeader is used as reason of a segment that has been found
	// on disk but whose header cannot be read. Such a segment cannot be played,
	// so it does not contribute to coverage.
	ErrInvalidSegmentHeader = errors.New("invalid recording segment header")

	// ErrZeroDuration is used as reason of a segment whose header reports a zero
	// duration. Such a segment covers no time at all.
	ErrZeroDuration = errors.New("recording segment has zero duration")
)

var errTooManySegments = errors.New("segment limit reached")

// CoverageSegment is a recording segment that contributes to a coverage interval.
type CoverageSegment struct {
	// Fpath is the absolute path of the segment file.
	Fpath string
	// Start is the start time encoded in the segment file name.
	Start time.Time
	// End is Start plus the duration read from the segment header.
	End time.Time
}

// CoverageInterval is a continuous time span covered by recordings.
type CoverageInterval struct {
	// Start and End delimit the covered time span.
	Start time.Time
	End   time.Time
	// Segments are the segments that cover the interval, sorted by start time.
	// Segments are not clipped to the interval, so they can extend outside it.
	Segments []CoverageSegment
}

// CoverageGap is a time span without recordings.
type CoverageGap struct {
	Start time.Time
	End   time.Time
}

// SkippedSegment is a segment that has been found on disk but does not
// contribute to coverage. The cause is available in Reason.
type SkippedSegment struct {
	Fpath  string
	Start  time.Time
	Reason error
}

// Coverage is the result of a coverage query.
type Coverage struct {
	// Start and End are the requested time range; End is exclusive.
	Start time.Time
	End   time.Time
	// Intervals are the time spans covered by recordings, sorted by start time.
	// Consecutive and overlapping segments are merged into a single interval,
	// and every interval keeps its source segments.
	Intervals []CoverageInterval
	// Gaps are the time spans inside [Start, End) that are not covered by
	// recordings, sorted by start time.
	Gaps []CoverageGap
	// Skipped contains the segments that have been ignored because they vanished
	// from disk, because their header is invalid or because they have a zero
	// duration.
	Skipped []SkippedSegment
}

type coverageReadResult struct {
	duration time.Duration
	err      error
}

// FindCoverage returns which parts of the time range [start, end) are covered by
// recording segments of the given path, and which parts are gaps.
//
// The end of each segment is computed from the duration stored in its header,
// which is read without loading the media payload. Consecutive and overlapping
// segments are merged into a single interval, and every interval keeps the list
// of segments that contribute to it.
//
// Segments that cannot be played (invalid header, zero duration) and segments
// removed while the query is running do not contribute to coverage: they are
// reported in Coverage.Skipped and their time span shows up as a gap.
func FindCoverage(
	pathConf *conf.Path,
	pathName string,
	start time.Time,
	end time.Time,
) (*Coverage, error) {
	return FindCoverageWithLimit(pathConf, pathName, start, end, DefaultMaxCoverageSegments)
}

// FindCoverageWithLimit is like FindCoverage, but uses a custom maximum number
// of segments. A non-positive value restores DefaultMaxCoverageSegments.
func FindCoverageWithLimit(
	pathConf *conf.Path,
	pathName string,
	start time.Time,
	end time.Time,
	maxSegments int,
) (*Coverage, error) {
	if !end.After(start) {
		return nil, fmt.Errorf("%w: end (%s) must be after start (%s)",
			ErrInvalidInterval,
			end.Format(time.RFC3339Nano),
			start.Format(time.RFC3339Nano))
	}

	if pathConf.RecordFormat != conf.RecordFormatFMP4 {
		return nil, fmt.Errorf("%w: format is '%s', only '%s' segments store a duration in their header",
			ErrUnsupportedRecordFormat, pathConf.RecordFormat, conf.RecordFormatFMP4)
	}

	if maxSegments <= 0 {
		maxSegments = DefaultMaxCoverageSegments
	}

	segments, commonPath, err := findSegmentsInDir(pathConf, pathName, nil, maxSegments)
	if err != nil {
		switch {
		case errors.Is(err, errTooManySegments):
			return nil, fmt.Errorf("%w for path '%s' in '%s': limit is %d",
				ErrTooManySegments, pathName, commonPath, maxSegments)

		case errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("%w for path '%s' in '%s': %w",
				ErrNoSegmentsFound, pathName, commonPath, err)

		default:
			return nil, fmt.Errorf("cannot scan recordings of path '%s' in '%s': %w",
				pathName, commonPath, err)
		}
	}

	if len(segments) == 0 {
		return nil, fmt.Errorf("%w for path '%s' in '%s'", ErrNoSegmentsFound, pathName, commonPath)
	}

	sort.Slice(segments, func(i, j int) bool {
		return segments[i].Start.Before(segments[j].Start)
	})

	// only segments that start before the end of the range can intersect it;
	// the headers of the following ones are not read at all.
	readCount := 0
	for readCount < len(segments) && segments[readCount].Start.Before(end) {
		readCount++
	}

	reads := readCoverageSegmentDurations(segments[:readCount])

	coverage := &Coverage{
		Start: start,
		End:   end,
	}

	candidates := make([]CoverageSegment, 0, readCount)

	for i := range readCount {
		seg := segments[i]
		read := reads[i]

		switch {
		case errors.Is(read.err, fs.ErrNotExist):
			// the file may have been removed by the cleaner between the scan
			// and the header read.
			coverage.Skipped = append(coverage.Skipped, SkippedSegment{
				Fpath:  seg.Fpath,
				Start:  seg.Start,
				Reason: fmt.Errorf("cannot read header of recording segment '%s': %w", seg.Fpath, read.err),
			})

		case read.err != nil:
			// an empty segment has a valid header but no media part: it covers
			// no time, like a segment with a zero duration.
			if errors.Is(read.err, ErrSegmentEmpty) {
				coverage.Skipped = append(coverage.Skipped, SkippedSegment{
					Fpath: seg.Fpath,
					Start: seg.Start,
					Reason: fmt.Errorf("%w '%s': %w",
						ErrZeroDuration, seg.Fpath, read.err),
				})
				continue
			}

			coverage.Skipped = append(coverage.Skipped, SkippedSegment{
				Fpath: seg.Fpath,
				Start: seg.Start,
				Reason: fmt.Errorf("%w '%s': %w",
					ErrInvalidSegmentHeader, seg.Fpath, read.err),
			})

		case read.duration <= 0:
			coverage.Skipped = append(coverage.Skipped, SkippedSegment{
				Fpath:  seg.Fpath,
				Start:  seg.Start,
				Reason: fmt.Errorf("%w '%s'", ErrZeroDuration, seg.Fpath),
			})

		default:
			segEnd := seg.Start.Add(read.duration)
			if !segEnd.After(start) {
				continue
			}

			candidates = append(candidates, CoverageSegment{
				Fpath: seg.Fpath,
				Start: seg.Start,
				End:   segEnd,
			})
		}
	}

	// when two segments start at the same time, process the longer one first.
	sort.SliceStable(candidates, func(i, j int) bool {
		if !candidates[i].Start.Equal(candidates[j].Start) {
			return candidates[i].Start.Before(candidates[j].Start)
		}
		return candidates[i].End.After(candidates[j].End)
	})

	for _, seg := range candidates {
		segStart := seg.Start
		if segStart.Before(start) {
			segStart = start
		}

		segEnd := seg.End
		if segEnd.After(end) {
			segEnd = end
		}

		if !segEnd.After(segStart) {
			continue
		}

		// merge with the previous interval when they touch or overlap.
		if len(coverage.Intervals) != 0 {
			last := &coverage.Intervals[len(coverage.Intervals)-1]
			if !segStart.After(last.End) {
				if segEnd.After(last.End) {
					last.End = segEnd
				}
				last.Segments = append(last.Segments, seg)
				continue
			}
		}

		coverage.Intervals = append(coverage.Intervals, CoverageInterval{
			Start:    segStart,
			End:      segEnd,
			Segments: []CoverageSegment{seg},
		})
	}

	pos := start
	for _, interval := range coverage.Intervals {
		if interval.Start.After(pos) {
			coverage.Gaps = append(coverage.Gaps, CoverageGap{
				Start: pos,
				End:   interval.Start,
			})
		}
		pos = interval.End
	}
	if pos.Before(end) {
		coverage.Gaps = append(coverage.Gaps, CoverageGap{
			Start: pos,
			End:   end,
		})
	}

	return coverage, nil
}

func readCoverageSegmentDurations(segments []*Segment) []coverageReadResult {
	results := make([]coverageReadResult, len(segments))

	workers := min(runtime.NumCPU(), len(segments))
	if workers < 1 {
		return results
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)

	for range workers {
		go func() {
			defer wg.Done()

			for i := range jobs {
				_, duration, err := SegmentFMP4ReadHeaderAndDuration(segments[i].Fpath)
				results[i] = coverageReadResult{
					duration: duration,
					err:      err,
				}
			}
		}()
	}

	for i := range segments {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return results
}
