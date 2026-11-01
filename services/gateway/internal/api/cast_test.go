package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	castv1 "github.com/lucnguyen/local-youtube/gen/go/cast/v1"
	"github.com/lucnguyen/local-youtube/gen/go/cast/v1/castv1connect"
)

type fakeCast struct {
	castv1connect.CastServiceClient
	start  *castv1.StartSessionRequest
	queue  *castv1.ReplaceQueueRequest
	seek   *castv1.SeekRequest
	narr   *castv1.SetNarrationRequest
	err    error
	called string
}

func sessionMsg() *castv1.Session {
	return &castv1.Session{
		Id:     "s1",
		Device: &castv1.Device{Id: "r1-living", Name: "Loa R1", Kind: castv1.DeviceKind_DEVICE_KIND_R1},
		State:  castv1.SessionState_SESSION_STATE_PLAYING,
		Queue: []*castv1.QueueItem{{VideoId: "abc", Title: "Bài", Channel: "Kênh",
			ThumbnailUrl: "https://i.ytimg.com/a.jpg", DurationSeconds: 245}},
		PositionSeconds: 90, DurationSeconds: 245, Volume: 0, Version: 2, StartedBy: "u_luc",
	}
}

func (f *fakeCast) StartSession(_ context.Context, req *connect.Request[castv1.StartSessionRequest]) (*connect.Response[castv1.StartSessionResponse], error) {
	f.start = req.Msg
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(&castv1.StartSessionResponse{Session: sessionMsg()}), nil
}

func (f *fakeCast) ReplaceQueue(_ context.Context, req *connect.Request[castv1.ReplaceQueueRequest]) (*connect.Response[castv1.ReplaceQueueResponse], error) {
	f.queue = req.Msg
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(&castv1.ReplaceQueueResponse{Session: sessionMsg()}), nil
}

func (f *fakeCast) Seek(_ context.Context, req *connect.Request[castv1.SeekRequest]) (*connect.Response[castv1.SeekResponse], error) {
	f.seek = req.Msg
	return connect.NewResponse(&castv1.SeekResponse{Session: sessionMsg()}), nil
}

func (f *fakeCast) Pause(_ context.Context, req *connect.Request[castv1.PauseRequest]) (*connect.Response[castv1.PauseResponse], error) {
	f.called = "pause " + req.Msg.GetSessionId()
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(&castv1.PauseResponse{Session: sessionMsg()}), nil
}

func (f *fakeCast) GetSession(context.Context, *connect.Request[castv1.GetSessionRequest]) (*connect.Response[castv1.GetSessionResponse], error) {
	if f.err != nil {
		return nil, f.err
	}
	s := sessionMsg()
	s.State, s.EndedReason = castv1.SessionState_SESSION_STATE_ENDED, castv1.EndedReason_ENDED_REASON_TAKEN_OVER
	return connect.NewResponse(&castv1.GetSessionResponse{Session: s}), nil
}

func (f *fakeCast) ListDevices(context.Context, *connect.Request[castv1.ListDevicesRequest]) (*connect.Response[castv1.ListDevicesResponse], error) {
	return connect.NewResponse(&castv1.ListDevicesResponse{Devices: []*castv1.Device{
		{Id: "r1-living", Name: "Loa R1", Kind: castv1.DeviceKind_DEVICE_KIND_R1, Model: "R1", ActiveSessionId: "s1"},
		{Id: "u1", Name: "Nest Mini", Kind: castv1.DeviceKind_DEVICE_KIND_GOOGLE, Model: "Google Nest Mini"},
	}}), nil
}

func (f *fakeCast) Handoff(context.Context, *connect.Request[castv1.HandoffRequest]) (*connect.Response[castv1.HandoffResponse], error) {
	return connect.NewResponse(&castv1.HandoffResponse{VideoId: "abc", PositionSeconds: 130}), nil
}

func (f *fakeCast) SetNarration(_ context.Context, req *connect.Request[castv1.SetNarrationRequest]) (*connect.Response[castv1.SetNarrationResponse], error) {
	f.narr = req.Msg
	s := sessionMsg()
	s.Narration, s.NarrationStatus = true, castv1.NarrationStatus_NARRATION_STATUS_PREPARING
	return connect.NewResponse(&castv1.SetNarrationResponse{Session: s}), nil
}

func castCall(t *testing.T, cast *fakeCast, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	g := &Gateway{logger: discardLogger(), cast: cast, devUserID: "u_default"}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-User-Id", "u_luc")
	rec := httptest.NewRecorder()
	g.Routes().ServeHTTP(rec, req)
	return rec
}

func TestStartingACastSendsTheProfileAndTheItems(t *testing.T) {
	cast := &fakeCast{}
	rec := castCall(t, cast, http.MethodPost, "/api/cast/sessions",
		`{"device_id":"r1-living","items":[{"video_id":"abc"},{"video_id":"def"}],"start_index":0,"start_position_s":90}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	if cast.start.GetUserId() != "u_luc" || cast.start.GetDeviceId() != "r1-living" ||
		strings.Join(cast.start.GetVideoIds(), ",") != "abc,def" || cast.start.GetStartPositionSeconds() != 90 {
		t.Fatalf("request = %+v", cast.start)
	}
	var view map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view["state"] != "playing" || view["position_s"] != float64(90) || view["ended_reason"] != nil {
		t.Fatalf("view = %v", view)
	}
	device := view["device"].(map[string]any)
	if device["kind"] != "r1" || device["name"] != "Loa R1" {
		t.Fatalf("device = %v", device)
	}
	item := view["queue"].([]any)[0].(map[string]any)
	if item["video_id"] != "abc" || item["thumbnail_url"] != "https://i.ytimg.com/a.jpg" || item["duration_s"] != float64(245) {
		t.Fatalf("item = %v", item)
	}
}

func TestAnEndedSessionSaysWhy(t *testing.T) {
	rec := castCall(t, &fakeCast{}, http.MethodGet, "/api/cast/sessions/s1", "")
	var view map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view["state"] != "ended" || view["ended_reason"] != "taken_over" {
		t.Fatalf("view = %v", view)
	}
}

func TestDevicesAreListedOnline(t *testing.T) {
	rec := castCall(t, &fakeCast{}, http.MethodGet, "/api/cast/devices", "")
	var devices []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &devices)
	if len(devices) != 2 || devices[0]["active_session_id"] != "s1" || devices[1]["kind"] != "google" ||
		devices[1]["online"] != true || devices[1]["active_session_id"] != nil {
		t.Fatalf("devices = %v", devices)
	}
}

func TestCastErrorsKeepTheirMeaning(t *testing.T) {
	cases := []struct {
		err  error
		want int
		code string
	}{
		{connect.NewError(connect.CodeNotFound, errors.New("no")), http.StatusNotFound, ""},
		{connect.NewError(connect.CodeAborted, errors.New("stale_version")), http.StatusConflict, "stale_version"},
		{connect.NewError(connect.CodeAborted, errors.New("session_ended")), http.StatusConflict, "session_ended"},
		{connect.NewError(connect.CodeFailedPrecondition, errors.New("live")), http.StatusUnprocessableEntity, "live"},
		{connect.NewError(connect.CodeUnavailable, errors.New("down")), http.StatusServiceUnavailable, "device_offline"},
	}
	for _, c := range cases {
		rec := castCall(t, &fakeCast{err: c.err}, http.MethodPost, "/api/cast/sessions/s1/pause", "")
		if rec.Code != c.want {
			t.Errorf("%v -> %d, want %d", c.err, rec.Code, c.want)
		}
		if c.code != "" && !strings.Contains(rec.Body.String(), `"code":"`+c.code+`"`) {
			t.Errorf("%v -> body %s, want code %s", c.err, rec.Body, c.code)
		}
	}
}

func TestReplacingTheQueueCarriesTheVersion(t *testing.T) {
	cast := &fakeCast{}
	rec := castCall(t, cast, http.MethodPut, "/api/cast/sessions/s1/queue",
		`{"version":7,"items":[{"video_id":"b"},{"video_id":"a"}],"current_index":1}`)
	if rec.Code != http.StatusOK || cast.queue.GetVersion() != 7 || cast.queue.GetCurrentIndex() != 1 ||
		strings.Join(cast.queue.GetVideoIds(), ",") != "b,a" || cast.queue.GetSessionId() != "s1" {
		t.Fatalf("status = %d request = %+v", rec.Code, cast.queue)
	}
}

func TestSeekAndHandoff(t *testing.T) {
	cast := &fakeCast{}
	if rec := castCall(t, cast, http.MethodPost, "/api/cast/sessions/s1/seek", `{"position_s":180}`); rec.Code != http.StatusOK || cast.seek.GetPositionSeconds() != 180 {
		t.Fatalf("seek: %d %+v", rec.Code, cast.seek)
	}
	rec := castCall(t, cast, http.MethodPost, "/api/cast/sessions/s1/handoff", "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"video_id":"abc","position_s":130}` {
		t.Fatalf("handoff: %d %s", rec.Code, rec.Body)
	}
}

func TestABadBodyIsABadRequest(t *testing.T) {
	if rec := castCall(t, &fakeCast{}, http.MethodPost, "/api/cast/sessions", `{`); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestNarrationIsAskedForAndReported(t *testing.T) {
	cast := &fakeCast{}
	rec := castCall(t, cast, http.MethodPost, "/api/cast/sessions",
		`{"device_id":"r1-living","items":[{"video_id":"abc"}],"narration":true}`)
	if rec.Code != http.StatusOK || !cast.start.GetNarration() {
		t.Fatalf("start: status %d, narration asked = %v", rec.Code, cast.start.GetNarration())
	}

	rec = castCall(t, cast, http.MethodPost, "/api/cast/sessions/s1/narration", `{"on":true}`)
	if rec.Code != http.StatusOK || cast.narr.GetSessionId() != "s1" || !cast.narr.GetOn() {
		t.Fatalf("set: status %d request %+v", rec.Code, cast.narr)
	}
	var view map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view["narration"] != true || view["narration_status"] != "preparing" {
		t.Fatalf("view = %v", view)
	}
}
