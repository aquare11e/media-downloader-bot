# Coordinator Service

A gRPC service that orchestrates between Plex and Transmission services for media downloads.

## Overview

This service acts as a coordinator between Plex and Transmission services, managing the download process of media content. It supports adding torrents via magnet links or .torrent files and provides real-time streaming updates about the download progress.

## Configuration

The service requires the following environment variables to be set:

- `SERVICE_PORT`: The port number on which the gRPC server will listen.
- `TRANSMISSION_SERVICE_URL`: The URL of the Transmission service.
- `PLEX_SERVICE_URL`: The URL of the Plex service.
- `REDIS_URL`: The URL of the Redis server.
- `REDIS_PASSWORD`: The password for the Redis server (optional).
- `FILMS_DIR_PATH`: The directory path for downloaded films.
- `SERIES_DIR_PATH`: The directory path for downloaded series.
- `CARTOONS_DIR_PATH`: The directory path for downloaded cartoons.
- `CARTOONS_SERIES_DIR_PATH`: The directory path for downloaded cartoon series.
- `SHORTS_DIR_PATH`: The directory path for downloaded shorts.
- `SWITCH_DIR_PATH`: The directory path for downloaded Nintendo Switch games (served by Ownfoil).

## Building and Running

1. Set up environment variables:
   ```bash
   export SERVICE_PORT=50053
   export TRANSMISSION_SERVICE_URL=transmission-service-url
   export PLEX_SERVICE_URL=plex-service-url
   export REDIS_URL=your-redis-url
   export REDIS_PASSWORD=your-redis-password # optional
   export FILMS_DIR_PATH=/path/to/films
   export SERIES_DIR_PATH=/path/to/series
   export CARTOONS_DIR_PATH=/path/to/cartoons
   export CARTOONS_SERIES_DIR_PATH=/path/to/cartoon_series
   export SHORTS_DIR_PATH=/path/to/shorts
   export SWITCH_DIR_PATH=/path/to/switch/library
   ```

2. Build and run the service:
   ```bash
   go build -o coordinator-service
   ./coordinator-service
   ```

## API Documentation

### AddTorrentByMagnet

Adds a torrent using a magnet link and streams download progress updates.

#### Request

```protobuf
message AddTorrentByMagnetRequest {
  string magnet_link = 1;
  common.RequestType category = 2;
}
```

#### Response

```protobuf
message DownloadResponse {
  string request_id = 1;
  DownloadStatus status = 2;
  string message = 3;
  double progress = 4;
  int32 eta = 5;
}

enum DownloadStatus {
  DOWNLOAD_STATUS_UNSPECIFIED = 0;
  DOWNLOAD_STATUS_IN_PROGRESS = 1;
  DOWNLOAD_STATUS_SUCCESS = 2;
  DOWNLOAD_STATUS_ERROR = 3;
}
```

### AddTorrentByFile

Adds a torrent using a base64 encoded .torrent file and streams download progress updates.

#### Request

```protobuf
message AddTorrentByFileRequest {
  string base64_file = 1;
  common.RequestType category = 2;
}
```

#### Response

```protobuf
message DownloadResponse {
  string request_id = 1;
  DownloadStatus status = 2;
  string message = 3;
  double progress = 4;
  int32 eta = 5;
}
```

## Testing with gRPCurl

You can use `grpcurl` to test the service:

```bash
# Add torrent by magnet link
grpcurl -plaintext -d '{"magnet_link": "magnet:?xt=urn:btih:...", "category": 0}' localhost:50053 coordinator.CoordinatorService/AddTorrentByMagnet

# Add torrent by file
grpcurl -plaintext -d '{"base64_file": "base64_encoded_torrent_file", "category": 0}' localhost:50053 coordinator.CoordinatorService/AddTorrentByFile
```

Where `category` values are:
- `1` for FILMS
- `2` for SERIES
- `3` for CARTOONS
- `4` for CARTOONS_SERIES
- `5` for SHORTS
- `6` for SWITCH

## Post-download actions

Once transmission reports a download as done, the coordinator runs the post-download
action of that category (see `internal/coordinator/post_download.go`):

- media categories (FILMS, SERIES, CARTOONS, CARTOONS_SERIES, SHORTS) → Plex library refresh
- `SWITCH` → no action; the files sit in `SWITCH_DIR_PATH`, which Ownfoil indexes itself

## Security Considerations

- Consider using environment variables or a secure configuration management system.
- The service should be run in a secure environment with appropriate network access controls.