package syncer

import (
	"testing"

	"telegram-online-player/internal/catalog"
)

func TestClassifyPlayMode(t *testing.T) {
	cases := []struct {
		filename, mime      string
		wantContainer, want string
	}{
		{"a-2024-01-01 00:00:00.mp4", "", "mp4", catalog.PlayPassthrough},
		{"a.MOV", "", "mp4", catalog.PlayPassthrough},
		{"a.flv", "", "flv", catalog.PlayRemux},
		{"a.ts", "", "ts", catalog.PlayRemux},
		{"a.m2ts", "", "ts", catalog.PlayRemux},
		{"a.mkv", "", "mkv", catalog.PlayTranscode},
		{"noext", "video/mp4", "mp4", catalog.PlayPassthrough},
		{"noext", "video/x-flv", "flv", catalog.PlayRemux},
		{"noext", "", "unknown", catalog.PlayTranscode},
	}
	for _, c := range cases {
		gotC, gotM := ClassifyPlayMode(c.filename, c.mime)
		if gotC != c.wantContainer || gotM != c.want {
			t.Errorf("Classify(%q,%q)=(%q,%q), want (%q,%q)",
				c.filename, c.mime, gotC, gotM, c.wantContainer, c.want)
		}
	}
}
