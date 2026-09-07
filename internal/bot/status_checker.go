package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	coordinatorpb "github.com/aquare11e/media-downloader-bot/common/protogen/coordinator"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/redis/go-redis/v9"
)

const (
	progressBarLength = 10
	buttonTextLength  = 35
	ellipsisLength    = 2
	maxStatusRows     = 5

	redisOpTimeout = 3 * time.Second

	callbackRefreshStatus = "refresh_status"
	callbackCloseStatus   = "close_status"
	callbackStatusPrefix  = "status_"

	listHeader      = "📊 Active Downloads:"
	noDownloadsText = "📭 No active downloads found. Start a new download with /download command!"
	statusErrorText = "❌ Oops! I couldn't get the download status. Please try again later!"
	inactiveText    = "✅ This download is no longer active — it has finished or was removed."
	pausedSuffix    = "\n\n⏸ Auto-refresh paused — tap 🔄 to resume."
)

// statusView is one fully rendered status message: everything needed to send or
// edit it, plus whether there is anything left worth refreshing.
type statusView struct {
	Text     string
	Keyboard *tgbotapi.InlineKeyboardMarkup
	Done     bool
}

// fingerprint identifies the visible content of a view. The watcher skips the
// Telegram edit when it does not change, which keeps the 2s refresh from
// hammering the API (and from tripping "message is not modified" errors).
func (v statusView) fingerprint() string {
	var sb strings.Builder
	sb.WriteString(v.Text)
	if v.Keyboard != nil {
		for _, row := range v.Keyboard.InlineKeyboard {
			sb.WriteString("\n")
			for _, button := range row {
				sb.WriteString("|")
				sb.WriteString(button.Text)
				if button.CallbackData != nil {
					sb.WriteString("#")
					sb.WriteString(*button.CallbackData)
				}
			}
		}
	}
	return sb.String()
}

type statusEntry struct {
	requestID string
	status    *DownloadStatus
}

// statusStore reads download progress written by the queue processor.
type statusStore struct {
	client *redis.Client
}

// activeStatuses returns the active downloads in a stable order. Request IDs
// whose hash is already gone are dropped from the set, so a missed terminal
// update cannot leave a ghost entry in the list forever.
func (s *statusStore) activeStatuses(ctx context.Context) ([]statusEntry, error) {
	requestIds, err := s.client.SMembers(ctx, KeyTorrentInProgressKeys).Result()
	if err != nil {
		return nil, err
	}
	if len(requestIds) == 0 {
		return nil, nil
	}

	// SMEMBERS has no ordering guarantee; sort so buttons keep their place
	// between refreshes instead of shuffling every 2 seconds.
	sort.Strings(requestIds)
	if len(requestIds) > maxStatusRows {
		requestIds = requestIds[:maxStatusRows]
	}

	pipe := s.client.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(requestIds))
	for i, requestId := range requestIds {
		cmds[i] = pipe.HGetAll(ctx, fmt.Sprintf(KeyTorrentInProgress, requestId))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}

	entries := make([]statusEntry, 0, len(requestIds))
	stale := make([]interface{}, 0)
	for i, requestId := range requestIds {
		res, err := cmds[i].Result()
		if err != nil {
			log.Printf("Failed to get progress updates (requestID: %s): %v", requestId, err)
			continue
		}

		if len(res) == 0 {
			stale = append(stale, requestId)
			continue
		}

		status := &DownloadStatus{}
		if err := status.FromRedisMap(res); err != nil {
			log.Printf("Failed to parse status (requestID: %s): %v", requestId, err)
			continue
		}

		entries = append(entries, statusEntry{requestID: requestId, status: status})
	}

	if len(stale) > 0 {
		if err := s.client.SRem(ctx, KeyTorrentInProgressKeys, stale...).Err(); err != nil {
			log.Printf("Failed to drop stale request IDs from active set: %v", err)
		}
	}

	return entries, nil
}

// status returns the download status, or ok=false when it is no longer tracked.
func (s *statusStore) status(ctx context.Context, requestID string) (*DownloadStatus, bool, error) {
	res, err := s.client.HGetAll(ctx, fmt.Sprintf(KeyTorrentInProgress, requestID)).Result()
	if err != nil {
		return nil, false, err
	}
	if len(res) == 0 {
		return nil, false, nil
	}

	status := &DownloadStatus{}
	if err := status.FromRedisMap(res); err != nil {
		return nil, false, err
	}

	return status, true, nil
}

func NewStatusChecker(bot *Bot) *StatusChecker {
	sc := &StatusChecker{
		bot:      bot,
		store:    &statusStore{client: bot.redisClient},
		watches:  make(map[int64]*statusWatch),
		interval: statusRefreshInterval,
		ttl:      statusWatchTTL,
	}

	sc.renderFn = sc.render
	sc.applyFn = sc.applyView
	return sc
}

func (sc *StatusChecker) CheckStatus(chatID int64, messageID int) {
	ctx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()

	view := sc.renderList(ctx)

	msg := tgbotapi.NewMessage(chatID, view.Text)
	if view.Keyboard != nil {
		msg.ReplyMarkup = *view.Keyboard
	}
	msg.ReplyToMessageID = messageID

	sent, err := sc.bot.api.Send(msg)
	if err != nil {
		log.Printf("Failed to send status message: %v", err)
		return
	}

	if view.Done {
		return
	}

	sc.startWatch(chatID, sent.MessageID, watchMode{}, view.fingerprint())
}

func (sc *StatusChecker) HandleCallback(callback *tgbotapi.CallbackQuery) {
	// Answer first so the button stops spinning regardless of what follows.
	if _, err := sc.bot.api.Request(tgbotapi.NewCallback(callback.ID, "")); err != nil {
		log.Printf("Failed to answer callback: %v", err)
	}

	if callback.Message == nil {
		return
	}

	chatID := callback.Message.Chat.ID
	messageID := callback.Message.MessageID

	switch {
	case callback.Data == callbackCloseStatus:
		sc.stopWatch(chatID)

		if _, err := sc.bot.api.Request(tgbotapi.NewDeleteMessage(chatID, messageID)); err != nil {
			log.Printf("Failed to delete status message: %v", err)
		}

		if callback.Message.ReplyToMessage != nil {
			if _, err := sc.bot.api.Request(tgbotapi.NewDeleteMessage(chatID, callback.Message.ReplyToMessage.MessageID)); err != nil {
				log.Printf("Failed to delete command message: %v", err)
			}
		}

	case callback.Data == callbackRefreshStatus:
		sc.showView(chatID, messageID, watchMode{})

	case strings.HasPrefix(callback.Data, callbackStatusPrefix):
		requestID := strings.TrimPrefix(callback.Data, callbackStatusPrefix)
		sc.showView(chatID, messageID, watchMode{detail: true, requestID: requestID})
	}
}

// showView renders the requested mode into an existing message and (re)arms the
// auto-refresh watch for it.
func (sc *StatusChecker) showView(chatID int64, messageID int, mode watchMode) {
	ctx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()

	view := sc.renderFn(ctx, mode)
	if err := sc.applyFn(chatID, messageID, view); err != nil {
		log.Printf("Failed to update status message: %v", err)
		return
	}

	if view.Done {
		sc.stopWatch(chatID)
		return
	}

	sc.armWatch(chatID, messageID, mode, view.fingerprint())
}

func (sc *StatusChecker) render(ctx context.Context, mode watchMode) statusView {
	if mode.detail {
		return sc.renderDetail(ctx, mode.requestID)
	}
	return sc.renderList(ctx)
}

func (sc *StatusChecker) renderList(ctx context.Context) statusView {
	entries, err := sc.store.activeStatuses(ctx)
	if err != nil {
		log.Printf("Failed to get progress updates: %v", err)
		return statusView{Text: statusErrorText, Done: true}
	}

	if len(entries) == 0 {
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh Status", callbackRefreshStatus),
				tgbotapi.NewInlineKeyboardButtonData("🗑️ Close", callbackCloseStatus),
			),
		)
		return statusView{Text: noDownloadsText, Keyboard: &keyboard, Done: true}
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(entries)+1)
	for _, entry := range entries {
		button := tgbotapi.NewInlineKeyboardButtonData(
			listButtonText(entry.status),
			callbackStatusPrefix+entry.requestID,
		)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(button))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh Status", callbackRefreshStatus),
		tgbotapi.NewInlineKeyboardButtonData("🗑️ Close", callbackCloseStatus),
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return statusView{Text: listHeader, Keyboard: &keyboard}
}

func (sc *StatusChecker) renderDetail(ctx context.Context, requestID string) statusView {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Back to List", callbackRefreshStatus),
			tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", callbackStatusPrefix+requestID),
		),
	)

	status, ok, err := sc.store.status(ctx, requestID)
	if err != nil {
		log.Printf("Failed to get progress updates (requestID: %s): %v", requestID, err)
		return statusView{Text: statusErrorText, Keyboard: &keyboard, Done: true}
	}

	// The queue processor deletes the hash once a download reaches a terminal
	// state, so a missing hash means "finished", not "broken".
	if !ok {
		return statusView{Text: inactiveText, Keyboard: &keyboard, Done: true}
	}

	etaText := ""
	if status.ETA > 0 {
		etaText = fmt.Sprintf("\n⏱️ ETA: %s", formatDuration(status.ETA))
	}

	text := fmt.Sprintf("📥 Download Details:\n\n📁 Name: %s\n%s \n📊 Progress: %s%s\n💬 Message: %s\n",
		status.Name,
		getStatusText(status.Status),
		createProgressBar(status.Progress),
		etaText,
		status.Message,
	)

	done := status.Status == coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_SUCCESS ||
		status.Status == coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_ERROR

	return statusView{Text: text, Keyboard: &keyboard, Done: done}
}

func (sc *StatusChecker) applyView(chatID int64, messageID int, view statusView) error {
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, view.Text)
	editMsg.ReplyMarkup = view.Keyboard

	_, err := sc.bot.api.Send(editMsg)
	return err
}

// isPermanentTelegramError reports whether retrying the edit is pointless — the
// message was deleted, the chat is gone, and so on.
func isPermanentTelegramError(err error) bool {
	var apiErr *tgbotapi.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.Code != 400 {
		return false
	}

	// Already avoided by the fingerprint check, but harmless if it slips through.
	return !strings.Contains(strings.ToLower(apiErr.Message), "message is not modified")
}

func listButtonText(status *DownloadStatus) string {
	progressBar := createProgressBar(status.Progress)
	etaText := ""
	if status.ETA > 0 {
		etaText = fmt.Sprintf("(ETA: %s)", formatShortDuration(status.ETA))
	}

	nameTextLength := buttonTextLength - len(progressBar) - len(etaText) - ellipsisLength
	nameText := status.Name
	// Truncate by runes: cutting mid-rune produces invalid UTF-8 that Telegram rejects.
	if runes := []rune(nameText); nameTextLength > 0 && len(runes) > nameTextLength {
		nameText = string(runes[:nameTextLength]) + ".."
	}

	return fmt.Sprintf("%s %s %s", nameText, progressBar, etaText)
}

func createProgressBar(progress float64) string {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}

	relativeProgress := progress / 100

	filled := int(relativeProgress * float64(progressBarLength))
	empty := progressBarLength - filled

	return fmt.Sprintf("[%s%s] %.1f%%",
		strings.Repeat("█", filled),
		strings.Repeat("-", empty),
		progress)
}

func getStatusText(status coordinatorpb.DownloadStatus) string {
	switch status {
	case coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_IN_PROGRESS:
		return "⏳"
	case coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_SUCCESS:
		return "✅"
	case coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_ERROR:
		return "❌"
	default:
		return "❓"
	}
}

func formatDuration(duration time.Duration) string {
	if duration.Hours() >= 1 {
		return fmt.Sprintf("%.1f hours", duration.Hours())
	}
	if duration.Minutes() >= 1 {
		return fmt.Sprintf("%.1f minutes", duration.Minutes())
	}
	return fmt.Sprintf("%d seconds", int(duration.Seconds()))
}

func formatShortDuration(duration time.Duration) string {
	if duration.Hours() >= 1 {
		return fmt.Sprintf("%.1fh", duration.Hours())
	}
	if duration.Minutes() >= 1 {
		return fmt.Sprintf("%.1fm", duration.Minutes())
	}
	return fmt.Sprintf("%ds", int(duration.Seconds()))
}
