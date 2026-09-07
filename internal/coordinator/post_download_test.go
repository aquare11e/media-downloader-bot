package coordinator

import (
	"context"
	"errors"
	"strings"
	"testing"

	common "github.com/aquare11e/media-downloader-bot/common/protogen/common"
	coordinatorpb "github.com/aquare11e/media-downloader-bot/common/protogen/coordinator"
	"github.com/aquare11e/media-downloader-bot/common/protogen/plex"
	"google.golang.org/grpc"
)

// fakePlexClient records the UpdateCategory calls made to the plex service.
type fakePlexClient struct {
	requests []*plex.UpdateCategoryRequest
	response *plex.UpdateCategoryResponse
	err      error
}

func (f *fakePlexClient) UpdateCategory(_ context.Context, in *plex.UpdateCategoryRequest, _ ...grpc.CallOption) (*plex.UpdateCategoryResponse, error) {
	f.requests = append(f.requests, in)
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

func newTestService(plexClient plex.PlexServiceClient) *Service {
	return &Service{plexClient: plexClient}
}

func TestPostDownloadActionRefreshesPlexForMediaCategories(t *testing.T) {
	mediaCategories := []common.RequestType{
		common.RequestType_FILMS,
		common.RequestType_SERIES,
		common.RequestType_CARTOONS,
		common.RequestType_CARTOONS_SERIES,
		common.RequestType_SHORTS,
	}

	for _, category := range mediaCategories {
		t.Run(category.String(), func(t *testing.T) {
			plexClient := &fakePlexClient{
				response: &plex.UpdateCategoryResponse{Result: plex.ResponseResult_RESPONSE_RESULT_SUCCESS},
			}
			service := newTestService(plexClient)

			status, message := service.postDownloadAction(category)(context.Background(), "request-1", category)

			if len(plexClient.requests) != 1 {
				t.Fatalf("expected 1 Plex refresh, got %d", len(plexClient.requests))
			}
			if plexClient.requests[0].Type != category {
				t.Errorf("Plex refresh category: got %s, want %s", plexClient.requests[0].Type, category)
			}
			if plexClient.requests[0].RequestId != "request-1" {
				t.Errorf("Plex refresh requestID: got %q, want %q", plexClient.requests[0].RequestId, "request-1")
			}
			if status != coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_SUCCESS {
				t.Errorf("status: got %s, want %s", status, coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_SUCCESS)
			}
			if !strings.Contains(message, "library refreshed") {
				t.Errorf("message %q does not mention the library refresh", message)
			}
		})
	}
}

func TestPostDownloadActionSkipsPlexForSwitch(t *testing.T) {
	plexClient := &fakePlexClient{
		response: &plex.UpdateCategoryResponse{Result: plex.ResponseResult_RESPONSE_RESULT_SUCCESS},
	}
	service := newTestService(plexClient)

	status, message := service.postDownloadAction(common.RequestType_SWITCH)(context.Background(), "request-2", common.RequestType_SWITCH)

	if len(plexClient.requests) != 0 {
		t.Fatalf("expected no Plex refresh for SWITCH, got %d calls", len(plexClient.requests))
	}
	if status != coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_SUCCESS {
		t.Errorf("status: got %s, want %s", status, coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_SUCCESS)
	}
	if !strings.Contains(message, "Switch library") {
		t.Errorf("message %q does not mention the Switch library", message)
	}
	if strings.Contains(strings.ToLower(message), "plex") {
		t.Errorf("message %q must not mention Plex", message)
	}
}

func TestPostDownloadActionReportsPlexFailure(t *testing.T) {
	plexClient := &fakePlexClient{err: errors.New("plex is down")}
	service := newTestService(plexClient)

	status, message := service.postDownloadAction(common.RequestType_FILMS)(context.Background(), "request-3", common.RequestType_FILMS)

	if status != coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_ERROR {
		t.Errorf("status: got %s, want %s", status, coordinatorpb.DownloadStatus_DOWNLOAD_STATUS_ERROR)
	}
	if !strings.Contains(message, "plex is down") {
		t.Errorf("message %q does not contain the Plex error", message)
	}
}
