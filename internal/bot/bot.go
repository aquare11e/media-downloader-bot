package bot

import (
	"log"

	coordinator "github.com/aquare11e/media-downloader-bot/common/protogen/coordinator"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/redis/go-redis/v9"
)

type Bot struct {
	api            *tgbotapi.BotAPI
	allowedUserIds map[int64]bool
	coordClient    coordinator.CoordinatorServiceClient
	redisClient    *redis.Client
	downloadFlow   *DownloadFlow
	statusChecker  *StatusChecker
	queueProcessor *QueueProcessor
}

func NewBot(
	token string,
	allowedUserIdsList []int64,
	coordClient coordinator.CoordinatorServiceClient,
	redisClient *redis.Client,
) (*Bot, error) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}

	allowedUserIds := make(map[int64]bool)
	for _, userId := range allowedUserIdsList {
		allowedUserIds[userId] = true
	}

	b := &Bot{
		api:            bot,
		allowedUserIds: allowedUserIds,
		coordClient:    coordClient,
		redisClient:    redisClient,
	}

	b.downloadFlow = NewDownloadFlow(b)
	b.statusChecker = NewStatusChecker(b)
	b.queueProcessor = NewQueueProcessor(b)
	return b, nil
}

func (b *Bot) Start() {
	log.Printf("Bot started. Authorized on account %s", b.api.Self.UserName)

	// Start the queue processor
	b.queueProcessor.Start()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil {
			log.Printf("[%s, %d] %s", update.Message.From.UserName, update.Message.Chat.ID, update.Message.Text)

			if !b.allowedUserIds[update.Message.From.ID] {
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Sorry, you are not authorized to use this bot.")
				b.api.Send(msg)
				continue
			}

			if update.Message.IsCommand() {
				b.handleCommand(update.Message)
			} else {
				b.downloadFlow.HandleMessage(update.Message)
			}
		} else if update.CallbackQuery != nil {
			if !b.allowedUserIds[update.CallbackQuery.From.ID] {
				callback := tgbotapi.NewCallback(update.CallbackQuery.ID, "Sorry, you are not authorized to use this bot.")
				b.api.Send(callback)
				continue
			}

			b.statusChecker.HandleCallback(update.CallbackQuery)
		}
	}

	// Stop the background workers when the bot stops
	b.queueProcessor.Stop()
	b.statusChecker.StopAll()
}

func (b *Bot) handleCommand(msg *tgbotapi.Message) {
	response := b.prehandleMessage(msg)

	switch msg.Command() {
	case "start":
		response.Text = "🌟 Wow! Welcome to the Torrent Downloader Bot! I can help you download torrents effortlessly.\nJust send /help to discover all the amazing commands available!"
	case "help":
		response.Text = "🌟 Welcome to the Torrent Downloader Bot! Here are the magical commands you can use:\n/start - Kickstart your journey with the bot\n/download - Let’s dive into the world of torrents and download your favorites!\n/status - Keep track of your ongoing downloads and their progress\n/help - Need assistance? Just ask and I’ll guide you!"
	case "download":
		b.downloadFlow.Start(msg.Chat.ID)
	case "status":
		b.statusChecker.CheckStatus(msg.Chat.ID, msg.MessageID)
	default:
		response.Text = "I don't know that command"
	}

	b.api.Send(response)
}

func (b *Bot) prehandleMessage(msg *tgbotapi.Message) tgbotapi.MessageConfig {
	response := tgbotapi.NewMessage(msg.Chat.ID, "")
	response.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)

	return response
}
