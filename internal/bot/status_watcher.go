package bot

import (
	"context"
	"log"
	"sync"
	"time"
)

const (
	// statusRefreshInterval is how often a watched status message is re-rendered.
	statusRefreshInterval = 2 * time.Second
	// statusWatchTTL is how long a status message keeps refreshing without any
	// user interaction. It bounds the Telegram traffic of a message left open.
	statusWatchTTL = 5 * time.Minute
)

// watchMode is what a watched status message currently shows: the list of
// active downloads, or the details of a single request.
type watchMode struct {
	detail    bool
	requestID string
}

// statusWatch is the auto-refresh state of one status message.
type statusWatch struct {
	chatID    int64
	messageID int
	cancel    context.CancelFunc

	mu          sync.Mutex
	mode        watchMode
	fingerprint string
	deadline    time.Time
}

type StatusChecker struct {
	bot   *Bot
	store *statusStore

	interval time.Duration
	ttl      time.Duration

	// Injection points so the refresh loop can be tested without Redis or Telegram.
	renderFn func(ctx context.Context, mode watchMode) statusView
	applyFn  func(chatID int64, messageID int, view statusView) error

	watchMu sync.Mutex
	watches map[int64]*statusWatch
}

func (w *statusWatch) snapshot() (watchMode, string, time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.mode, w.fingerprint, w.deadline
}

// startWatch begins auto-refreshing a status message, replacing any watch
// already running for the chat. One watch per chat keeps the per-chat edit rate
// well inside Telegram's limits.
func (sc *StatusChecker) startWatch(chatID int64, messageID int, mode watchMode, fingerprint string) {
	ctx, cancel := context.WithCancel(context.Background())

	watch := &statusWatch{
		chatID:      chatID,
		messageID:   messageID,
		cancel:      cancel,
		mode:        mode,
		fingerprint: fingerprint,
		deadline:    time.Now().Add(sc.ttl),
	}

	sc.watchMu.Lock()
	if previous, ok := sc.watches[chatID]; ok {
		previous.cancel()
	}
	sc.watches[chatID] = watch
	sc.watchMu.Unlock()

	go sc.runWatch(ctx, watch)
}

// armWatch points the chat's existing watch at a new mode and extends its
// deadline, or starts a fresh watch when there is none for this message.
func (sc *StatusChecker) armWatch(chatID int64, messageID int, mode watchMode, fingerprint string) {
	sc.watchMu.Lock()
	watch, ok := sc.watches[chatID]
	sc.watchMu.Unlock()

	if !ok || watch.messageID != messageID {
		sc.startWatch(chatID, messageID, mode, fingerprint)
		return
	}

	watch.mu.Lock()
	watch.mode = mode
	watch.fingerprint = fingerprint
	watch.deadline = time.Now().Add(sc.ttl)
	watch.mu.Unlock()
}

func (sc *StatusChecker) stopWatch(chatID int64) {
	sc.watchMu.Lock()
	watch, ok := sc.watches[chatID]
	if ok {
		delete(sc.watches, chatID)
	}
	sc.watchMu.Unlock()

	if ok {
		watch.cancel()
	}
}

// stopThisWatch removes a watch only if it is still the current one for its
// chat, so a loop shutting itself down cannot drop a newer watch.
func (sc *StatusChecker) stopThisWatch(watch *statusWatch) {
	sc.watchMu.Lock()
	if current, ok := sc.watches[watch.chatID]; ok && current == watch {
		delete(sc.watches, watch.chatID)
	}
	sc.watchMu.Unlock()

	watch.cancel()
}

// StopAll cancels every running refresh loop. Called on bot shutdown.
func (sc *StatusChecker) StopAll() {
	sc.watchMu.Lock()
	watches := make([]*statusWatch, 0, len(sc.watches))
	for _, watch := range sc.watches {
		watches = append(watches, watch)
	}
	sc.watches = make(map[int64]*statusWatch)
	sc.watchMu.Unlock()

	for _, watch := range watches {
		watch.cancel()
	}
}

func (sc *StatusChecker) watchFor(chatID int64) *statusWatch {
	sc.watchMu.Lock()
	defer sc.watchMu.Unlock()
	return sc.watches[chatID]
}

func (sc *StatusChecker) watchCount() int {
	sc.watchMu.Lock()
	defer sc.watchMu.Unlock()
	return len(sc.watches)
}

func (sc *StatusChecker) runWatch(ctx context.Context, watch *statusWatch) {
	ticker := time.NewTicker(sc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !sc.tick(ctx, watch) {
				sc.stopThisWatch(watch)
				return
			}
		}
	}
}

// tick re-renders the watched message and reports whether the loop should keep
// running.
func (sc *StatusChecker) tick(ctx context.Context, watch *statusWatch) bool {
	mode, previousFingerprint, deadline := watch.snapshot()
	expired := time.Now().After(deadline)

	view := sc.renderFn(ctx, mode)
	if expired && !view.Done {
		view.Text += pausedSuffix
	}

	fingerprint := view.fingerprint()
	if fingerprint != previousFingerprint {
		if err := sc.applyFn(watch.chatID, watch.messageID, view); err != nil {
			log.Printf("Failed to auto-refresh status message %d in chat %d: %v", watch.messageID, watch.chatID, err)
			return !isPermanentTelegramError(err)
		}

		watch.mu.Lock()
		// Only record the fingerprint if the user has not switched view in the
		// meantime; otherwise the next tick would skip a needed edit.
		if watch.mode == mode {
			watch.fingerprint = fingerprint
		}
		watch.mu.Unlock()
	}

	return !view.Done && !expired
}
