package coordinator

import (
	"context"
	"fmt"
	"log"

	common "github.com/aquare11e/media-downloader-bot/common/protogen/common"
	coordinatorpb "github.com/aquare11e/media-downloader-bot/common/protogen/coordinator"
	"github.com/aquare11e/media-downloader-bot/common/protogen/plex"
	"github.com/aquare11e/media-downloader-bot/common/protogen/transmission"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Service struct {
	coordinatorpb.UnimplementedCoordinatorServiceServer
	transmissionClient   transmission.TransmissionServiceClient
	plexClient           plex.PlexServiceClient
	redisClient          *redis.Client
	pbTypeToDownloadPath map[common.RequestType]string
	rutrackerClient      *RutrackerClient
}

func NewService(transmissionConn, plexConn *grpc.ClientConn, redisClient *redis.Client, pbTypeToDownloadPath map[common.RequestType]string) *Service {
	return &Service{
		transmissionClient:   transmission.NewTransmissionServiceClient(transmissionConn),
		plexClient:           plex.NewPlexServiceClient(plexConn),
		redisClient:          redisClient,
		pbTypeToDownloadPath: pbTypeToDownloadPath,
		rutrackerClient:      NewRutrackerClient(),
	}
}

func (s *Service) AddTorrentByMagnet(ctx context.Context, req *coordinatorpb.AddTorrentByMagnetRequest) (*coordinatorpb.DownloadResponse, error) {
	log.Printf("Adding torrent by magnet (requestID: %s, category: %s)", req.RequestId, req.Category)

	return s.executeWithLogging(ctx, req.RequestId, req.Category, func() (*transmission.AddTorrentResponse, error) {
		return s.transmissionClient.AddTorrentByMagnet(ctx, &transmission.AddTorrentByMagnetRequest{
			MagnetLink: req.MagnetLink,
			Filedir:    s.pbTypeToDownloadPath[req.Category],
			RequestId:  req.RequestId,
			Category:   req.Category.String(),
		})
	})
}

func (s *Service) AddTorrentByFile(ctx context.Context, req *coordinatorpb.AddTorrentByFileRequest) (*coordinatorpb.DownloadResponse, error) {
	log.Printf("Adding torrent by file (requestID: %s, category: %s)", req.RequestId, req.Category)

	return s.executeWithLogging(ctx, req.RequestId, req.Category, func() (*transmission.AddTorrentResponse, error) {
		return s.transmissionClient.AddTorrentByFile(ctx, &transmission.AddTorrentByFileRequest{
			Base64File: req.Base64File,
			Filedir:    s.pbTypeToDownloadPath[req.Category],
			RequestId:  req.RequestId,
			Category:   req.Category.String(),
		})
	})
}

func (s *Service) AddTorrentByRutrackerURL(ctx context.Context, req *coordinatorpb.AddTorrentByRutrackerURLRequest) (*coordinatorpb.DownloadResponse, error) {
	log.Printf("Adding torrent by Rutracker URL (requestID: %s, category: %s, url: %s)", req.RequestId, req.Category, req.RutrackerUrl)

	// Fetch magnet link from rutracker page
	magnetLink, err := s.rutrackerClient.GetMagnetLink(req.RutrackerUrl)
	if err != nil {
		log.Printf("Failed to get magnet link from rutracker (requestID: %s): %v", req.RequestId, err)
		return nil, status.Errorf(codes.Internal, "failed to get magnet link from rutracker: %v", err)
	}

	log.Printf("Extracted magnet link from rutracker (requestID: %s)", req.RequestId)

	return s.executeWithLogging(ctx, req.RequestId, req.Category, func() (*transmission.AddTorrentResponse, error) {
		return s.transmissionClient.AddTorrentByMagnet(ctx, &transmission.AddTorrentByMagnetRequest{
			MagnetLink: magnetLink,
			Filedir:    s.pbTypeToDownloadPath[req.Category],
			RequestId:  req.RequestId,
			Category:   req.Category.String(),
		})
	})
}

func (s *Service) executeWithLogging(
	ctx context.Context,
	requestID string,
	category common.RequestType,
	fn func() (*transmission.AddTorrentResponse, error),
) (*coordinatorpb.DownloadResponse, error) {
	response, err := fn()
	if err != nil {
		log.Printf("Error occurred (requestID: %s): %v", requestID, err)
		return nil, status.Errorf(codes.Internal, "failed to execute function: %v", err)
	}

	log.Printf("Torrent added (requestID: %s, torrentID: %d)", requestID, response.TorrentId)

	// Save to Redis
	torrentRecord := &TorrentRecord{
		TorrentID: response.TorrentId,
		Category:  category,
	}
	err = s.redisClient.HSet(ctx, fmt.Sprintf(KeyTorrentFormat, requestID), torrentRecord.ToRedisMap()).Err()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to save to Redis torrent record: %v", err)
	}

	err = s.redisClient.SAdd(ctx, KeyTorrentInProgress, requestID).Err()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to add to Redis requestID to in progress set: %v", err)
	}

	return &coordinatorpb.DownloadResponse{
		Name:      response.Name,
		RequestId: requestID,
		Progress:  0,
		Status:    coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_IN_PROGRESS,
		Message:   "Download started",
	}, nil
}
