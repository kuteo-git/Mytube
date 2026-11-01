// Package rpc serves the cast service over ConnectRPC.
package rpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	castv1 "github.com/lucnguyen/local-youtube/gen/go/cast/v1"
	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
	"github.com/lucnguyen/local-youtube/services/cast/internal/usecase"
)

type Server struct{ engine *usecase.Engine }

func NewServer(engine *usecase.Engine) *Server { return &Server{engine: engine} }

// Codes the gateway reads to choose a status. The message carries a short
// machine word as well, because ABORTED means two different things here.
const (
	MsgStaleVersion = "stale_version"
	MsgEnded        = "session_ended"
)

func toConnect(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, domain.ErrStaleVersion):
		return connect.NewError(connect.CodeAborted, errors.New(MsgStaleVersion))
	case errors.Is(err, domain.ErrEnded):
		return connect.NewError(connect.CodeAborted, errors.New(MsgEnded))
	case errors.Is(err, domain.ErrLive):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, domain.ErrDeviceOffline):
		return connect.NewError(connect.CodeUnavailable, err)
	case errors.Is(err, domain.ErrEmptyQueue), errors.Is(err, usecase.ErrBadIndex):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func (s *Server) ListDevices(ctx context.Context, _ *connect.Request[castv1.ListDevicesRequest]) (*connect.Response[castv1.ListDevicesResponse], error) {
	devices, err := s.engine.Devices(ctx)
	if err != nil {
		return nil, toConnect(err)
	}
	sessions, err := s.engine.Sessions(ctx)
	if err != nil {
		return nil, toConnect(err)
	}
	active := map[string]string{}
	for _, sess := range sessions {
		active[sess.DeviceID] = sess.ID
	}
	out := make([]*castv1.Device, 0, len(devices))
	for _, d := range devices {
		out = append(out, &castv1.Device{
			Id: d.ID, Name: d.Name, Kind: kind(d.Kind), Model: d.Model, ActiveSessionId: active[d.ID],
		})
	}
	return connect.NewResponse(&castv1.ListDevicesResponse{Devices: out}), nil
}

func (s *Server) ListSessions(ctx context.Context, _ *connect.Request[castv1.ListSessionsRequest]) (*connect.Response[castv1.ListSessionsResponse], error) {
	sessions, err := s.engine.Sessions(ctx)
	if err != nil {
		return nil, toConnect(err)
	}
	out := make([]*castv1.Session, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, toProto(sess))
	}
	return connect.NewResponse(&castv1.ListSessionsResponse{Sessions: out}), nil
}

func (s *Server) StartSession(ctx context.Context, req *connect.Request[castv1.StartSessionRequest]) (*connect.Response[castv1.StartSessionResponse], error) {
	m := req.Msg
	sess, err := s.engine.StartSession(ctx, usecase.StartRequest{
		UserID: m.GetUserId(), DeviceID: m.GetDeviceId(), VideoIDs: m.GetVideoIds(),
		StartIndex: int(m.GetStartIndex()), StartPositionS: int(m.GetStartPositionSeconds()),
	})
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&castv1.StartSessionResponse{Session: toProto(sess)}), nil
}

func (s *Server) GetSession(ctx context.Context, req *connect.Request[castv1.GetSessionRequest]) (*connect.Response[castv1.GetSessionResponse], error) {
	sess, err := s.engine.Session(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&castv1.GetSessionResponse{Session: toProto(sess)}), nil
}

func (s *Server) Pause(ctx context.Context, req *connect.Request[castv1.PauseRequest]) (*connect.Response[castv1.PauseResponse], error) {
	sess, err := s.engine.Pause(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&castv1.PauseResponse{Session: toProto(sess)}), nil
}

func (s *Server) Resume(ctx context.Context, req *connect.Request[castv1.ResumeRequest]) (*connect.Response[castv1.ResumeResponse], error) {
	sess, err := s.engine.Resume(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&castv1.ResumeResponse{Session: toProto(sess)}), nil
}

func (s *Server) Next(ctx context.Context, req *connect.Request[castv1.NextRequest]) (*connect.Response[castv1.NextResponse], error) {
	sess, err := s.engine.Next(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&castv1.NextResponse{Session: toProto(sess)}), nil
}

func (s *Server) Stop(ctx context.Context, req *connect.Request[castv1.StopRequest]) (*connect.Response[castv1.StopResponse], error) {
	sess, err := s.engine.Stop(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&castv1.StopResponse{Session: toProto(sess)}), nil
}

func (s *Server) Seek(ctx context.Context, req *connect.Request[castv1.SeekRequest]) (*connect.Response[castv1.SeekResponse], error) {
	sess, err := s.engine.Seek(ctx, req.Msg.GetSessionId(), int(req.Msg.GetPositionSeconds()))
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&castv1.SeekResponse{Session: toProto(sess)}), nil
}

func (s *Server) SetVolume(ctx context.Context, req *connect.Request[castv1.SetVolumeRequest]) (*connect.Response[castv1.SetVolumeResponse], error) {
	sess, err := s.engine.SetVolume(ctx, req.Msg.GetSessionId(), int(req.Msg.GetPercent()))
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&castv1.SetVolumeResponse{Session: toProto(sess)}), nil
}

func (s *Server) Jump(ctx context.Context, req *connect.Request[castv1.JumpRequest]) (*connect.Response[castv1.JumpResponse], error) {
	sess, err := s.engine.Jump(ctx, req.Msg.GetSessionId(), int(req.Msg.GetIndex()))
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&castv1.JumpResponse{Session: toProto(sess)}), nil
}

func (s *Server) ReplaceQueue(ctx context.Context, req *connect.Request[castv1.ReplaceQueueRequest]) (*connect.Response[castv1.ReplaceQueueResponse], error) {
	m := req.Msg
	sess, err := s.engine.ReplaceQueue(ctx, m.GetSessionId(), int(m.GetVersion()), m.GetVideoIds(), int(m.GetCurrentIndex()))
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&castv1.ReplaceQueueResponse{Session: toProto(sess)}), nil
}

func (s *Server) Handoff(ctx context.Context, req *connect.Request[castv1.HandoffRequest]) (*connect.Response[castv1.HandoffResponse], error) {
	videoID, position, err := s.engine.Handoff(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&castv1.HandoffResponse{VideoId: videoID, PositionSeconds: int32(position)}), nil
}

func kind(k domain.Kind) castv1.DeviceKind {
	switch k {
	case domain.KindR1:
		return castv1.DeviceKind_DEVICE_KIND_R1
	case domain.KindGoogle:
		return castv1.DeviceKind_DEVICE_KIND_GOOGLE
	default:
		return castv1.DeviceKind_DEVICE_KIND_UNSPECIFIED
	}
}

var states = map[domain.State]castv1.SessionState{
	domain.Loading: castv1.SessionState_SESSION_STATE_LOADING,
	domain.Playing: castv1.SessionState_SESSION_STATE_PLAYING,
	domain.Paused:  castv1.SessionState_SESSION_STATE_PAUSED,
	domain.Ended:   castv1.SessionState_SESSION_STATE_ENDED,
}

var reasons = map[domain.EndedReason]castv1.EndedReason{
	domain.QueueFinished:   castv1.EndedReason_ENDED_REASON_QUEUE_FINISHED,
	domain.Stopped:         castv1.EndedReason_ENDED_REASON_STOPPED,
	domain.Replaced:        castv1.EndedReason_ENDED_REASON_REPLACED,
	domain.TakenOver:       castv1.EndedReason_ENDED_REASON_TAKEN_OVER,
	domain.StoppedOnDevice: castv1.EndedReason_ENDED_REASON_STOPPED_ON_DEVICE,
	domain.DeviceOffline:   castv1.EndedReason_ENDED_REASON_DEVICE_OFFLINE,
	domain.Handoff:         castv1.EndedReason_ENDED_REASON_HANDOFF,
	domain.Failed:          castv1.EndedReason_ENDED_REASON_FAILED,
}

func toProto(s domain.Session) *castv1.Session {
	queue := make([]*castv1.QueueItem, len(s.Queue))
	for i, it := range s.Queue {
		queue[i] = &castv1.QueueItem{
			VideoId: it.VideoID, Title: it.Title, Channel: it.Channel,
			ThumbnailUrl: it.ThumbnailURL, DurationSeconds: int32(it.DurationS),
		}
	}
	return &castv1.Session{
		Id:     s.ID,
		Device: &castv1.Device{Id: s.DeviceID, Name: s.DeviceName, Kind: kind(s.DeviceKind)},
		State:  states[s.State], EndedReason: reasons[s.EndedReason],
		Queue: queue, CurrentIndex: int32(s.CurrentIndex),
		PositionSeconds: int32(s.PositionS), DurationSeconds: int32(s.DurationS), Volume: int32(s.Volume),
		Version: int32(s.Version), StartedBy: s.StartedBy, UpdatedAt: timestamppb.New(s.UpdatedAt),
	}
}
