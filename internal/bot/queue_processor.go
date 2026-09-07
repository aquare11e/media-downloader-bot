package bot

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	coordinatorpb "github.com/aquare11e/media-downloader-bot/common/protogen/coordinator"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	// queueBlockTimeout is how long BLPOP waits for a progress update before
	// looping, so Stop() is honoured promptly.
	queueBlockTimeout = 5 * time.Second
	// queueErrorBackoff avoids a hot loop when Redis is unreachable.
	queueErrorBackoff = 1 * time.Second
	// statusTTL keeps an orphaned download from lingering in the status list
	// forever if its terminal update is ever missed.
	statusTTL = 24 * time.Hour
)

type QueueProcessor struct {
	bot       *Bot
	stopChan  chan struct{}
	isRunning bool
}

func NewQueueProcessor(bot *Bot) *QueueProcessor {
	return &QueueProcessor{
		bot:      bot,
		stopChan: make(chan struct{}),
	}
}

func (qp *QueueProcessor) Start() {
	if qp.isRunning {
		return
	}

	qp.isRunning = true
	go qp.processQueue()
}

func (qp *QueueProcessor) Stop() {
	if !qp.isRunning {
		return
	}

	close(qp.stopChan)
	qp.isRunning = false
}

// processQueue drains progress updates as they arrive. BLPOP instead of polling
// keeps the status shown to the user as fresh as the coordinator makes it.
func (qp *QueueProcessor) processQueue() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-qp.stopChan
		cancel()
	}()

	for {
		select {
		case <-qp.stopChan:
			return
		default:
		}

		res, err := qp.bot.redisClient.BLPop(ctx, queueBlockTimeout, KeyDownloadProgressQueue).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				// No update within the block timeout.
				continue
			}
			if ctx.Err() != nil {
				return
			}

			log.Printf("Failed to get message from queue: %v", err)
			time.Sleep(queueErrorBackoff)
			continue
		}

		// BLPOP returns [key, value].
		if len(res) != 2 {
			log.Printf("Unexpected BLPOP result length: %d", len(res))
			continue
		}

		qp.processMessage(ctx, res[1])
	}
}

func (qp *QueueProcessor) processMessage(ctx context.Context, message string) {
	encodedMessage := base64.StdEncoding.EncodeToString([]byte(message))
	log.Printf("Message from queue: %s", encodedMessage)

	var downloadResp coordinatorpb.DownloadResponse
	if err := proto.Unmarshal([]byte(message), &downloadResp); err != nil {
		log.Printf("Failed to unmarshal message: %v", err)
		return
	}

	// Convert to DownloadStatus
	status := &DownloadStatus{
		Name:     downloadResp.Name,
		Status:   downloadResp.Status,
		Message:  downloadResp.Message,
		ETA:      time.Duration(downloadResp.Eta) * time.Second,
		Progress: downloadResp.Progress,
	}

	log.Printf("Download status: %s", status.ToLogString())

	// Update status in Redis
	key := fmt.Sprintf(KeyTorrentInProgress, downloadResp.RequestId)
	pipe := qp.bot.redisClient.Pipeline()
	pipe.HSet(ctx, key, status.ToRedisMap())
	pipe.Expire(ctx, key, statusTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("Failed to update status in Redis: %v", err)
		return
	}

	// Add to set of active downloads if not already present
	if err := qp.bot.redisClient.SAdd(ctx, KeyTorrentInProgressKeys, downloadResp.RequestId).Err(); err != nil {
		log.Printf("Failed to add to active downloads set: %v", err)
		return
	}

	// If download is completed or failed, remove from active downloads
	if status.Status != coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_SUCCESS && status.Status != coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_ERROR {
		return
	}

	if err := qp.bot.redisClient.SRem(ctx, KeyTorrentInProgressKeys, downloadResp.RequestId).Err(); err != nil {
		log.Printf("Failed to remove from active downloads set: %v", err)
		return
	}

	if err := qp.bot.redisClient.Del(ctx, key).Err(); err != nil {
		log.Printf("Failed to remove status from Redis: %v", err)
	}

	ownerResp := qp.bot.redisClient.GetDel(ctx, fmt.Sprintf(KeyTorrentDownloadOwner, downloadResp.RequestId))
	if ownerResp.Err() != nil {
		log.Printf("Failed to get download owner: %v", ownerResp.Err())
		return
	}

	ownerID := ownerResp.Val()
	if ownerID == "" {
		log.Printf("Download owner not found for request ID: %s", downloadResp.RequestId)
		return
	}

	ownerIDInt, err := strconv.ParseInt(ownerID, 10, 64)
	if err != nil {
		log.Printf("Failed to convert ownerID to int64: %v", err)
		return
	}

	msg := tgbotapi.NewMessage(ownerIDInt, "🎉 Your download is complete!\n📁 File: "+status.Name+"\n📝 Message: "+status.Message+"\n\nIf you encountered any issues, feel free to reach out for help!")
	qp.bot.api.Send(msg)
}
