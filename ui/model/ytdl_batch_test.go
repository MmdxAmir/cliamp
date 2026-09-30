package model

import (
	"testing"

	"github.com/bjarneo/cliamp/resolve"
)

func TestInitYTDLBatchTrigger(t *testing.T) {
	tests := []struct {
		name string
		urls []string
		want string
	}{
		{name: "music playlist", urls: []string{"https://music.youtube.com/playlist?list=PL1"}, want: "https://music.youtube.com/playlist?list=PL1"},
		{name: "music host with www and upper case", urls: []string{"https://WWW.Music.YouTube.com/watch?v=a&list=PL1"}, want: "https://WWW.Music.YouTube.com/watch?v=a&list=PL1"},
		{name: "music host with m prefix", urls: []string{"https://m.music.youtube.com/watch?v=a&list=PL1"}, want: "https://m.music.youtube.com/watch?v=a&list=PL1"},
		{name: "youtube radio mix", urls: []string{"https://www.youtube.com/watch?v=a&list=RDa"}, want: "https://www.youtube.com/watch?v=a&list=RDa"},
		{name: "youtube plain playlist", urls: []string{"https://www.youtube.com/playlist?list=PL1"}},
		{name: "music url without list", urls: []string{"https://music.youtube.com/watch?v=a"}},
		{name: "other host", urls: []string{"https://example.com/music.youtube.com?list=PL1"}},
		{name: "first match wins", urls: []string{"https://youtube.com/playlist?list=PL1", "https://music.youtube.com/playlist?list=PL2", "https://youtube.com/watch?list=RDx"}, want: "https://music.youtube.com/playlist?list=PL2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m Model
			cmd := m.initYTDLBatch(tt.urls)
			if (cmd != nil) != (tt.want != "") {
				t.Fatalf("cmd = %v, want trigger %v", cmd != nil, tt.want != "")
			}
			if m.ytdlBatch.url != tt.want {
				t.Fatalf("batch url = %q, want %q", m.ytdlBatch.url, tt.want)
			}
			if tt.want == "" {
				return
			}
			if !m.ytdlBatch.loading || m.ytdlBatch.offset != resolve.YTDLRadioInitialItems {
				t.Fatalf("batch state = %+v, want loading at offset %d", m.ytdlBatch, resolve.YTDLRadioInitialItems)
			}
		})
	}
}
