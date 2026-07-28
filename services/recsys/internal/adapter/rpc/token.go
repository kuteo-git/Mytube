package rpc

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

var errUnknownSignal = errors.New("unknown signal type")

// Page tokens are opaque to the client by contract, so their shape can change
// freely. They now carry two things: which frozen ordering to read, and how far
// into it the reader is.
func encodeToken(snapshotID string, offset int32) string {
	raw := snapshotID + "|" + strconv.Itoa(int(offset))
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeToken(token string) (snapshotID string, offset int32) {
	if token == "" {
		return "", 0
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", 0
	}
	raw := string(decoded)
	// The snapshot id itself embeds "|" — it is built from userID+"|"+topic — so
	// splitting on the first "|" cuts it apart wrongly. The offset is always a
	// plain decimal with no "|" in it, so the last "|" is the real boundary.
	i := strings.LastIndex(raw, "|")
	if i < 0 {
		return "", 0
	}
	snapshotID, offsetPart := raw[:i], raw[i+1:]
	n, err := strconv.Atoi(offsetPart)
	if err != nil || n < 0 {
		return snapshotID, 0
	}
	return snapshotID, int32(n)
}
