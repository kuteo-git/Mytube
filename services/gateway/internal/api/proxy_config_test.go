package api

import (
	"strings"
	"testing"
)

// The password is the one part of the URL the browser must never be handed back,
// and the rest is the part it must be.
//
// Everywhere else on this screen a credential is simply never sent (`keyHint`,
// ttsConfig, translateConfig). That rule cannot apply unchanged here: the
// password lives *inside* the single field somebody has to be able to read, and
// blanking the whole thing leaves them unable to tell which provider they
// configured — the first question anyone asks when captions stop arriving.
func TestMaskProxyURLHidesOnlyThePassword(t *testing.T) {
	masked := maskProxyURL("http://proxy-user:s3cr3t@p.webshare.io:80")

	if strings.Contains(masked, "s3cr3t") {
		t.Fatalf("the password survived masking: %s", masked)
	}
	for _, keep := range []string{"http://", "proxy-user", "p.webshare.io", ":80"} {
		if !strings.Contains(masked, keep) {
			t.Errorf("masking lost %q, which is what identifies the proxy: %s", keep, masked)
		}
	}
	// Readable, not percent-encoded. url.URL escapes the mask when it prints,
	// and "%E2%80%A2%E2%80%A2%E2%80%A2%E2%80%A2" in a text field looks like
	// corruption rather than a hidden password.
	if !strings.Contains(masked, proxyPasswordMask) {
		t.Errorf("the mask was escaped rather than shown: %s", masked)
	}
}

func TestMaskProxyURLLeavesAlonWhatHasNoPassword(t *testing.T) {
	for _, raw := range []string{"", "http://p.webshare.io:80", "socks5://host:1080"} {
		if got := maskProxyURL(raw); got != raw {
			t.Errorf("maskProxyURL(%q) = %q, want it untouched", raw, got)
		}
	}
}

// Saving a change to a switch must not write the mask into the file as the
// password.
//
// This is the whole reason merging exists here. The browser only ever holds the
// masked URL, so every save posts it back — and taking it literally would
// destroy the credential the first time somebody toggled anything.
func TestSavingWithAMaskedURLKeepsTheStoredPassword(t *testing.T) {
	stored := proxyConfig{URL: "http://user:secret@p.webshare.io:80"}
	submitted := proxyConfig{
		URL:         maskProxyURL(stored.URL),
		Enabled:     true,
		ForCaptions: true,
	}

	merged := mergeSubmittedProxyConfig(stored, submitted)

	if merged.URL != stored.URL {
		t.Fatalf("password not restored: %s", merged.URL)
	}
	if !merged.Enabled || !merged.ForCaptions {
		t.Error("the switches that were actually submitted did not survive")
	}
}

// Editing the visible part while leaving the mask alone changes that part and
// keeps the password.
//
// Without this, moving to a different port or username would mean going back to
// the provider's dashboard for a password that has not changed.
func TestEditingAroundTheMaskKeepsThePassword(t *testing.T) {
	stored := proxyConfig{URL: "http://user:secret@p.webshare.io:80"}
	submitted := proxyConfig{URL: "http://user:" + proxyPasswordMask + "@p.webshare.io:1080"}

	merged := mergeSubmittedProxyConfig(stored, submitted)

	if !strings.Contains(merged.URL, ":secret@") {
		t.Errorf("password lost while editing the port: %s", merged.URL)
	}
	if !strings.Contains(merged.URL, ":1080") {
		t.Errorf("the edit was discarded: %s", merged.URL)
	}
}

// A real new password goes in as typed.
func TestANewPasswordReplacesTheStoredOne(t *testing.T) {
	stored := proxyConfig{URL: "http://user:secret@p.webshare.io:80"}
	submitted := proxyConfig{URL: "http://user:brandnew@p.webshare.io:80"}

	if got := mergeSubmittedProxyConfig(stored, submitted).URL; got != submitted.URL {
		t.Errorf("got %s, want the newly typed URL", got)
	}
}

// Clearing the field clears the proxy. It is how somebody removes a credential,
// so an empty submission is honoured rather than read as "unchanged".
func TestClearingTheFieldClearsIt(t *testing.T) {
	stored := proxyConfig{URL: "http://user:secret@p.webshare.io:80", Enabled: true}

	if got := mergeSubmittedProxyConfig(stored, proxyConfig{}).URL; got != "" {
		t.Errorf("got %q, want the proxy removed", got)
	}
}

// What the form refuses, and why each one is worth refusing here rather than at
// the moment of use — which is a caption fetch on a retry timer at three in the
// morning, leaving nothing but a log line nobody reads.
func TestValidateProxyURL(t *testing.T) {
	ok := []string{
		"",
		"http://user:pass@p.webshare.io:80",
		"https://host:443",
		"socks5://host:1080",
		"socks5h://user:pass@host:1080",
	}
	for _, raw := range ok {
		if err := validateProxyURL(raw); err != nil {
			t.Errorf("validateProxyURL(%q) = %v, want accepted", raw, err)
		}
	}

	bad := map[string]string{
		// The most likely paste: what a provider's dashboard shows under
		// "host:port". It parses as a URL whose *scheme* is the hostname, which
		// then fails much later and confusingly.
		"p.webshare.io:80": "proxy_url_scheme",
		"ftp://host:21":    "proxy_url_scheme",
		"http://":          "proxy_url_no_host",
	}
	for raw, want := range bad {
		err := validateProxyURL(raw)
		if err == nil {
			t.Errorf("validateProxyURL(%q) was accepted", raw)
			continue
		}
		if err.Error() != want {
			t.Errorf("validateProxyURL(%q) = %v, want %s", raw, err, want)
		}
	}
}
