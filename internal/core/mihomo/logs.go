package mihomo

import (
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	mlog "github.com/metacubex/mihomo/log"
)

// logTap is the one subscriber to mihomo's log, which is how mihomo reports
// most failures to apply a config: a TUN or a listener that did not start is
// only logged. mihomo blocks every log call, and with it whatever code
// logged, until each subscriber has taken the event, so the tap does no more
// than collect errors while a caller watches. mihomo prints every event at
// its configured level to stdout itself, which the journal collects.
type logTap struct {
	mu      sync.Mutex
	changed *sync.Cond
	// watching collects the errors logged while it is not nil.
	watching []string
	// marks is the last mark logged, seen the last one that arrived.
	marks, seen uint64
}

// A mark is a debug event the tap logs itself. Debug events reach
// subscribers whatever the log level, and are not printed.
const markPrefix = "omihomo: log mark "

func startLogTap(log *slog.Logger) *logTap {
	t := &logTap{}
	t.changed = sync.NewCond(&t.mu)
	sub := mlog.Subscribe()
	go func() {
		for event := range sub {
			t.mu.Lock()
			if n, ok := strings.CutPrefix(event.Payload, markPrefix); ok && event.LogLevel == mlog.DEBUG {
				if n, err := strconv.ParseUint(n, 10, 64); err == nil && n > t.seen {
					t.seen = n
					t.changed.Broadcast()
				}
			} else if event.LogLevel >= mlog.ERROR && t.watching != nil {
				t.watching = append(t.watching, event.Payload)
			}
			t.mu.Unlock()
		}
		log.Error("mihomo's log closed")
	}()
	return t
}

// watch collects the errors mihomo logs from now until the returned stop,
// which waits until every event logged before it has reached the tap and
// returns the errors. Callers must not overlap.
func (t *logTap) watch() (stop func() []string) {
	t.mu.Lock()
	t.watching = []string{}
	t.mu.Unlock()
	return func() []string {
		// Events reach subscribers in the order they were logged, so once
		// a mark logged now arrives, so has everything logged before it.
		t.mu.Lock()
		t.marks++
		mark := t.marks
		t.mu.Unlock()
		mlog.Debugln("%s%d", markPrefix, mark)

		timedOut := false
		timer := time.AfterFunc(5*time.Second, func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			timedOut = true
			t.changed.Broadcast()
		})
		defer timer.Stop()
		t.mu.Lock()
		defer t.mu.Unlock()
		for t.seen < mark && !timedOut {
			t.changed.Wait()
		}
		errs := t.watching
		t.watching = nil
		return errs
	}
}
