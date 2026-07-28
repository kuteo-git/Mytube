package rpc

import (
	"errors"
	"strconv"
)

var errUnknownSignal = errors.New("unknown signal type")

// Paging tokens are opaque to the client so the encoding can change without an
// API break.
func decodeToken(token string) int32 {
	offset, err := strconv.Atoi(token)
	if err != nil || offset < 0 {
		return 0
	}
	return int32(offset)
}

func encodeToken(offset int32) string {
	return strconv.Itoa(int(offset))
}
