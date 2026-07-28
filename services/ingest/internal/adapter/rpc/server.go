// Package rpc adapts the ingest use cases to ConnectRPC.
package rpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/usecase"
)

type Server struct {
	ingest  *usecase.Ingest
	scanner *usecase.Scanner
}

func NewServer(ingest *usecase.Ingest, scanner *usecase.Scanner) *Server {
	return &Server{ingest: ingest, scanner: scanner}
}

func videoToProto(v domain.ExternalVideo) *ingestv1.ExternalVideo {
	return &ingestv1.ExternalVideo{
		Id:              v.ID,
		Title:           v.Title,
		ChannelId:       v.ChannelID,
		ChannelName:     v.ChannelName,
		DurationSeconds: v.DurationSeconds,
		ViewCount:       v.ViewCount,
		ThumbnailUrl:    v.ThumbnailURL,
		SourceUrl:       v.SourceURL,
		PublishedAt:     timestamppb.New(v.PublishedAt),
		Description:     v.Description,
		Topics:          v.Topics,
		Hashtags:        v.Hashtags,
		InLibrary:       v.InLibrary,
	}
}

func (s *Server) Search(ctx context.Context, req *connect.Request[ingestv1.SearchRequest]) (*connect.Response[ingestv1.SearchResponse], error) {
	videos, err := s.ingest.Search(ctx, req.Msg.GetQuery(), req.Msg.GetLimit())
	if err != nil {
		return nil, toConnectErr(err)
	}

	out := make([]*ingestv1.ExternalVideo, 0, len(videos))
	for _, v := range videos {
		out = append(out, videoToProto(v))
	}
	return connect.NewResponse(&ingestv1.SearchResponse{Videos: out}), nil
}

func (s *Server) EnsureVideo(ctx context.Context, req *connect.Request[ingestv1.EnsureVideoRequest]) (*connect.Response[ingestv1.EnsureVideoResponse], error) {
	videoID, err := s.ingest.EnsureVideo(ctx, req.Msg.GetUrl())
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&ingestv1.EnsureVideoResponse{VideoId: videoID}), nil
}

func scanToProto(r domain.ScanResult) *ingestv1.ScanStatus {
	return &ingestv1.ScanStatus{
		StartedAt:      timestamppb.New(r.StartedAt),
		DurationMs:     r.Duration.Milliseconds(),
		SourcesScanned: r.SourcesScanned,
		SourcesFailed:  r.SourcesFailed,
		VideosSeen:     int32(r.VideosSeen),
		VideosAdded:    int32(r.VideosAdded),
		Errors:         r.Errors,
	}
}

func (s *Server) Refresh(ctx context.Context, _ *connect.Request[ingestv1.RefreshRequest]) (*connect.Response[ingestv1.RefreshResponse], error) {
	result, err := s.scanner.ScanNow(ctx)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&ingestv1.RefreshResponse{Status: scanToProto(result)}), nil
}

func (s *Server) GetScanStatus(_ context.Context, _ *connect.Request[ingestv1.GetScanStatusRequest]) (*connect.Response[ingestv1.GetScanStatusResponse], error) {
	return connect.NewResponse(&ingestv1.GetScanStatusResponse{
		Status: scanToProto(s.scanner.LastScan()),
	}), nil
}

func toConnectErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, domain.ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, domain.ErrNoProgressiveFormat):
		// Not a failure of this service: upstream simply has nothing a bare
		// video element can play, so the client should wait for the download.
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

var jobStates = map[domain.JobState]ingestv1.JobState{
	domain.JobQueued:    ingestv1.JobState_JOB_STATE_QUEUED,
	domain.JobRunning:   ingestv1.JobState_JOB_STATE_RUNNING,
	domain.JobSucceeded: ingestv1.JobState_JOB_STATE_SUCCEEDED,
	domain.JobFailed:    ingestv1.JobState_JOB_STATE_FAILED,
	domain.JobCancelled: ingestv1.JobState_JOB_STATE_CANCELLED,
}

func jobToProto(j domain.Job) *ingestv1.Job {
	job := &ingestv1.Job{
		Id:              j.ID,
		SourceUrl:       j.SourceURL,
		VideoId:         j.VideoID,
		Title:           j.Title,
		State:           jobStates[j.State],
		Progress:        j.Progress,
		DownloadedBytes: j.DownloadedBytes,
		TotalBytes:      j.TotalBytes,
		ErrorMessage:    j.ErrorMessage,
		Attempts:        j.Attempts,
		CreatedAt:       timestamppb.New(j.CreatedAt),
	}
	if j.FinishedAt != nil {
		job.FinishedAt = timestamppb.New(*j.FinishedAt)
	}
	return job
}

func (s *Server) ResolveStream(ctx context.Context, req *connect.Request[ingestv1.ResolveStreamRequest]) (*connect.Response[ingestv1.ResolveStreamResponse], error) {
	location, err := s.ingest.ResolveStream(ctx, req.Msg.GetVideoId())
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&ingestv1.ResolveStreamResponse{
		Url:       location.URL,
		Height:    location.Height,
		MimeType:  location.MimeType,
		ExpiresAt: timestamppb.New(location.ExpiresAt),
	}), nil
}

func (s *Server) Submit(ctx context.Context, req *connect.Request[ingestv1.SubmitRequest]) (*connect.Response[ingestv1.SubmitResponse], error) {
	job, err := s.ingest.Submit(ctx, req.Msg.GetUrl(), req.Msg.GetRequestedBy(), req.Msg.GetPreferredHeight())
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&ingestv1.SubmitResponse{Job: jobToProto(job)}), nil
}

func (s *Server) GetJob(ctx context.Context, req *connect.Request[ingestv1.GetJobRequest]) (*connect.Response[ingestv1.GetJobResponse], error) {
	job, err := s.ingest.GetJob(ctx, req.Msg.GetJobId())
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&ingestv1.GetJobResponse{Job: jobToProto(job)}), nil
}

func (s *Server) ListJobs(ctx context.Context, req *connect.Request[ingestv1.ListJobsRequest]) (*connect.Response[ingestv1.ListJobsResponse], error) {
	jobs, err := s.ingest.ListJobs(ctx, req.Msg.GetActiveOnly(), req.Msg.GetLimit())
	if err != nil {
		return nil, toConnectErr(err)
	}
	out := make([]*ingestv1.Job, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobToProto(j))
	}
	return connect.NewResponse(&ingestv1.ListJobsResponse{Jobs: out}), nil
}

func (s *Server) CancelJob(ctx context.Context, req *connect.Request[ingestv1.CancelJobRequest]) (*connect.Response[ingestv1.CancelJobResponse], error) {
	if err := s.ingest.CancelJob(ctx, req.Msg.GetJobId()); err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&ingestv1.CancelJobResponse{}), nil
}
