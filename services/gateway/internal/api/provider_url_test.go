package api

import "testing"

// Both conventions, one endpoint.
//
// The field has always taken a base without `/v1`; every provider's own
// documentation gives one with it, and this project's speech field expects it
// that way. Somebody will paste one into the other, and `/v1/v1/models` is a
// 404 that explains nothing.
func TestProviderURLAcceptsBothConventions(t *testing.T) {
	for _, in := range []string{
		"https://api.example.com",
		"https://api.example.com/",
		"https://api.example.com/v1",
		"https://api.example.com/v1/",
	} {
		if got := providerURL(in, "models"); got != "https://api.example.com/v1/models" {
			t.Errorf("providerURL(%q) = %q", in, got)
		}
	}
}

func TestProviderURLIsEmptyWhenNothingIsSet(t *testing.T) {
	if got := providerURL("  ", "models"); got != "" {
		t.Errorf("got %q, want empty so the caller can refuse", got)
	}
}

// The speech field, which is where the convention came from.
func TestSpeechURLAcceptsBothConventions(t *testing.T) {
	for _, in := range []string{
		"http://localhost:8002",
		"http://localhost:8002/v1",
		"http://localhost:8002/v1/",
		// And the whole endpoint, for somebody who pasted that instead.
		"http://localhost:8002/v1/audio/speech",
	} {
		if got := speechURL(in); got != "http://localhost:8002/v1/audio/speech" {
			t.Errorf("speechURL(%q) = %q", in, got)
		}
	}
}
