package timedtext

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

type stub struct {
	calls   int
	tracks  []domain.SubtitleTrack
	refused bool
}

func (s *stub) FetchSubtitles(context.Context, string, string, int32) ([]domain.SubtitleTrack, bool) {
	s.calls++
	return s.tracks, s.refused
}

func chain(cheap, fallback *stub) *First {
	return NewFirst(cheap, nil, fallback, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func chainWithRemote(cheap, remote, fallback *stub) *First {
	return NewFirst(cheap, remote, fallback, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestTheCheapPathIsEnoughWhenItWorks(t *testing.T) {
	cheap := &stub{tracks: []domain.SubtitleTrack{{Language: "vi"}}}
	fallback := &stub{}

	tracks, refused := chain(cheap, fallback).FetchSubtitles(context.Background(), "", "v", 1080)

	if len(tracks) != 1 || refused {
		t.Fatalf("tracks=%+v refused=%v", tracks, refused)
	}
	// yt-dlp costs four hits on the endpoint that is refusing us. It must not
	// run when the one hit already worked.
	if fallback.calls != 0 {
		t.Errorf("fell back %d times, want 0", fallback.calls)
	}
}

// A refusal must not be answered by asking the same endpoint four more times in
// the same minute. That is the retry table's job, minutes later.
func TestARefusalDoesNotFallBack(t *testing.T) {
	cheap := &stub{refused: true}
	fallback := &stub{}

	_, refused := chain(cheap, fallback).FetchSubtitles(context.Background(), "", "v", 1080)

	if !refused {
		t.Error("refusal not passed on")
	}
	if fallback.calls != 0 {
		t.Errorf("fell back %d times into a refusal, want 0", fallback.calls)
	}
}

// The cheap path reads YouTube's own player response, a shape nobody promised
// will stay the same. The day it changes, captions must go on working — yt-dlp
// is a project whose whole business is keeping up with that.
func TestNothingFoundFallsBackToYtDlp(t *testing.T) {
	cheap := &stub{}
	fallback := &stub{tracks: []domain.SubtitleTrack{{Language: "en"}}}

	tracks, _ := chain(cheap, fallback).FetchSubtitles(context.Background(), "", "v", 1080)

	if fallback.calls != 1 {
		t.Fatalf("fell back %d times, want 1", fallback.calls)
	}
	if len(tracks) != 1 || tracks[0].Language != "en" {
		t.Errorf("got %+v, want the fallback's answer", tracks)
	}
}

// The other machine exists for exactly one failure: YouTube refusing this
// address. Measured at thirteen hours straight, while videos played normally —
// both local paths leave by the same front door, so when it is shut they are
// shut together.
func TestARefusalGoesToTheOtherMachine(t *testing.T) {
	cheap := &stub{refused: true}
	remote := &stub{tracks: []domain.SubtitleTrack{{Language: "vi"}}}
	fallback := &stub{}

	tracks, refused := chainWithRemote(cheap, remote, fallback).
		FetchSubtitles(context.Background(), "", "v", 1080)

	if refused {
		t.Error("refused=true although the other machine answered")
	}
	if len(tracks) != 1 || tracks[0].Language != "vi" {
		t.Fatalf("got %+v, want the remote's answer", tracks)
	}
	// yt-dlp leaves by the same door as the path that was just refused.
	if fallback.calls != 0 {
		t.Errorf("fell back %d times, want 0", fallback.calls)
	}
}

// A remote that cannot help must not turn a refusal into something else. The
// video still belongs in the retry queue, and yt-dlp is still the wrong answer.
func TestARemoteThatCannotHelpLeavesTheRefusalStanding(t *testing.T) {
	cheap := &stub{refused: true}
	remote := &stub{}
	fallback := &stub{}

	_, refused := chainWithRemote(cheap, remote, fallback).
		FetchSubtitles(context.Background(), "", "v", 1080)

	if !refused {
		t.Error("refusal lost")
	}
	if fallback.calls != 0 {
		t.Errorf("fell back %d times into a refusal, want 0", fallback.calls)
	}
}

// Nobody has to run a second machine. Unconfigured is the ordinary state and
// must cost nothing.
func TestNoRemoteConfiguredChangesNothing(t *testing.T) {
	cheap := &stub{}
	fallback := &stub{tracks: []domain.SubtitleTrack{{Language: "en"}}}

	tracks, _ := chain(cheap, fallback).FetchSubtitles(context.Background(), "", "v", 1080)

	if len(tracks) != 1 || fallback.calls != 1 {
		t.Errorf("tracks=%+v fallbackCalls=%d", tracks, fallback.calls)
	}
}

// The remote is not asked when the local path already worked: it is a second
// door, not a second opinion.
func TestTheRemoteIsNotAskedWhenTheLocalPathWorked(t *testing.T) {
	cheap := &stub{tracks: []domain.SubtitleTrack{{Language: "vi"}}}
	remote := &stub{}
	fallback := &stub{}

	chainWithRemote(cheap, remote, fallback).FetchSubtitles(context.Background(), "", "v", 1080)

	if remote.calls != 0 || fallback.calls != 0 {
		t.Errorf("remote=%d fallback=%d, want 0 and 0", remote.calls, fallback.calls)
	}
}
