package main

import "testing"

func TestNewClientBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                                   DefaultBaseURL,
		"https://ld.example.com/api/public/": "https://ld.example.com/api/public",
	} {
		if got := NewClient(in, "k").baseURL; got != want {
			t.Errorf("NewClient(%q).baseURL = %q, want %q", in, got, want)
		}
	}
}
