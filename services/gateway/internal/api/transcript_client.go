package api

import (
	"fmt"
	"strings"
)

// The captions helper, which is no longer a setting.
//
// It used to be one: a base URL and a shared secret, on the reasoning that
// YouTube blocks by address and another machine is another address. The first
// half is true and the second is not — measured 2026-08-27 on this household's
// Home Assistant box, which was refused with exactly the 429 the app was
// getting, in the same minute. A second door in the same wall is not a second
// door.
//
// What changes the address is a proxy (see proxy_config.go), so the helper moved
// to loopback where it has no address of its own worth choosing, and the two
// fields that used to be on the settings screen went with it. `scripts/dev.sh`
// starts it; nobody configures it.
const transcriptServerURL = "http://127.0.0.1:8185/transcript"

// How the proxy reaches the Python side.
//
// Sent per request rather than read from that process's environment, and the
// reason is the same one written into remotetranscript's Config: a caption fetch
// can be started by the retry sweep, on a timer, with no request to carry
// anything on — so settings are read at the moment of use. A proxy read once at
// start-up over there would mean saving this form did nothing until somebody
// restarted a different process, which is the trap `internal/mediaroot` exists
// to document. It also leaves that server holding no credential at all.
//
// A header rather than a query parameter, because it carries a password and a
// query string lands in access logs.
const transcriptProxyHeader = "X-Transcript-Proxy"

// A video with captions in both languages this household reads, so a test says
// something about the ordering as well as about the connection.
const transcriptTestVideo = "dQw4w9WgXcQ"

// The languages this app can use, in the order it wants them. Sent to the
// helper rather than left to it: "Vietnamese if there is any, else English" is
// one rule and it lives on this side, or two servers will disagree about it.
const transcriptLanguages = "vi,en"

// transcriptAnswer is what the helper sends back.
type transcriptAnswer struct {
	Language  string `json:"language"`
	Generated bool   `json:"generated"`
	VTT       string `json:"vtt"`
	Error     string `json:"error"`
	// Which of the two things went wrong, decided on the Python side where the
	// exception is: "proxy" means the request never reached YouTube, "upstream"
	// means it did and was answered badly. Without it both arrive here as a
	// string and the difference has to be guessed from its wording.
	Kind string `json:"kind"`
}

func errFromTranscript(message string) error {
	return fmt.Errorf("%s", message)
}

// countCues counts timing lines, which is what a cue is.
//
// The number matters more than the status: the helper can answer 200 with an
// empty transcript, and a status code calls that success.
func countCues(vtt string) int {
	n := 0
	for _, line := range strings.Split(vtt, "\n") {
		if strings.Contains(line, "-->") {
			n++
		}
	}
	return n
}
