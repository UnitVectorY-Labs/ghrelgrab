package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestFetchLatestVersionJSON(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    string
		wantErr string
	}{
		{name: "release with extra fields", body: `{"tag_name":"v1.2.3","name":"release"}`, want: "v1.2.3"},
		{name: "duplicate keys", body: `{"tag_name":"v1","tag_name":"v2"}`, wantErr: "parse response"},
		{name: "duplicate unknown keys", body: `{"tag_name":"v1","other":1,"other":2}`, wantErr: "parse response"},
		{name: "invalid UTF-8", body: "{\"tag_name\":\"v\xff\"}", wantErr: "parse response"},
		{name: "case sensitive field", body: `{"TAG_NAME":"v1"}`, wantErr: "no tag_name"},
		{name: "trailing JSON", body: `{"tag_name":"v1"} {}`, wantErr: "parse response"},
		{name: "missing tag", body: `{}`, wantErr: "no tag_name"},
	}

	originalTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			})
			got, err := fetchLatestVersion("owner/repo", "")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("fetchLatestVersion() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("fetchLatestVersion() = %q, %v; want %q, nil", got, err, tt.want)
			}
		})
	}
}
