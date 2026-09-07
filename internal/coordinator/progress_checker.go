package coordinator

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	common "github.com/aquare11e/media-downloader-bot/common/protogen/common"
	coordinatorpb "github.com/aquare11e/media-downloader-bot/common/protogen/coordinator"
	"github.com/aquare11e/media-downloader-bot/common/protogen/transmission"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (s *Service) StartProgressCheckerService(ctx context.Context) {
	log.Printf("Starting progress checker service (interval: %s)", s.checkInterval)

	ticker := time.NewTicker(s.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("Progress checker service stopped")
			return
		case <-ticker.C:
			s.checkProgress(ctx)
		}
	}
}

func (s *Service) checkProgress(ctx context.Context) {
	requestIDs, err := s.redisClient.SMembers(ctx, KeyTorrentInProgress).Result()
	if err != nil {
		log.Printf("failed to get torrent IDs: %v", err)
		return
	}

	if len(requestIDs) == 0 {
		return
	}

	for _, requestID := range requestIDs {
		statusResp, err := s.getTorrentStatus(ctx, requestID)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				log.Printf("torrent not found, stopping for that request (requestID: %s): %v", requestID, err)
				s.handleTorrentNotFound(ctx, requestID)
				continue
			}

			log.Printf("failed to get torrent status (requestID: %s): %v", requestID, err)
			continue
		}

		log.Printf("Torrent status (id: %s, name: %s): %s, progress: %.2f%%, eta: %d seconds", requestID, statusResp.Name, statusResp.Status, statusResp.Progress, statusResp.Eta)
		switch statusResp.Status {
		case transmission.TorrentStatus_STATUS_ERROR:
			log.Printf("torrent status is error, check transmission's download: %s", statusResp.Name)
			err := s.handleError(ctx, requestID, statusResp.Name, "❌ Download failed")
			if err != nil {
				log.Printf("failed to handle error: %v", err)
			}

		case transmission.TorrentStatus_STATUS_STOPPED:
			log.Printf("torrent status is stopped, check transmission's download: %s", statusResp.Name)
			err := s.handleError(ctx, requestID, statusResp.Name, "❌ Download stopped")
			if err != nil {
				log.Printf("failed to handle error: %v", err)
			}

		case transmission.TorrentStatus_STATUS_IN_PROGRESS:
			err := s.handleInProgress(ctx, requestID, statusResp.Name, statusResp.Progress, statusResp.Eta)
			if err != nil {
				log.Printf("failed to handle in progress: %v", err)
			}

		case transmission.TorrentStatus_STATUS_DONE:
			log.Printf("torrent status is done, running post-download action: %s", statusResp.Name)
			err := s.handleDone(ctx, requestID, statusResp.Name)
			if err != nil {
				log.Printf("failed to handle done: %v", err)
			}
		}
	}
}

func (s *Service) getTorrentStatus(ctx context.Context, requestID string) (*transmission.GetTorrentStatusResponse, error) {
	torrentID, err := s.redisClient.HGet(ctx, fmt.Sprintf(KeyTorrentFormat, requestID), "torrent_id").Result()
	if err != nil {
		log.Printf("failed to get torrent ID: %v", err)
		return nil, err
	}

	torrentIDInt, err := strconv.ParseInt(torrentID, 10, 64)
	if err != nil {
		log.Printf("failed to parse torrent ID: %v", err)
		return nil, err
	}

	// Get torrent status
	statusReq := &transmission.GetTorrentStatusRequest{
		TorrentId: torrentIDInt,
		RequestId: requestID,
	}

	statusResp, err := s.transmissionClient.GetTorrentStatus(ctx, statusReq)
	if err != nil {
		log.Printf("failed to get torrent status (id: %d): %v", torrentIDInt, err)
		return nil, err
	}

	return statusResp, nil
}

func (s *Service) handleTorrentNotFound(ctx context.Context, requestID string) {
	s.redisClient.SRem(ctx, KeyTorrentInProgress, requestID)
	s.redisClient.Del(ctx, fmt.Sprintf(KeyTorrentFormat, requestID))

	s.sendProgressToRedis(ctx, &coordinatorpb.DownloadResponse{
		RequestId: requestID,
		Status:    coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_ERROR,
		Message:   "❌ Download lost",
	})
}

func (s *Service) handleError(ctx context.Context, requestID string, name string, message string) error {
	s.redisClient.SRem(ctx, KeyTorrentInProgress, requestID)
	s.redisClient.Del(ctx, fmt.Sprintf(KeyTorrentFormat, requestID))

	progressUpdate := &coordinatorpb.DownloadResponse{
		RequestId: requestID,
		Name:      name,
		Status:    coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_ERROR,
		Message:   message,
	}

	if err := s.sendProgressToRedis(ctx, progressUpdate); err != nil {
		log.Printf("failed to send progress to Redis: %v", err)
		return err
	}

	return nil
}

func (s *Service) handleInProgress(ctx context.Context, requestID string, name string, progress float64, eta int32) error {
	progressUpdate := &coordinatorpb.DownloadResponse{
		RequestId: requestID,
		Name:      name,
		Progress:  progress,
	}

	if eta > 0 {
		progressUpdate.Eta = eta
	}

	return s.sendProgressToRedis(ctx, progressUpdate)
}

func (s *Service) handleDone(ctx context.Context, requestID string, name string) error {
	category, err := s.redisClient.HGet(ctx, fmt.Sprintf(KeyTorrentFormat, requestID), "category").Result()
	if err != nil {
		log.Printf("failed to get torrent category: %v", err)
		return err
	}

	categoryInt, err := strconv.ParseInt(category, 10, 32)
	if err != nil {
		log.Printf("failed to parse torrent category: %v", err)
		return err
	}

	// Run the follow-up action of that category (Plex refresh for media, nothing for SWITCH)
	requestType := common.RequestType(categoryInt)
	status, message := s.postDownloadAction(requestType)(ctx, requestID, requestType)

	progressUpdate := &coordinatorpb.DownloadResponse{
		RequestId: requestID,
		Name:      name,
		Status:    status,
		Message:   message,
		Progress:  100,
	}

	// Send final update to Redis
	if err := s.sendProgressToRedis(ctx, progressUpdate); err != nil {
		log.Printf("failed to send progress to Redis: %v", err)
		return err
	}

	// Clean up Redis
	err = s.redisClient.Del(ctx, fmt.Sprintf(KeyTorrentFormat, requestID)).Err()
	if err != nil {
		log.Printf("failed to delete torrent from Redis: %v", err)
		return err
	}

	err = s.redisClient.SRem(ctx, KeyTorrentInProgress, requestID).Err()
	if err != nil {
		log.Printf("failed to remove torrent from Redis: %v", err)
		return err
	}

	return nil
}

func (s *Service) sendProgressToRedis(ctx context.Context, progress *coordinatorpb.DownloadResponse) error {
	progressBytes, err := proto.Marshal(progress)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to marshal progress update: %v", err)
	}

	// Push to Redis queue
	err = s.redisClient.RPush(ctx, KeyDownloadProgress, progressBytes).Err()
	if err != nil {
		return status.Errorf(codes.Internal, "failed to push progress update to Redis: %v", err)
	}

	// Set expiration for the queue (e.g., 24 hours)
	err = s.redisClient.Expire(ctx, KeyDownloadProgress, 24*time.Hour).Err()
	if err != nil {
		return status.Errorf(codes.Internal, "failed to set queue expiration: %v", err)
	}

	return nil
}
