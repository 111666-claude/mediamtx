package playback

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/recordstore"
)

type coverageSegment struct {
	Path  string    `json:"path"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func newCoverageSegment(seg recordstore.CoverageSegment) coverageSegment {
	return coverageSegment{
		Path:  seg.Fpath,
		Start: seg.Start,
		End:   seg.End,
	}
}

type coverageInterval struct {
	Start    time.Time         `json:"start"`
	End      time.Time         `json:"end"`
	Segments []coverageSegment `json:"segments"`
}

type coverageGap struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type coverageSkippedSegment struct {
	Path   string    `json:"path"`
	Start  time.Time `json:"start"`
	Reason string    `json:"reason"`
}

type coverageRes struct {
	Start     time.Time                `json:"start"`
	End       time.Time                `json:"end"`
	Intervals []coverageInterval       `json:"intervals"`
	Gaps      []coverageGap            `json:"gaps"`
	Skipped   []coverageSkippedSegment `json:"skipped"`
}

func newCoverageRes(coverage *recordstore.Coverage) *coverageRes {
	res := &coverageRes{
		Start:     coverage.Start,
		End:       coverage.End,
		Intervals: make([]coverageInterval, len(coverage.Intervals)),
		Gaps:      make([]coverageGap, len(coverage.Gaps)),
		Skipped:   make([]coverageSkippedSegment, len(coverage.Skipped)),
	}

	for i, interval := range coverage.Intervals {
		res.Intervals[i] = coverageInterval{
			Start:    interval.Start,
			End:      interval.End,
			Segments: make([]coverageSegment, len(interval.Segments)),
		}

		for j, seg := range interval.Segments {
			res.Intervals[i].Segments[j] = newCoverageSegment(seg)
		}
	}

	for i, gap := range coverage.Gaps {
		res.Gaps[i] = coverageGap{
			Start: gap.Start,
			End:   gap.End,
		}
	}

	for i, skipped := range coverage.Skipped {
		res.Skipped[i] = coverageSkippedSegment{
			Path:   skipped.Fpath,
			Start:  skipped.Start,
			Reason: skipped.Reason.Error(),
		}
	}

	return res
}

func (s *Server) onCoverage(ctx *gin.Context) {
	pathName := ctx.Query("path")

	// validate path name before passing it to the authentication manager
	err := conf.IsValidPathName(pathName)
	if err != nil {
		s.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	if !s.doAuth(ctx, pathName) {
		return
	}

	pathConf, err := s.safeFindPathConf(pathName)
	if err != nil {
		s.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	var start time.Time
	rawStart := ctx.Query("start")
	if rawStart == "" {
		s.writeError(ctx, http.StatusBadRequest, errors.New("'start' parameter is required"))
		return
	}
	start, err = time.Parse(time.RFC3339, rawStart)
	if err != nil {
		s.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid 'start' parameter: %w", err))
		return
	}

	var end time.Time
	rawEnd := ctx.Query("end")
	if rawEnd == "" {
		s.writeError(ctx, http.StatusBadRequest, errors.New("'end' parameter is required"))
		return
	}
	end, err = time.Parse(time.RFC3339, rawEnd)
	if err != nil {
		s.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid 'end' parameter: %w", err))
		return
	}

	coverage, err := recordstore.FindCoverage(pathConf, pathName, start, end)
	if err != nil {
		switch {
		case errors.Is(err, recordstore.ErrInvalidInterval),
			errors.Is(err, recordstore.ErrUnsupportedRecordFormat):
			s.writeError(ctx, http.StatusBadRequest, err)

		case errors.Is(err, recordstore.ErrNoSegmentsFound):
			s.writeError(ctx, http.StatusNotFound, err)

		case errors.Is(err, recordstore.ErrTooManySegments):
			s.writeError(ctx, http.StatusRequestEntityTooLarge, err)

		default:
			s.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	ctx.JSON(http.StatusOK, newCoverageRes(coverage))
}
