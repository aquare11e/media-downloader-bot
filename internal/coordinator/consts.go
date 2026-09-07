package coordinator

import "time"

const (
	// KeyTorrentFormat is the format for Redis keys storing torrent information
	KeyTorrentFormat = "coordinator:torrent:%s"
	// KeyTorrentInProgress is the key for Redis storing torrent in progress
	KeyTorrentInProgress = "coordinator:torrent:in_progress"
	// KeyDownloadProgress is the key for Redis storing download progress
	KeyDownloadProgress = "coordinator-bot:download:progress"

	// StaleThreshold is the time after which a record is considered stale
	StaleThreshold = 10 * time.Minute
	// CheckInterval is how often the recovery service checks for stale records
	CheckInterval = 1 * time.Minute
)
