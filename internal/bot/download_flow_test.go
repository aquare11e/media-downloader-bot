package bot

import (
	"testing"

	common "github.com/aquare11e/media-downloader-bot/common/protogen/common"
)

func TestCategoryFromText(t *testing.T) {
	tests := []struct {
		text string
		want common.RequestType
	}{
		{filmsCategory, common.RequestType_FILMS},
		{seriesCategory, common.RequestType_SERIES},
		{cartoonsCategory, common.RequestType_CARTOONS},
		{cartoonsSeriesCategory, common.RequestType_CARTOONS_SERIES},
		{cartoonsShortsCategory, common.RequestType_SHORTS},
		{switchCategory, common.RequestType_SWITCH},
		{"🎮 Switch", common.RequestType_SWITCH},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got, ok := categoryFromText(tt.text)
			if !ok {
				t.Fatalf("category %q was not recognized", tt.text)
			}
			if got != tt.want {
				t.Errorf("category %q: got %s, want %s", tt.text, got, tt.want)
			}
		})
	}
}

func TestCategoryFromTextUnknown(t *testing.T) {
	for _, text := range []string{"", "Switch", "🎮", "/download"} {
		if got, ok := categoryFromText(text); ok {
			t.Errorf("category %q should not be recognized, got %s", text, got)
		}
	}
}

func TestClassifyText(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		want   LinkType
		wantOk bool
	}{
		{"magnet link", "magnet:?xt=urn:btih:abc123&dn=Some.Movie", LinkTypeMagnet, true},
		{"rutracker viewtopic", "https://rutracker.org/forum/viewtopic.php?t=1234567", LinkTypeRutracker, true},
		{"rutracker short topic", "https://rutracker.org/forum/t/1234567", LinkTypeRutracker, true},
		{"plain text", "hello there", 0, false},
		{"empty", "", 0, false},
		{"http link that is not rutracker", "https://example.com/forum/t/1234567", 0, false},
		{"rutracker home page", "https://rutracker.org/forum/index.php", 0, false},
		{"magnet without btih", "magnet:?xt=urn:sha1:abc123", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := classifyText(tt.text)
			if ok != tt.wantOk {
				t.Fatalf("classifyText(%q) recognized = %v, want %v", tt.text, ok, tt.wantOk)
			}
			if ok && got != tt.want {
				t.Errorf("classifyText(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
