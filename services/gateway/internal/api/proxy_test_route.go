package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Testing a proxy answers three questions, and they fail separately.
//
// Measured by hand on 2026-08-28 while this was designed, and the reason this
// route exists in this shape: two commands settled in five seconds what reading
// configuration could not settle at all.
//
//  1. **Does the proxy carry a request at all?** Wrong password, wrong port, a
//     provider that has suspended the account — all of these look identical
//     from inside the app: captions simply do not arrive.
//  2. **Does it actually change the address?** A transparent proxy carries the
//     request perfectly and leaves the address alone, so YouTube refuses
//     exactly as before. That is the failure most likely to be mistaken for
//     "the proxy is broken" when it is doing precisely what it was told.
//  3. **Does YouTube then answer?** An address can be reachable, different, and
//     still refused — a datacenter range, or a residential address that has had
//     its own turn at being blocked.
//
// A single pass/fail verdict answers none of them. So this reports the address
// each way round and the outcome of one real caption fetch, and lets the person
// reading it draw the conclusion.
const (
	// Somebody else's address-echo service is the only way to learn the address
	// a request leaves by. Chosen because it answers plain text and nothing
	// else, so there is no parser here to be wrong.
	proxyEchoURL = "https://ipv4.webshare.io/"

	// Kept short. This runs while somebody is looking at a settings screen, and
	// a proxy that is going to fail usually fails by not answering.
	proxyTestTimeout = 20 * time.Second
)

type proxyTestResult struct {
	// The address each way round. Equal means the proxy is not changing it,
	// which is question 2 above and is invisible without both numbers.
	DirectIP string `json:"directIp,omitempty"`
	ProxyIP  string `json:"proxyIp,omitempty"`

	// A code rather than a sentence — the server does not know what language
	// the viewer reads (§4b).
	Code string `json:"code,omitempty"`

	// What one real caption fetch did. `Cues` is the number that matters: a
	// server can answer 200 with an empty transcript, and a status code calls
	// that success.
	CaptionsOK   bool   `json:"captionsOk"`
	CaptionsLang string `json:"captionsLang,omitempty"`
	Cues         int    `json:"cues,omitempty"`
	CaptionsCode string `json:"captionsCode,omitempty"`

	TookMS int64 `json:"tookMs"`
}

// handleTestProxy tests the values in the *form*, not the ones on disk.
//
// The same rule the speech test already follows: testing after saving is
// testing what you have already accepted, and the whole point of pressing this
// is to find out before committing to it.
func (g *Gateway) handleTestProxy(w http.ResponseWriter, r *http.Request) {
	var submitted proxyConfig
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&submitted)
	cfg := mergeSubmittedProxyConfig(loadProxyConfig(g.proxyConfigPath()), submitted)

	start := time.Now()
	result := proxyTestResult{}

	if strings.TrimSpace(cfg.URL) == "" {
		writeJSON(w, http.StatusOK, proxyTestResult{Code: "proxy_url_missing"})
		return
	}
	if err := validateProxyURL(cfg.URL); err != nil {
		writeJSON(w, http.StatusOK, proxyTestResult{Code: err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), proxyTestTimeout)
	defer cancel()

	// Direct first, and its failure is not fatal: this household's connection
	// may be behind something that blocks the echo service outright, and that
	// says nothing about whether the proxy works. An empty direct address just
	// means the comparison cannot be made.
	result.DirectIP, _ = fetchOutboundIP(ctx, nil)

	proxied, err := fetchOutboundIP(ctx, &cfg.URL)
	if err != nil {
		result.Code = "proxy_unreachable"
		result.TookMS = time.Since(start).Milliseconds()
		g.logger.Warn("proxy test", "url", maskProxyURL(cfg.URL), "error", err)
		writeJSON(w, http.StatusOK, result)
		return
	}
	result.ProxyIP = proxied

	if result.DirectIP != "" && result.DirectIP == proxied {
		// Reachable, and pointless. Named rather than left for the reader to
		// notice two identical numbers.
		result.Code = "proxy_not_changing_address"
	}

	// Then the question the proxy was bought to answer.
	lang, cues, captionErr := testCaptionsThroughProxy(ctx, cfg.URL)
	switch {
	case captionErr != nil:
		result.CaptionsCode = "captions_refused"
		g.logger.Warn("proxy test captions", "error", captionErr)
	case cues == 0:
		result.CaptionsCode = "captions_empty"
	default:
		result.CaptionsOK = true
		result.CaptionsLang = lang
		result.Cues = cues
	}

	result.TookMS = time.Since(start).Milliseconds()
	g.logger.Info("proxy test",
		"direct_ip", result.DirectIP, "proxy_ip", result.ProxyIP,
		"captions_ok", result.CaptionsOK, "cues", result.Cues,
		"took_ms", result.TookMS)
	writeJSON(w, http.StatusOK, result)
}

// fetchOutboundIP asks what address a request leaves by, optionally through a
// proxy.
//
// A client per call rather than a shared one: this is two requests on a settings
// screen, and a `http.Transport` configured with somebody's half-typed proxy URL
// is not a thing to keep.
func fetchOutboundIP(ctx context.Context, proxyURL *string) (string, error) {
	transport := &http.Transport{}
	if proxyURL != nil {
		parsed, err := url.Parse(strings.TrimSpace(*proxyURL))
		if err != nil {
			return "", err
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	client := &http.Client{Transport: transport, Timeout: proxyTestTimeout}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyEchoURL, nil)
	if err != nil {
		return "", err
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()

	// Bounded: this answers one address, and an unbounded read of somebody
	// else's service is this process's memory.
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<10))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

// testCaptionsThroughProxy asks the local transcript server for one real video.
//
// Through the same door the app itself uses, deliberately — a test that took a
// different path could pass while the real one fails.
func testCaptionsThroughProxy(ctx context.Context, proxyURL string) (string, int, error) {
	q := url.Values{"video_id": {transcriptTestVideo}, "langs": {transcriptLanguages}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		transcriptServerURL+"?"+q.Encode(), nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set(transcriptProxyHeader, proxyURL)

	res, err := (&http.Client{Timeout: proxyTestTimeout}).Do(req)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return "", 0, err
	}
	var answer transcriptAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		return "", 0, err
	}
	if answer.Error != "" {
		return "", 0, errFromTranscript(answer.Error)
	}
	return answer.Language, countCues(answer.VTT), nil
}
