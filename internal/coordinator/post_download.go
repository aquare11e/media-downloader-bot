package coordinator

import (
	"context"
	"fmt"
	"log"

	common "github.com/aquare11e/media-downloader-bot/common/protogen/common"
	coordinatorpb "github.com/aquare11e/media-downloader-bot/common/protogen/coordinator"
	"github.com/aquare11e/media-downloader-bot/common/protogen/plex"
)

// postDownloadAction is the follow-up work executed once a download is completed.
// It reports the final status and the message shown to the user.
type postDownloadAction func(ctx context.Context, requestID string, category common.RequestType) (coordinatorpb.DownloadStatus, string)

// postDownloadAction resolves the action to run for a completed category.
func (s *Service) postDownloadAction(category common.RequestType) postDownloadAction {
	switch category {
	case common.RequestType_SWITCH:
		return switchLibraryCompleted
	default:
		return s.refreshPlexLibrary
	}
}

// refreshPlexLibrary triggers a Plex library scan for media categories.
func (s *Service) refreshPlexLibrary(ctx context.Context, requestID string, category common.RequestType) (coordinatorpb.DownloadStatus, string) {
	plexResp, err := s.plexClient.UpdateCategory(ctx, &plex.UpdateCategoryRequest{
		RequestId: requestID,
		Type:      category,
	})

	switch {
	case err != nil:
		return coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_ERROR, fmt.Sprintf("Failed to refresh Plex library: %v", err)
	case plexResp.Result == plex.ResponseResult_RESPONSE_RESULT_SUCCESS:
		return coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_SUCCESS, "✅ Download completed and library refreshed"
	default:
		return coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_ERROR, plexResp.Message
	}
}

// switchLibraryCompleted completes a SWITCH download without any explicit action:
// the files land in SWITCH_DIR_PATH, which Ownfoil indexes on its own through the
// shared filesystem.
func switchLibraryCompleted(_ context.Context, requestID string, category common.RequestType) (coordinatorpb.DownloadStatus, string) {
	log.Printf("No post-download action for category %s (requestID: %s)", category, requestID)
	return coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_SUCCESS, "✅ Download completed\n\nAdded to Switch library."
}
