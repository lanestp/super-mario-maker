package accountapi

import (
	"sync"
	"time"
)

const (
	// * How many failed logins are allowed in a window before more are refused
	maxFailedLogins    = 10
	failedLoginsWindow = 10 * time.Minute
)

// * Slows down password guessing by counting failed logins for
// * each address and username pair
type loginLimiter struct {
	now func() time.Time

	mutex    sync.Mutex
	failures map[string][]time.Time
}

func newLoginLimiter(now func() time.Time) *loginLimiter {
	return &loginLimiter{now: now, failures: map[string][]time.Time{}}
}

// * Drops failures which are older than the window. The mutex must be held
func (l *loginLimiter) recent(key string) []time.Time {
	cutoff := l.now().Add(-failedLoginsWindow)

	kept := l.failures[key][:0]
	for _, failure := range l.failures[key] {
		if failure.After(cutoff) {
			kept = append(kept, failure)
		}
	}

	if len(kept) == 0 {
		delete(l.failures, key)
	} else {
		l.failures[key] = kept
	}

	return kept
}

func (l *loginLimiter) allowed(key string) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	return len(l.recent(key)) < maxFailedLogins
}

func (l *loginLimiter) failed(key string) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	// * Stops the map growing without limit when many different keys fail
	if len(l.failures) > 10000 {
		for other := range l.failures {
			l.recent(other)
		}
	}

	l.failures[key] = append(l.recent(key), l.now())
}
