package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// proxyConfig is one outbound proxy, and which kinds of traffic go through it.
//
// ## Why this exists
//
// YouTube refuses things by **public address**. Measured 2026-08-28, four
// videos, one minute, one machine: asking for captions directly was `IpBlocked`
// 4 of 4, and asking through a rotating residential proxy succeeded 4 of 4.
// Nothing in the shape of the request makes a difference — the refusal is about
// the house.
//
// This replaces the transcript settings entirely. Those asked "which other
// machine should we ask", on the reasoning that another machine is another
// address; that was measured on this household's Home Assistant box and is
// false — same house, same address, same 429 in the same minute. The transcript
// server still exists and still does the work, but it is now an internal
// detail on loopback with nothing to configure. The address is the setting.
//
// ## Why the traffic is chosen per purpose
//
// A residential proxy is metered by the gigabyte, and the four kinds of traffic
// here differ by three orders of magnitude: a caption file is tens of kilobytes
// and a 1080p video is hundreds of megabytes. One switch covering both would
// mean somebody turning on captions and finding a month's bandwidth gone by
// morning, with nothing anywhere having said so.
type proxyConfig struct {
	// URL is the whole proxy in one field: scheme://user:pass@host:port.
	//
	// One field rather than four, because that is the form every provider hands
	// out (Webshare, Bright Data, Oxylabs, IPRoyal) and the form both consumers
	// take — go-ytdlp's `.Proxy()` and Python's `GenericProxyConfig`. Four
	// separate boxes would mean everybody splits the string by hand and this
	// code joins it again: two places to get it wrong for no gain.
	URL string `json:"url"`

	// Enabled is the master switch.
	//
	// Separate from clearing the URL so that turning the proxy off for an
	// evening does not mean pasting the credential back in afterwards.
	Enabled bool `json:"enabled"`

	// Which traffic goes through it. Every one of these names a real outbound
	// path that can genuinely take a proxy — §5 forbids a switch that does
	// nothing, and that applies hardest to a switch about money.
	ForCaptions bool `json:"forCaptions"`
	ForListings bool `json:"forListings"`
	ForMedia    bool `json:"forMedia"`
	ForComments bool `json:"forComments"`
}

// The name is shared with ingest, which reads this file rather than being told
// its contents per request — the caption fetch it belongs to is also started by
// a timer nobody is waiting on. The same arrangement transcript-config.json had.
const proxyConfigFile = "proxy.json"

func (g *Gateway) proxyConfigPath() string {
	return filepath.Join(g.configDir, proxyConfigFile)
}

func loadProxyConfig(path string) proxyConfig {
	var cfg proxyConfig
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		// A corrupt file is not a reason to stop the library working. It means
		// no proxy, which is what every install starts as.
		return proxyConfig{}
	}
	return cfg
}

func saveProxyConfig(path string, cfg proxyConfig) error {
	blob, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// 0600: the URL holds a password.
	return withFileLock(path, func() error {
		return writeFileAtomicMode(path, blob, 0o600)
	})
}

// validateProxyURL answers whether this is something the consumers can use, and
// says what is wrong when it is not.
//
// Checked here rather than at the moment of use, because the moment of use is a
// caption fetch on a retry timer at three in the morning, and the only evidence
// left would be a line in a log nobody reads. A settings form can say it while
// somebody is looking at it.
func validateProxyURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("proxy_url_unparseable")
	}
	// Named explicitly rather than defaulted. A bare "host:port" parses as a
	// URL whose scheme is the host, which is the kind of wrong that produces a
	// confusing failure much later.
	switch parsed.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("proxy_url_scheme")
	}
	if parsed.Host == "" {
		return fmt.Errorf("proxy_url_no_host")
	}
	return nil
}

// maskProxyURL is what the browser is allowed to see.
//
// The rule everywhere else on this screen is that a credential is never sent
// back at all (`keyHint`, ttsConfig, translateConfig). That rule cannot apply
// unchanged here, because the password lives *inside* the one field the user
// has to be able to read: blank it entirely and nobody can tell which provider
// they configured, which is the first thing anyone wants to know when captions
// stop arriving.
//
// So the password alone is replaced, and everything identifying is kept:
//
//	http://proxy-user:s3cr3t@p.webshare.io:80
//	http://proxy-user:••••@p.webshare.io:80
func maskProxyURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.User == nil {
		return trimmed
	}
	if _, hasPassword := parsed.User.Password(); !hasPassword {
		return trimmed
	}
	parsed.User = url.UserPassword(parsed.User.Username(), proxyPasswordMask)
	// url.URL escapes the mask when it prints; put the readable one back.
	return strings.Replace(parsed.String(), url.QueryEscape(proxyPasswordMask), proxyPasswordMask, 1)
}

// The stand-in for a password, and the token that means "unchanged" coming back.
const proxyPasswordMask = "••••"

// mergeSubmittedProxyConfig applies a form submission over what is stored.
//
// The URL comes back masked, so a save that changed only a switch would
// otherwise write the mask into the file as the password. A submitted URL whose
// password is still the mask keeps the stored one; anything else is a real
// change, including clearing the field.
func mergeSubmittedProxyConfig(current, submitted proxyConfig) proxyConfig {
	merged := submitted
	merged.URL = strings.TrimSpace(submitted.URL)
	if merged.URL != "" && strings.Contains(merged.URL, proxyPasswordMask) {
		// Same URL as before, or the user edited another part of it. Only the
		// password is taken from storage; the rest is what they typed, so
		// changing the host or the username still works without retyping the
		// password.
		merged.URL = restoreProxyPassword(merged.URL, current.URL)
	}
	return merged
}

// restoreProxyPassword puts the stored password back where the mask is.
//
// String surgery rather than parse-edit-print, and that is not laziness: a
// masked URL **cannot be parsed**. `url.Parse` rejects the bullets outright —
// "net/url: invalid userinfo" — so the obvious version silently returned the URL
// with the mask still in it, and the mask would then have been saved as the
// password. Caught by TestSavingWithAMaskedURLKeepsTheStoredPassword, which is
// why it exists.
//
// The mask is four bullet characters; nothing in a scheme, host or port can
// contain them, so the first occurrence is the password and only the password.
func restoreProxyPassword(masked, stored string) string {
	password, ok := passwordOf(stored)
	if !ok {
		return masked
	}
	return strings.Replace(masked, proxyPasswordMask, password, 1)
}

func passwordOf(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User == nil {
		return "", false
	}
	return parsed.User.Password()
}

func (g *Gateway) handleGetProxyConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, proxyConfigDTO(loadProxyConfig(g.proxyConfigPath())))
}

func proxyConfigDTO(cfg proxyConfig) map[string]any {
	return map[string]any{
		"url":         maskProxyURL(cfg.URL),
		"enabled":     cfg.Enabled,
		"forCaptions": cfg.ForCaptions,
		"forListings": cfg.ForListings,
		"forMedia":    cfg.ForMedia,
		"forComments": cfg.ForComments,
	}
}

func (g *Gateway) handleSaveProxyConfig(w http.ResponseWriter, r *http.Request) {
	var submitted proxyConfig
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&submitted); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	path := g.proxyConfigPath()
	merged := mergeSubmittedProxyConfig(loadProxyConfig(path), submitted)

	if err := validateProxyURL(merged.URL); err != nil {
		// A code, not a sentence: the server does not know what language the
		// viewer reads (§4b).
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": err.Error()})
		return
	}
	if err := saveProxyConfig(path, merged); err != nil {
		g.logger.Warn("proxy config save", "error", err)
		http.Error(w, "could not save", http.StatusServiceUnavailable)
		return
	}
	// The URL is logged masked. This line exists to answer "was it ever saved",
	// and a log file is exactly the sort of place a password should not be.
	g.logger.Info("proxy config saved",
		"url", maskProxyURL(merged.URL), "enabled", merged.Enabled,
		"captions", merged.ForCaptions, "listings", merged.ForListings,
		"media", merged.ForMedia, "comments", merged.ForComments)
	writeJSON(w, http.StatusOK, proxyConfigDTO(merged))
}
