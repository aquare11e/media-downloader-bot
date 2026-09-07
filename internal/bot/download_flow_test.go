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
