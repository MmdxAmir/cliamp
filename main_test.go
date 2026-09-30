package main

import (
	"slices"
	"strings"
	"testing"
)

// cliamp search and cliamp search-sc play the first match of the query. Any
// other arguments pass through as they are.
func TestSearchArgs(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    []string
		want    []string
		wantErr string
	}{
		{name: "no arguments"},
		{name: "files", args: []string{"a.mp3", "search"}, want: []string{"a.mp3", "search"}},
		{name: "youtube", args: []string{"search", "never", "gonna"}, want: []string{"ytsearch1:never gonna"}},
		{name: "soundcloud", args: []string{"search-sc", "lofi beats"}, want: []string{"scsearch1:lofi beats"}},
		{name: "no query", args: []string{"search"}, wantErr: "search requires a query string"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := searchArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("searchArgs(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}
