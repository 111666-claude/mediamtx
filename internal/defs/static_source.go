package defs

import (
	"context"

	"github.com/bluenviron/mediamtx/internal/conf"
)

// StaticSourceRunParams is the set of params passed to Run().
type StaticSourceRunParams struct {
	Context        context.Context
	ResolvedSource string
	Conf           *conf.Path
	ReloadConf     chan *conf.Path

	// OnReady, if set, is called once by the source when its listener has
	// been bound or the first start attempt failed. It is used to order a
	// runOnDemand command after the source is actually listening.
	OnReady func()
}
