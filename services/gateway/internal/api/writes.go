package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	recsysv1 "github.com/lucnguyen/local-youtube/gen/go/recsys/v1"
)

// Write paths fan out to two services: catalog owns the product state the UI
// reads back, recsys owns its own copy as ranking input. The gateway is the
// only component that knows both need telling.

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func (g *Gateway) recordSignal(userID string, signalType recsysv1.SignalType, videoID, query string, fraction float32) {
	ctx, cancel := contextWithTimeout(5 * time.Second)
	defer cancel()

	if _, err := g.recsys.RecordSignal(ctx, connect.NewRequest(&recsysv1.RecordSignalRequest{
		UserId:          userID,
		Type:            signalType,
		VideoId:         videoID,
		Query:           query,
		WatchedFraction: fraction,
		OccurredAt:      timestamppb.Now(),
	})); err != nil {
		// A lost signal degrades ranking slightly; it must never fail the user
		// action that produced it.
		g.logger.Warn("record signal", "type", signalType.String(), "error", err)
	}
}

type progressRequest struct {
	PositionSeconds int32   `json:"positionSeconds"`
	WatchedFraction float32 `json:"watchedFraction"`
}

func (g *Gateway) handleProgress(w http.ResponseWriter, r *http.Request) {
	var body progressRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	userID := g.userID(r)
	videoID := r.PathValue("id")

	if _, err := g.catalog.RecordWatchProgress(r.Context(), connect.NewRequest(&catalogv1.RecordWatchProgressRequest{
		UserId:          userID,
		VideoId:         videoID,
		PositionSeconds: body.PositionSeconds,
		WatchedFraction: body.WatchedFraction,
	})); err != nil {
		g.writeErr(w, r, err)
		return
	}

	go g.recordSignal(userID, recsysv1.SignalType_SIGNAL_TYPE_WATCH, videoID, "", body.WatchedFraction)
	w.WriteHeader(http.StatusNoContent)
}

type reactionRequest struct {
	// One of LIKE, DISLIKE or NONE.
	Reaction string `json:"reaction"`
}

func (g *Gateway) handleReaction(w http.ResponseWriter, r *http.Request) {
	var body reactionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	reaction := catalogv1.Reaction_REACTION_NONE
	signalType := recsysv1.SignalType_SIGNAL_TYPE_UNSPECIFIED
	switch body.Reaction {
	case "LIKE":
		reaction, signalType = catalogv1.Reaction_REACTION_LIKE, recsysv1.SignalType_SIGNAL_TYPE_LIKE
	case "DISLIKE":
		reaction, signalType = catalogv1.Reaction_REACTION_DISLIKE, recsysv1.SignalType_SIGNAL_TYPE_DISLIKE
	case "NONE", "":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown reaction"})
		return
	}

	userID := g.userID(r)
	videoID := r.PathValue("id")

	resp, err := g.catalog.SetReaction(r.Context(), connect.NewRequest(&catalogv1.SetReactionRequest{
		UserId:   userID,
		VideoId:  videoID,
		Reaction: reaction,
	}))
	// Send the signal to recsys regardless of whether the video exists in the
	// catalog. A viewer saying "not interested" on a suggested video must still
	// hide it from the feed — the signal is what the ranker reads, and it does
	// not need the video to be in the library.
	if signalType != recsysv1.SignalType_SIGNAL_TYPE_UNSPECIFIED {
		go g.recordSignal(userID, signalType, videoID, "", 0)
	}

	if err != nil {
		// A reaction on a video not yet in the library is not a client error.
		// The signal above was already sent; acknowledge success.
		if connect.CodeOf(err) == connect.CodeNotFound {
			writeJSON(w, http.StatusOK, map[string]int64{"likeCount": 0})
			return
		}
		g.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"likeCount": resp.Msg.GetLikeCount()})
}

type createCommentRequest struct {
	Text            string  `json:"text"`
	ParentCommentID *string `json:"parentCommentId,omitempty"`
}

func (g *Gateway) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	var body createCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	resp, err := g.catalog.CreateComment(r.Context(), connect.NewRequest(&catalogv1.CreateCommentRequest{
		VideoId:         r.PathValue("id"),
		UserId:          g.userID(r),
		Text:            body.Text,
		ParentCommentId: body.ParentCommentID,
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toCommentDTO(resp.Msg.GetComment()))
}
