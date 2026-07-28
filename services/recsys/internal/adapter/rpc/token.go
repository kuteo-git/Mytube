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
	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 {
		return "", 0
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n < 0 {
		return parts[0], 0
	}
	return parts[0], int32(n)
}
