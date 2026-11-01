package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"connectrpc.com/connect"

	castv1 "github.com/lucnguyen/local-youtube/gen/go/cast/v1"
)

// The cast routes: the household's speakers, and the sessions playing on them.
// Every client asks here and only here; the cast service talks to the speakers.
// Shapes are snake_case to match the plan the clients were written against
// (docs/superpowers/plans/2026-10-31-cast-to-speakers.md §3.1).
func (g *Gateway) castRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cast/devices", g.handleCastDevices)
	mux.HandleFunc("GET /api/cast/sessions", g.handleCastSessions)
	mux.HandleFunc("POST /api/cast/sessions", g.handleStartCast)
	mux.HandleFunc("GET /api/cast/sessions/{id}", g.handleGetCast)
	mux.HandleFunc("POST /api/cast/sessions/{id}/pause", g.castCommand(g.castPause))
	mux.HandleFunc("POST /api/cast/sessions/{id}/resume", g.castCommand(g.castResume))
	mux.HandleFunc("POST /api/cast/sessions/{id}/next", g.castCommand(g.castNext))
	mux.HandleFunc("POST /api/cast/sessions/{id}/stop", g.castCommand(g.castStop))
	mux.HandleFunc("POST /api/cast/sessions/{id}/seek", g.castCommand(g.castSeek))
	mux.HandleFunc("POST /api/cast/sessions/{id}/volume", g.castCommand(g.castVolume))
	mux.HandleFunc("POST /api/cast/sessions/{id}/jump", g.castCommand(g.castJump))
	mux.HandleFunc("PUT /api/cast/sessions/{id}/queue", g.castCommand(g.castQueue))
	mux.HandleFunc("POST /api/cast/sessions/{id}/handoff", g.handleCastHandoff)
}

type castDeviceDTO struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Model           string `json:"model,omitempty"`
	Online          bool   `json:"online"`
	ActiveSessionID string `json:"active_session_id,omitempty"`
}

type castItemDTO struct {
	VideoID      string `json:"video_id"`
	Title        string `json:"title"`
	Channel      string `json:"channel"`
	ThumbnailURL string `json:"thumbnail_url"`
	DurationS    int32  `json:"duration_s"`
}

type castSessionDTO struct {
	ID           string        `json:"id"`
	Device       castDeviceDTO `json:"device"`
	State        string        `json:"state"`
	EndedReason  *string       `json:"ended_reason"`
	Queue        []castItemDTO `json:"queue"`
	CurrentIndex int32         `json:"current_index"`
	PositionS    int32         `json:"position_s"`
	DurationS    int32         `json:"duration_s"`
	Volume       int32         `json:"volume"`
	Version      int32         `json:"version"`
	StartedBy    string        `json:"started_by"`
	UpdatedAt    string        `json:"updated_at"`
}

var castKinds = map[castv1.DeviceKind]string{
	castv1.DeviceKind_DEVICE_KIND_R1:     "r1",
	castv1.DeviceKind_DEVICE_KIND_GOOGLE: "google",
}

var castStates = map[castv1.SessionState]string{
	castv1.SessionState_SESSION_STATE_LOADING: "loading",
	castv1.SessionState_SESSION_STATE_PLAYING: "playing",
	castv1.SessionState_SESSION_STATE_PAUSED:  "paused",
	castv1.SessionState_SESSION_STATE_ENDED:   "ended",
}

var castReasons = map[castv1.EndedReason]string{
	castv1.EndedReason_ENDED_REASON_QUEUE_FINISHED:    "queue_finished",
	castv1.EndedReason_ENDED_REASON_STOPPED:           "stopped",
	castv1.EndedReason_ENDED_REASON_REPLACED:          "replaced",
	castv1.EndedReason_ENDED_REASON_TAKEN_OVER:        "taken_over",
	castv1.EndedReason_ENDED_REASON_STOPPED_ON_DEVICE: "stopped_on_device",
	castv1.EndedReason_ENDED_REASON_DEVICE_OFFLINE:    "device_offline",
	castv1.EndedReason_ENDED_REASON_HANDOFF:           "handoff",
	castv1.EndedReason_ENDED_REASON_FAILED:            "failed",
}

func toCastDevice(d *castv1.Device) castDeviceDTO {
	return castDeviceDTO{
		ID: d.GetId(), Name: d.GetName(), Kind: castKinds[d.GetKind()], Model: d.GetModel(),
		Online: true, ActiveSessionID: d.GetActiveSessionId(),
	}
}

func toCastSession(s *castv1.Session) castSessionDTO {
	queue := make([]castItemDTO, 0, len(s.GetQueue()))
	for _, it := range s.GetQueue() {
		queue = append(queue, castItemDTO{
			VideoID: it.GetVideoId(), Title: it.GetTitle(), Channel: it.GetChannel(),
			ThumbnailURL: it.GetThumbnailUrl(), DurationS: it.GetDurationSeconds(),
		})
	}
	var reason *string
	if r, ok := castReasons[s.GetEndedReason()]; ok {
		reason = &r
	}
	updated := ""
	if s.GetUpdatedAt() != nil {
		updated = s.GetUpdatedAt().AsTime().Format(time.RFC3339)
	}
	return castSessionDTO{
		ID: s.GetId(), Device: toCastDevice(s.GetDevice()), State: castStates[s.GetState()], EndedReason: reason,
		Queue: queue, CurrentIndex: s.GetCurrentIndex(), PositionS: s.GetPositionSeconds(),
		DurationS: s.GetDurationSeconds(), Volume: s.GetVolume(), Version: s.GetVersion(),
		StartedBy: s.GetStartedBy(), UpdatedAt: updated,
	}
}

// writeCastErr keeps what each refusal means. A stale queue version and an
// ended session are both conflicts, told apart by `code`, because the remote
// answers them differently: reload the queue, or close.
func (g *Gateway) writeCastErr(w http.ResponseWriter, r *http.Request, err error) {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		g.logger.Error("cast request failed", "path", r.URL.Path, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	switch connectErr.Code() {
	case connect.CodeNotFound:
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
	case connect.CodeAborted:
		writeJSON(w, http.StatusConflict, map[string]string{"code": connectErr.Message()})
	case connect.CodeFailedPrecondition:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "live"})
	case connect.CodeUnavailable:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "device_offline"})
	case connect.CodeInvalidArgument:
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid", "error": connectErr.Message()})
	default:
		g.logger.Error("cast request failed", "path", r.URL.Path, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"code": "cast_unavailable"})
	}
}

func (g *Gateway) handleCastDevices(w http.ResponseWriter, r *http.Request) {
	resp, err := g.cast.ListDevices(r.Context(), connect.NewRequest(&castv1.ListDevicesRequest{}))
	if err != nil {
		g.writeCastErr(w, r, err)
		return
	}
	out := make([]castDeviceDTO, 0, len(resp.Msg.GetDevices()))
	for _, d := range resp.Msg.GetDevices() {
		out = append(out, toCastDevice(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) handleCastSessions(w http.ResponseWriter, r *http.Request) {
	resp, err := g.cast.ListSessions(r.Context(), connect.NewRequest(&castv1.ListSessionsRequest{}))
	if err != nil {
		g.writeCastErr(w, r, err)
		return
	}
	out := make([]castSessionDTO, 0, len(resp.Msg.GetSessions()))
	for _, s := range resp.Msg.GetSessions() {
		out = append(out, toCastSession(s))
	}
	writeJSON(w, http.StatusOK, out)
}

type castItemsBody []struct {
	VideoID string `json:"video_id"`
}

func (items castItemsBody) ids() []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.VideoID)
	}
	return out
}

func (g *Gateway) handleStartCast(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceID       string        `json:"device_id"`
		Items          castItemsBody `json:"items"`
		StartIndex     int32         `json:"start_index"`
		StartPositionS int32         `json:"start_position_s"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	resp, err := g.cast.StartSession(r.Context(), connect.NewRequest(&castv1.StartSessionRequest{
		DeviceId: body.DeviceID, VideoIds: body.Items.ids(), StartIndex: body.StartIndex,
		StartPositionSeconds: body.StartPositionS, UserId: g.userID(r),
	}))
	if err != nil {
		g.writeCastErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toCastSession(resp.Msg.GetSession()))
}

func (g *Gateway) handleGetCast(w http.ResponseWriter, r *http.Request) {
	resp, err := g.cast.GetSession(r.Context(), connect.NewRequest(&castv1.GetSessionRequest{SessionId: r.PathValue("id")}))
	if err != nil {
		g.writeCastErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toCastSession(resp.Msg.GetSession()))
}

func (g *Gateway) handleCastHandoff(w http.ResponseWriter, r *http.Request) {
	resp, err := g.cast.Handoff(r.Context(), connect.NewRequest(&castv1.HandoffRequest{SessionId: r.PathValue("id")}))
	if err != nil {
		g.writeCastErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		VideoID   string `json:"video_id"`
		PositionS int32  `json:"position_s"`
	}{resp.Msg.GetVideoId(), resp.Msg.GetPositionSeconds()})
}

// castCommand adapts a command that answers with the session: reads the body
// it needs, calls, and writes the session or the refusal.
func (g *Gateway) castCommand(
	call func(r *http.Request, id string, body json.RawMessage) (*castv1.Session, error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		s, err := call(r, r.PathValue("id"), body)
		if err != nil {
			var bad badBody
			if errors.As(err, &bad) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
				return
			}
			g.writeCastErr(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, toCastSession(s))
	}
}

type badBody struct{ error }

func decode(body json.RawMessage, into any) error {
	if len(body) == 0 {
		return badBody{errors.New("missing body")}
	}
	if err := json.Unmarshal(body, into); err != nil {
		return badBody{err}
	}
	return nil
}

func (g *Gateway) castPause(r *http.Request, id string, _ json.RawMessage) (*castv1.Session, error) {
	resp, err := g.cast.Pause(r.Context(), connect.NewRequest(&castv1.PauseRequest{SessionId: id}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetSession(), nil
}

func (g *Gateway) castResume(r *http.Request, id string, _ json.RawMessage) (*castv1.Session, error) {
	resp, err := g.cast.Resume(r.Context(), connect.NewRequest(&castv1.ResumeRequest{SessionId: id}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetSession(), nil
}

func (g *Gateway) castNext(r *http.Request, id string, _ json.RawMessage) (*castv1.Session, error) {
	resp, err := g.cast.Next(r.Context(), connect.NewRequest(&castv1.NextRequest{SessionId: id}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetSession(), nil
}

func (g *Gateway) castStop(r *http.Request, id string, _ json.RawMessage) (*castv1.Session, error) {
	resp, err := g.cast.Stop(r.Context(), connect.NewRequest(&castv1.StopRequest{SessionId: id}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetSession(), nil
}

func (g *Gateway) castSeek(r *http.Request, id string, body json.RawMessage) (*castv1.Session, error) {
	var b struct {
		PositionS int32 `json:"position_s"`
	}
	if err := decode(body, &b); err != nil {
		return nil, err
	}
	resp, err := g.cast.Seek(r.Context(), connect.NewRequest(&castv1.SeekRequest{SessionId: id, PositionSeconds: b.PositionS}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetSession(), nil
}

func (g *Gateway) castVolume(r *http.Request, id string, body json.RawMessage) (*castv1.Session, error) {
	var b struct {
		Percent int32 `json:"percent"`
	}
	if err := decode(body, &b); err != nil {
		return nil, err
	}
	resp, err := g.cast.SetVolume(r.Context(), connect.NewRequest(&castv1.SetVolumeRequest{SessionId: id, Percent: b.Percent}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetSession(), nil
}

func (g *Gateway) castJump(r *http.Request, id string, body json.RawMessage) (*castv1.Session, error) {
	var b struct {
		Index int32 `json:"index"`
	}
	if err := decode(body, &b); err != nil {
		return nil, err
	}
	resp, err := g.cast.Jump(r.Context(), connect.NewRequest(&castv1.JumpRequest{SessionId: id, Index: b.Index}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetSession(), nil
}

func (g *Gateway) castQueue(r *http.Request, id string, body json.RawMessage) (*castv1.Session, error) {
	var b struct {
		Version      int32         `json:"version"`
		Items        castItemsBody `json:"items"`
		CurrentIndex int32         `json:"current_index"`
	}
	if err := decode(body, &b); err != nil {
		return nil, err
	}
	resp, err := g.cast.ReplaceQueue(r.Context(), connect.NewRequest(&castv1.ReplaceQueueRequest{
		SessionId: id, Version: b.Version, VideoIds: b.Items.ids(), CurrentIndex: b.CurrentIndex,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetSession(), nil
}
