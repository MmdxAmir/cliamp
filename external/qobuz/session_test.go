package qobuz

import (
	"net/url"
	"testing"
)

func TestParseQueryParams(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  oauthResult
	}{
		{name: "empty", query: "", want: oauthResult{}},
		{name: "user_auth_token", query: "user_auth_token=uat&user_id=7", want: oauthResult{Token: "uat", UserID: "7"}},
		{name: "token", query: "token=tok", want: oauthResult{Token: "tok"}},
		{name: "user_auth_token wins over token", query: "token=tok&user_auth_token=uat", want: oauthResult{Token: "uat"}},
		{name: "code_autorisation", query: "code_autorisation=fr", want: oauthResult{Code: "fr"}},
		{name: "code", query: "code=en", want: oauthResult{Code: "en"}},
		{name: "code_autorisation wins over code", query: "code=en&code_autorisation=fr", want: oauthResult{Code: "fr"}},
		{name: "token and code are both kept", query: "token=tok&code=en&user_id=7", want: oauthResult{Token: "tok", UserID: "7", Code: "en"}},
		{name: "empty values are ignored", query: "user_auth_token=&token=tok&code_autorisation=&code=en", want: oauthResult{Token: "tok", Code: "en"}},
		{name: "unrelated params", query: "state=x&foo=bar", want: oauthResult{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := url.ParseQuery(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			if got := parseQueryParams(q); got != tt.want {
				t.Errorf("parseQueryParams(%q) = %+v, want %+v", tt.query, got, tt.want)
			}
		})
	}
}
