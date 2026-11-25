package recordstore

import (
	"github.com/bluenviron/mediamtx/internal/mp4box"
)

// Mtxi is a MediaMTX segment info.
//
// It is defined in the mp4box package so it can be registered with the MP4
// box parser even in binaries and tests that do not import recordstore.
type Mtxi = mp4box.Mtxi
