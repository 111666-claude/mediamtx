package rtmp

import "sync"

// unauthenticatedConnLimiter bounds the number of concurrent connections
// that have been accepted but have not completed authorization yet.
//
// A slot is reserved with tryAcquire before the RTMP/TLS handshake, protocol
// parsing or the OnConnect hook run, and is released exactly once when
// authorization succeeds or when the connection terminates (authentication
// failure, timeout, disconnect or server shutdown).
//
// A limit equal to zero means no limit.
type unauthenticatedConnLimiter struct {
	mutex  sync.Mutex
	limit  int
	active int
}

// setLimit changes the maximum number of active unauthenticated connections.
// It is safe to call while the server is running. A value of zero or less
// disables the limit.
func (l *unauthenticatedConnLimiter) setLimit(limit int) {
	if limit < 0 {
		limit = 0
	}

	l.mutex.Lock()
	defer l.mutex.Unlock()

	l.limit = limit
}

// tryAcquire reserves a slot. It returns false, without changing the active
// count, when a positive limit has already been reached.
func (l *unauthenticatedConnLimiter) tryAcquire() bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.limit > 0 && l.active >= l.limit {
		return false
	}

	l.active++
	return true
}

// release returns a slot previously reserved with tryAcquire.
func (l *unauthenticatedConnLimiter) release() {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.active > 0 {
		l.active--
	}
}
