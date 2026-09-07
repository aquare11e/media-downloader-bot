package bot

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	common "github.com/aquare11e/media-downloader-bot/common/protogen/common"
	coordinatorpb "github.com/aquare11e/media-downloader-bot/common/protogen/coordinator"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
)

type Step int

const (
	StepWaitingForLink Step = iota + 1
	StepWaitingForCategory
	StepDownloading
)

type LinkType int

const (
	LinkTypeMagnet LinkType = iota + 1
	LinkTypeTorrentFile
	LinkTypeRutracker
)

type downloadState struct {
	step     Step
	link     string
	linkType LinkType
	category common.RequestType
}

type DownloadFlow struct {
	bot    *Bot
	States map[int64]*downloadState
}

func NewDownloadFlow(bot *Bot) *DownloadFlow {
	return &DownloadFlow{
		bot:    bot,
		States: make(map[int64]*downloadState),
	}
}

func (df *DownloadFlow) Start(chatID int64) {
	df.States[chatID] = &downloadState{
		step: StepWaitingForLink,
	}

	response := tgbotapi.NewMessage(chatID, "✨ Awesome! Please send me a magnet link, torrent file, or Rutracker URL to begin your download journey!")
	df.bot.api.Send(response)
}

func (df *DownloadFlow) HandleMessage(msg *tgbotapi.Message) {
	response := tgbotapi.NewMessage(msg.Chat.ID, "")

	// The category keyboard is the only step that needs the previous message for
	// context. Everything else - including a link dropped without /download - is
	// the start of a new download.
	if state, exists := df.States[msg.Chat.ID]; exists && state.step == StepWaitingForCategory {
		df.handleWaitingForCategoryStep(msg, state, response)
		return
	}

	df.startFromMessage(msg, response)
}

// startFromMessage begins a download from whatever the user sent, whether or not
// they went through /download first.
func (df *DownloadFlow) startFromMessage(msg *tgbotapi.Message, response tgbotapi.MessageConfig) {
	link, linkType, ok, err := df.detectLink(msg)
	if err != nil {
		response.Text = err.Error()
		delete(df.States, msg.Chat.ID)
		df.bot.api.Send(response)
		return
	}

	if !ok {
		response.Text = "📥 Send me a magnet link, a .torrent file, or a Rutracker URL and I'll start the download right away. Use /help to see all commands."
		delete(df.States, msg.Chat.ID)
		df.bot.api.Send(response)
		return
	}

	df.States[msg.Chat.ID] = &downloadState{
		step:     StepWaitingForCategory,
		link:     link,
		linkType: linkType,
	}
	df.sendCategoryButtons(msg.Chat.ID)
}

// detectLink resolves a message into a download link and its type. ok is false
// when the message is not a download request at all; err carries a
// user-facing message when a torrent file was sent but could not be read.
func (df *DownloadFlow) detectLink(msg *tgbotapi.Message) (string, LinkType, bool, error) {
	if linkType, ok := classifyText(msg.Text); ok {
		return msg.Text, linkType, true, nil
	}

	if msg.Document == nil || !strings.HasSuffix(msg.Document.FileName, ".torrent") {
		return "", 0, false, nil
	}

	file, err := df.bot.api.GetFile(tgbotapi.FileConfig{FileID: msg.Document.FileID})
	if err != nil {
		log.Printf("Failed to get file info: %v", err)
		return "", 0, false, errors.New("❌ Oops! I couldn't process your torrent file. Please try again!")
	}

	// Download torrent file from Telegram
	fileURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", df.bot.api.Token, file.FilePath)
	resp, err := http.Get(fileURL)
	if err != nil {
		log.Printf("Failed to download torrent file: %v", err)
		return "", 0, false, errors.New("❌ Oops! I couldn't download your torrent file. Please try again!")
	}
	defer resp.Body.Close()

	fileBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read torrent file: %v", err)
		return "", 0, false, errors.New("❌ Oops! I couldn't read your torrent file. Please try again!")
	}

	return base64.StdEncoding.EncodeToString(fileBytes), LinkTypeTorrentFile, true, nil
}

// classifyText reports which kind of download link the text is, if any.
func classifyText(text string) (LinkType, bool) {
	switch {
	case strings.HasPrefix(text, "magnet:?xt=urn:btih:"):
		return LinkTypeMagnet, true
	case isRutrackerURL(text):
		return LinkTypeRutracker, true
	default:
		return 0, false
	}
}

// isRutrackerURL checks if the URL is a rutracker topic URL
func isRutrackerURL(urlStr string) bool {
	return strings.Contains(urlStr, "rutracker.org") &&
		(strings.Contains(urlStr, "viewtopic.php") || strings.Contains(urlStr, "/forum/t/"))
}

// categoryFromText maps a category button label to its request type.
func categoryFromText(text string) (common.RequestType, bool) {
	switch text {
	case filmsCategory:
		return common.RequestType_FILMS, true
	case seriesCategory:
		return common.RequestType_SERIES, true
	case cartoonsCategory:
		return common.RequestType_CARTOONS, true
	case cartoonsSeriesCategory:
		return common.RequestType_CARTOONS_SERIES, true
	case cartoonsShortsCategory:
		return common.RequestType_SHORTS, true
	case switchCategory:
		return common.RequestType_SWITCH, true
	default:
		return common.RequestType_REQUEST_TYPE_UNSPECIFIED, false
	}
}

func (df *DownloadFlow) handleWaitingForCategoryStep(msg *tgbotapi.Message, state *downloadState, response tgbotapi.MessageConfig) {
	category, ok := categoryFromText(msg.Text)
	if !ok {
		// A link sent while the keyboard is up replaces the pending one rather
		// than being rejected as an invalid category.
		if _, isLink := classifyText(msg.Text); isLink || msg.Document != nil {
			df.startFromMessage(msg, response)
			return
		}

		response.Text = "❌ Please select a valid category from the options below"
		df.bot.api.Send(response)
		return
	}

	state.category = category
	state.step = StepDownloading

	// Start the download based on link type
	requestID := uuid.New().String()
	var resp *coordinatorpb.DownloadResponse
	var err error

	switch state.linkType {
	case LinkTypeMagnet:
		resp, err = df.bot.coordClient.AddTorrentByMagnet(context.Background(), &coordinatorpb.AddTorrentByMagnetRequest{
			RequestId:  requestID,
			MagnetLink: state.link,
			Category:   state.category,
		})
	case LinkTypeTorrentFile:
		resp, err = df.bot.coordClient.AddTorrentByFile(context.Background(), &coordinatorpb.AddTorrentByFileRequest{
			RequestId:  requestID,
			Base64File: state.link,
			Category:   state.category,
		})
	case LinkTypeRutracker:
		resp, err = df.bot.coordClient.AddTorrentByRutrackerURL(context.Background(), &coordinatorpb.AddTorrentByRutrackerURLRequest{
			RequestId:    requestID,
			RutrackerUrl: state.link,
			Category:     state.category,
		})
	}

	if err != nil {
		log.Printf("Failed to start download: %v", err)
		response.Text = "❌ Oops! I couldn't start the download. Please try again later!"
		delete(df.States, msg.Chat.ID)
		df.bot.api.Send(response)
		return
	}

	status := &DownloadStatus{
		Name:     resp.Name,
		Status:   resp.Status,
		Message:  resp.Message,
		ETA:      time.Duration(resp.Eta) * time.Second,
		Progress: resp.Progress,
	}

	statusKey := fmt.Sprintf(KeyTorrentInProgress, resp.RequestId)
	err1 := df.bot.redisClient.HSet(context.Background(), statusKey, status.ToRedisMap()).Err()
	df.bot.redisClient.Expire(context.Background(), statusKey, statusTTL)
	err2 := df.bot.redisClient.SAdd(context.Background(), KeyTorrentInProgressKeys, resp.RequestId).Err()
	err3 := df.bot.redisClient.Set(context.Background(), fmt.Sprintf(KeyTorrentDownloadOwner, resp.RequestId), msg.Chat.ID, 24*time.Hour).Err()
	if err1 != nil || err2 != nil || err3 != nil {
		log.Printf("Failed to set status in Redis: \ndetails: %v, \nkeys: %v, \nowner: %v", err1, err2, err3)
		response.Text = "⚠️ Download started, but I couldn't save the status locally. You can check the status using /status command"
		delete(df.States, msg.Chat.ID)
		df.bot.api.Send(response)
		return
	}

	response.Text = "✅ Download started!\n📁 Torrent name: " + resp.Name
	delete(df.States, msg.Chat.ID)

	// Remove the keyboard
	response.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
	df.bot.api.Send(response)
}

func (df *DownloadFlow) sendCategoryButtons(chatID int64) {
	keyboard := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(filmsCategory),
			tgbotapi.NewKeyboardButton(seriesCategory),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(cartoonsCategory),
			tgbotapi.NewKeyboardButton(cartoonsSeriesCategory),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(cartoonsShortsCategory),
			tgbotapi.NewKeyboardButton(switchCategory),
		),
	)

	msg := tgbotapi.NewMessage(chatID, "🎬 Please select a category for your content:")
	msg.ReplyMarkup = keyboard
	df.bot.api.Send(msg)
}
