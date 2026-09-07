package main

import (
	"testing"

	"github.com/aquare11e/media-downloader-bot/common/protogen/common"
)

func TestDownloadPathsFromEnv(t *testing.T) {
	envs := map[string]string{
		"FILMS_DIR_PATH":           "/media/films",
		"SERIES_DIR_PATH":          "/media/series",
		"CARTOONS_DIR_PATH":        "/media/cartoons",
		"CARTOONS_SERIES_DIR_PATH": "/media/cartoons_series",
		"SHORTS_DIR_PATH":          "/media/shorts",
		"SWITCH_DIR_PATH":          "/media/switch",
	}
	for key, value := range envs {
		t.Setenv(key, value)
	}

	paths := downloadPathsFromEnv()

	expected := map[common.RequestType]string{
		common.RequestType_FILMS:           "/media/films",
		common.RequestType_SERIES:          "/media/series",
		common.RequestType_CARTOONS:        "/media/cartoons",
		common.RequestType_CARTOONS_SERIES: "/media/cartoons_series",
		common.RequestType_SHORTS:          "/media/shorts",
		common.RequestType_SWITCH:          "/media/switch",
	}

	if len(paths) != len(expected) {
		t.Fatalf("expected %d download paths, got %d", len(expected), len(paths))
	}

	for requestType, want := range expected {
		if got := paths[requestType]; got != want {
			t.Errorf("download path for %s: got %q, want %q", requestType, got, want)
		}
	}
}
