package rpc

import "testing"

// The regression this guards: snapshotID is built from userID+"|"+topic, so it
// contains a "|" of its own. Splitting the token on the first "|" instead of
// the last one truncates the snapshot id, making every page-2 request miss the
// snapshot and re-rank from scratch — which is what made "page 2" repeat
// almost all of page 1 in production.
func TestDecodeTokenSurvivesAPipeInsideTheSnapshotID(t *testing.T) {
	token := encodeToken("u_luc|#3", 24)

	snapshotID, offset := decodeToken(token)
	if snapshotID != "u_luc|#3" {
		t.Fatalf("snapshotID = %q, want %q", snapshotID, "u_luc|#3")
	}
	if offset != 24 {
		t.Fatalf("offset = %d, want 24", offset)
	}
}

func TestDecodeTokenRoundTripsAPlainSnapshotID(t *testing.T) {
	token := encodeToken("u_luc|", 0)

	snapshotID, offset := decodeToken(token)
	if snapshotID != "u_luc|" {
		t.Fatalf("snapshotID = %q, want %q", snapshotID, "u_luc|")
	}
	if offset != 0 {
		t.Fatalf("offset = %d, want 0", offset)
	}
}

func TestDecodeTokenHandlesEmptyAndGarbageInput(t *testing.T) {
	if id, offset := decodeToken(""); id != "" || offset != 0 {
		t.Fatalf("empty token: got (%q, %d), want (\"\", 0)", id, offset)
	}
	if id, offset := decodeToken("not-valid-base64!!!"); id != "" || offset != 0 {
		t.Fatalf("garbage token: got (%q, %d), want (\"\", 0)", id, offset)
	}
}
