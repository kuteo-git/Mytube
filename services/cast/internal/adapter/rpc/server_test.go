package rpc

import (
	"testing"

	"connectrpc.com/connect"

	castv1 "github.com/lucnguyen/local-youtube/gen/go/cast/v1"
	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
)

func TestEveryStateAndReasonHasAWireValue(t *testing.T) {
	for _, st := range []domain.State{domain.Loading, domain.Playing, domain.Paused, domain.Ended} {
		if states[st] == castv1.SessionState_SESSION_STATE_UNSPECIFIED {
			t.Errorf("state %q has no wire value", st)
		}
	}
	for _, r := range []domain.EndedReason{
		domain.QueueFinished, domain.Stopped, domain.Replaced, domain.TakenOver,
		domain.StoppedOnDevice, domain.DeviceOffline, domain.Handoff, domain.Failed,
	} {
		if reasons[r] == castv1.EndedReason_ENDED_REASON_UNSPECIFIED {
			t.Errorf("reason %q has no wire value", r)
		}
	}
}

func TestErrorsBecomeTheCodesTheGatewayReads(t *testing.T) {
	cases := map[error]connect.Code{
		domain.ErrNotFound:      connect.CodeNotFound,
		domain.ErrStaleVersion:  connect.CodeAborted,
		domain.ErrEnded:         connect.CodeAborted,
		domain.ErrLive:          connect.CodeFailedPrecondition,
		domain.ErrDeviceOffline: connect.CodeUnavailable,
		domain.ErrEmptyQueue:    connect.CodeInvalidArgument,
	}
	for err, want := range cases {
		if got := connect.CodeOf(toConnect(err)); got != want {
			t.Errorf("%v -> %v, want %v", err, got, want)
		}
	}
}
