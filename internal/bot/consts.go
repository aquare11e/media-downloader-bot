package bot

const (
	filmsCategory          = "🎬 Films"
	seriesCategory         = "📺 Series"
	cartoonsCategory       = "🎨 Cartoons"
	cartoonsSeriesCategory = "🕸️ Cartoon Series"
	cartoonsShortsCategory = "🩳 Cartoon Shorts"
	switchCategory         = "🎮 Switch"

	// Redis related
	KeyTorrentInProgress     = "bot:torrents:%s"
	KeyTorrentInProgressKeys = "bot:torrents:keys"
	KeyTorrentDownloadOwner  = "bot:torrents:owner:%s"
	KeyDownloadProgressQueue = "coordinator-bot:download:progress"
)
