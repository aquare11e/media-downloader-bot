package bot

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func viewWithButton(text, buttonText, callbackData string) statusView {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(buttonText, callbackData),
		),
	)
	return statusView{Text: text, Keyboard: &keyboard}
}

func TestStatusViewFingerprint(t *testing.T) {
	base := viewWithButton("Downloads", "movie [###-------] 30.0%", "status_1")

	tests := []struct {
		name     string
		view     statusView
		wantSame bool
	}{
		{"identical", viewWithButton("Downloads", "movie [###-------] 30.0%", "status_1"), true},
		{"text changed", viewWithButton("Other", "movie [###-------] 30.0%", "status_1"), false},
		{"button label changed", viewWithButton("Downloads", "movie [####------] 40.0%", "status_1"), false},
		{"callback data changed", viewWithButton("Downloads", "movie [###-------] 30.0%", "status_2"), false},
		{"keyboard dropped", statusView{Text: "Downloads"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			same := base.fingerprint() == tt.view.fingerprint()
			if same != tt.wantSame {
				t.Errorf("fingerprint equality: got %v, want %v", same, tt.wantSame)
			}
		})
	}
}

// recorder captures the views the watcher pushes to Telegram.
type recorder struct {
	mu     sync.Mutex
	views  []statusView
	err    error
	notify chan struct{}
}

func newRecorder() *recorder {
	return &recorder{notify: make(chan struct{}, 32)}
}

func (r *recorder) apply(_ int64, _ int, view statusView) error {
	r.mu.Lock()
	if r.err == nil {
		r.views = append(r.views, view)
	}
	err := r.err
	r.mu.Unlock()

	select {
	case r.notify <- struct{}{}:
	default:
	}

	return err
}

func (r *recorder) applied() []statusView {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]statusView(nil), r.views...)
}

func newTestChecker(render func(ctx context.Context, mode watchMode) statusView, apply func(int64, int, statusView) error) *StatusChecker {
	return &StatusChecker{
		watches:  make(map[int64]*statusWatch),
		interval: time.Millisecond,
		ttl:      time.Minute,
		renderFn: render,
		applyFn:  apply,
	}
}

func newTestWatch(sc *StatusChecker, fingerprint string) *statusWatch {
	_, cancel := context.WithCancel(context.Background())
	watch := &statusWatch{
		chatID:      1,
		messageID:   2,
		cancel:      cancel,
		fingerprint: fingerprint,
		deadline:    time.Now().Add(sc.ttl),
	}
	sc.watches[watch.chatID] = watch
	return watch
}

func TestTickSkipsEditWhenNothingChanged(t *testing.T) {
	view := viewWithButton("Downloads", "movie [###-------] 30.0%", "status_1")
	rec := newRecorder()
	sc := newTestChecker(func(context.Context, watchMode) statusView { return view }, rec.apply)
	watch := newTestWatch(sc, view.fingerprint())

	if !sc.tick(context.Background(), watch) {
		t.Fatal("tick stopped the watch on an unchanged view")
	}
	if got := len(rec.applied()); got != 0 {
		t.Errorf("edits sent: got %d, want 0", got)
	}
}

func TestTickEditsWhenProgressChanges(t *testing.T) {
	updated := viewWithButton("Downloads", "movie [####------] 40.0%", "status_1")
	rec := newRecorder()
	sc := newTestChecker(func(context.Context, watchMode) statusView { return updated }, rec.apply)
	watch := newTestWatch(sc, viewWithButton("Downloads", "movie [###-------] 30.0%", "status_1").fingerprint())

	if !sc.tick(context.Background(), watch) {
		t.Fatal("tick stopped the watch while the download is still active")
	}

	applied := rec.applied()
	if len(applied) != 1 {
		t.Fatalf("edits sent: got %d, want 1", len(applied))
	}
	if applied[0].Text != updated.Text {
		t.Errorf("edited text: got %q, want %q", applied[0].Text, updated.Text)
	}
	if watch.fingerprint != updated.fingerprint() {
		t.Error("watch did not record the fingerprint it just sent")
	}
}

func TestTickStopsOnDoneView(t *testing.T) {
	rec := newRecorder()
	sc := newTestChecker(func(context.Context, watchMode) statusView {
		return statusView{Text: noDownloadsText, Done: true}
	}, rec.apply)
	watch := newTestWatch(sc, "previous")

	if sc.tick(context.Background(), watch) {
		t.Fatal("tick kept watching after the view reported Done")
	}
	if got := len(rec.applied()); got != 1 {
		t.Fatalf("edits sent: got %d, want 1", got)
	}
}

func TestTickPausesAfterDeadline(t *testing.T) {
	rec := newRecorder()
	sc := newTestChecker(func(context.Context, watchMode) statusView {
		return statusView{Text: listHeader}
	}, rec.apply)
	watch := newTestWatch(sc, "previous")
	watch.deadline = time.Now().Add(-time.Second)

	if sc.tick(context.Background(), watch) {
		t.Fatal("tick kept watching past the deadline")
	}

	applied := rec.applied()
	if len(applied) != 1 {
		t.Fatalf("edits sent: got %d, want 1", len(applied))
	}
	if !strings.HasSuffix(applied[0].Text, pausedSuffix) {
		t.Errorf("final edit did not mark auto-refresh as paused: %q", applied[0].Text)
	}
}

func TestRunWatchFollowsModeSwitch(t *testing.T) {
	rec := newRecorder()
	sc := newTestChecker(func(_ context.Context, mode watchMode) statusView {
		if mode.detail {
			return statusView{Text: "detail " + mode.requestID}
		}
		return statusView{Text: listHeader}
	}, rec.apply)

	sc.startWatch(1, 2, watchMode{}, "stale")
	defer sc.StopAll()

	waitForEdit(t, rec)
	sc.armWatch(1, 2, watchMode{detail: true, requestID: "abc"}, "stale-detail")
	waitForEdit(t, rec)

	applied := rec.applied()
	last := applied[len(applied)-1]
	if last.Text != "detail abc" {
		t.Errorf("last edit: got %q, want %q", last.Text, "detail abc")
	}
}

func TestStartWatchReplacesPreviousWatchForChat(t *testing.T) {
	rec := newRecorder()
	sc := newTestChecker(func(context.Context, watchMode) statusView {
		return statusView{Text: listHeader}
	}, rec.apply)

	sc.startWatch(1, 2, watchMode{}, "stale")
	first := sc.watchFor(1)
	sc.startWatch(1, 3, watchMode{}, "stale")
	defer sc.StopAll()

	if sc.watchFor(1) == first {
		t.Fatal("second /status did not replace the watch for the chat")
	}
	if got := sc.watchCount(); got != 1 {
		t.Errorf("watches for chat: got %d, want 1", got)
	}
}

func TestStopWatchEndsTheLoop(t *testing.T) {
	rec := newRecorder()
	sc := newTestChecker(func(context.Context, watchMode) statusView {
		return statusView{Text: listHeader}
	}, rec.apply)

	sc.startWatch(1, 2, watchMode{}, "stale")
	waitForEdit(t, rec)
	sc.stopWatch(1)

	if got := sc.watchCount(); got != 0 {
		t.Errorf("watches after stop: got %d, want 0", got)
	}
}

func waitForEdit(t *testing.T, rec *recorder) {
	t.Helper()
	select {
	case <-rec.notify:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a status edit")
	}
}
