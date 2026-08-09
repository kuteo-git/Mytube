package api

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func rankingGateway(t *testing.T) *Gateway {
	t.Helper()
	return &Gateway{
		configDir: t.TempDir(),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// A household that has never opened the advanced screen sends nothing, and
// recsys answers with its built-in constants. Anything else would mean
// installing the setting changed the feed.
func TestNoRankingFileMeansNoOverrides(t *testing.T) {
	got := rankingGateway(t).loadRanking()
	if got != (rankingConfig{}) {
		t.Fatalf("a missing file produced %+v, want no overrides", got)
	}
	proto := got.toProto()
	if proto.SessionBlend != nil || proto.MaxPublishedAgeDays != nil {
		t.Fatal("an empty config put values on the wire; recsys would read them " +
			"as settings rather than as silence")
	}
}

// The file is meant to be edited by hand, which is precisely why a broken one
// must not take the feed with it.
func TestABrokenRankingFileFallsBackToTheBuiltInValues(t *testing.T) {
	g := rankingGateway(t)
	if err := os.WriteFile(g.rankingPath(), []byte("{sessionBlend: 0.5,"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := g.loadRanking(); got != (rankingConfig{}) {
		t.Fatalf("a corrupt file produced %+v, want no overrides", got)
	}
}

// Zero has to survive the round trip as a set value. A blend of zero is a real
// instruction — ignore what I am watching right now — and `omitempty` on a bare
// float would have quietly dropped it.
func TestZeroSurvivesBeingSaved(t *testing.T) {
	g := rankingGateway(t)
	zero := 0.0
	blob := []byte(`{"sessionBlend":0}`)
	if err := os.WriteFile(g.rankingPath(), blob, 0o644); err != nil {
		t.Fatal(err)
	}

	got := g.loadRanking()
	if got.SessionBlend == nil {
		t.Fatal("a session blend of zero was read as unset")
	}
	if *got.SessionBlend != zero {
		t.Fatalf("session blend read back as %v", *got.SessionBlend)
	}
	if got.SoftmaxTemperature != nil {
		t.Fatal("a field the file never mentioned came back set")
	}
}

// The settings page works out what a slider is a percentage *of* from this. It
// carried its own copy once and spent a release quoting a number that was true
// before the fresh-subscribed share existed.
func TestFixedSharesFollowTheRankingConfig(t *testing.T) {
	g := rankingGateway(t)

	base := g.fixedShares()
	if base["freshSubscribed"] != defaultFreshSubscribedPercent {
		t.Fatalf("fresh share reported as %d with no config", base["freshSubscribed"])
	}

	if err := os.WriteFile(g.rankingPath(), []byte(`{"freshSubscribedPercent":25}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := g.fixedShares()["freshSubscribed"]; got != 25 {
		t.Fatalf("fresh share reported as %d after being set to 25", got)
	}
	// The other two are not settings and must not move with it.
	if g.fixedShares()["rewatch"] != shareRewatchPercent {
		t.Fatal("the rewatch share moved")
	}
}

func TestRankingConfigSurvivesARoundTripThroughDisk(t *testing.T) {
	g := rankingGateway(t)
	temp := 0.25
	pool := 96
	written := rankingConfig{SoftmaxTemperature: &temp, SamplePoolSize: &pool}

	blob := []byte(`{"softmaxTemperature":0.25,"samplePoolSize":96}`)
	if err := os.WriteFile(filepath.Join(g.configDir, "ranking.json"), blob, 0o644); err != nil {
		t.Fatal(err)
	}

	got := g.loadRanking()
	if got.SoftmaxTemperature == nil || *got.SoftmaxTemperature != *written.SoftmaxTemperature {
		t.Fatalf("temperature came back as %v", got.SoftmaxTemperature)
	}
	if got.SamplePoolSize == nil || *got.SamplePoolSize != *written.SamplePoolSize {
		t.Fatalf("sample pool came back as %v", got.SamplePoolSize)
	}
}
