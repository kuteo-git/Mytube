package ytdlp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/adapter/proxycfg"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// ResolveTracks picks the two adaptive files an HLS playlist describes: one
// carrying only picture, one carrying only sound.
//
// Separate from ResolveRemuxURLs, which answers the same question for ffmpeg
// and needs nothing but URLs. A playlist has to *describe* what it points at —
// the codec string above all, because a player decides whether it can play a
// stream by reading CODECS before fetching a byte of it.
//
// Verified before being handed over, for the same reason and by the same means
// as every other resolve here (verify.go).
func (d *Downloader) ResolveTracks(ctx context.Context, videoURL string, height int32) (domain.MediaTracks, error) {
	if height <= 0 {
		height = 1080
	}

	var lastErr error
	for range resolveAttempts {
		tracks, err := d.resolveTracksOnce(ctx, videoURL, height)
		if err != nil {
			return domain.MediaTracks{}, err
		}
		lastErr = nil
		// Every rendition is verified, not just the best one. A ladder whose
		// second rung is dead is worse than one rung: the player switches down
		// when the network dips — exactly when it can least afford a stall —
		// and lands on a URL that was never going to answer.
		//
		// Concurrently, because this sits in front of the viewer. Each probe is
		// a bounded 1 MiB request and the ladder is up to `maxRenditions` deep,
		// so run in turn they were most of the wait before the first frame — for
		// answers that have nothing to say to each other. The guarantee is
		// unchanged: every URL is still probed and one refusal still fails the
		// whole attempt. Only the wall clock moves, from the sum to the slowest.
		urls := []string{tracks.Audio.URL}
		for _, v := range tracks.Videos {
			urls = append(urls, v.URL)
		}
		group, probeCtx := errgroup.WithContext(ctx)
		for _, u := range urls {
			group.Go(func() error { return verifyURL(probeCtx, u) })
		}
		// The first refusal cancels the rest through the group's context, which
		// keeps this from spending seven requests to learn what one of them has
		// already answered.
		lastErr = group.Wait()
		if lastErr == nil {
			return tracks, nil
		}
		if ctx.Err() != nil {
			return domain.MediaTracks{}, ctx.Err()
		}
	}
	return domain.MediaTracks{}, fmt.Errorf("resolve tracks %q: every resolved url was refused: %w", videoURL, lastErr)
}

func (d *Downloader) resolveTracksOnce(ctx context.Context, videoURL string, height int32) (domain.MediaTracks, error) {
	result, err := newCommand(purposeMedia, proxycfg.Media).
		SkipDownload().
		NoPlaylist().
		DumpJSON().
		NoWarnings().
		Run(ctx, videoURL)
	if err != nil {
		return domain.MediaTracks{}, fmt.Errorf("resolve tracks %q: %w", videoURL, err)
	}
	infos, err := result.GetExtractedInfo()
	if err != nil {
		return domain.MediaTracks{}, err
	}
	if len(infos) == 0 {
		return domain.MediaTracks{}, domain.ErrNotFound
	}

	// One entry per height, keeping the best bitrate at each. YouTube publishes
	// several encodings of the same height and a ladder wants one rung apiece.
	byHeight := map[int]domain.MediaTrack{}
	var audio domain.MediaTrack
	// What the chosen audio is, kept beside the track itself because the choice
	// depends on things a MediaTrack does not carry — the language, and whether
	// YouTube calls it the original.
	var chosenAudio audioCandidate
	seen := formatTally{}
	for _, f := range infos[0].Formats {
		seen.total++
		if f.URL == "" {
			seen.noURL++
			continue
		}
		if protocol := deref(f.Protocol); protocol != "https" && protocol != "http" {
			seen.notDirect++
			continue
		}
		vcodec, acodec := deref(f.VCodec), deref(f.ACodec)
		hasVideo := vcodec != "" && vcodec != "none"
		hasAudio := acodec != "" && acodec != "none"

		switch {
		case hasVideo && !hasAudio:
			// mp4 only, which in practice means H.264 up to 1080p and AV1 above
			// it.
			//
			// This used to say "H.264 only" and never checked a codec — the
			// container test was doing all the work, and AV1 in mp4 passed it
			// silently. That was harmless while the ceiling was 1080p, where
			// YouTube publishes avc1; it stops being harmless now the ceiling is
			// 2160p, because **YouTube publishes no H.264 above 1080p at all**.
			// Measured on a real 4K upload: 1440p and 2160p exist as vp9 and
			// av01 and nothing else.
			//
			// VP9 needs no rule of its own. YouTube ships it as webm over https
			// (excluded here) or as vp09 in mp4 over m3u8 (excluded by the
			// https test above), so the container filter removes it without
			// being asked to.
			seen.videoOnly++
			if deref(f.Extension) != "mp4" {
				seen.videoWrongExt++
				continue
			}
			height32 := int32(deref(f.Height))
			if !usableRenditionHeight(height32, height) {
				// One rule, asked once; the tally only reports which side of it
				// this format fell off, because that is the difference between
				// "the ceiling is too low" and "this video publishes nothing
				// worth watching".
				if height32 > height {
					seen.videoTooTall++
				} else {
					seen.videoTooShort++
				}
				continue
			}
			h := int(height32)
			candidate := domain.MediaTrack{
				URL:    f.URL,
				Codec:  vcodec,
				Width:  int(deref(f.Width)),
				Height: h,
				// TBR is the format's own bitrate in kbit/s; the playlist wants
				// bits, and BANDWIDTH is what a player budgets with.
				Bitrate: int(deref(f.TBR) * 1000),
			}
			if existing, ok := byHeight[h]; !ok || preferRendition(existing, candidate) {
				byHeight[h] = candidate
			}
		case hasAudio && !hasVideo:
			seen.audioOnly++
			if deref(f.Extension) != "m4a" {
				seen.audioWrongExt++
				continue
			}
			candidate := audioCandidate{
				Language:     deref(f.Language),
				LanguagePref: deref(f.LanguagePreference),
				Bitrate:      int(deref(f.TBR) * 1000),
			}
			// "Nothing chosen yet" is asked explicitly rather than inferred from
			// a zero-valued candidate. Inferring it is a bug this had: the empty
			// candidate carries LanguagePreference 0, and a video with no dubs
			// at all reports **-1** — so every real track lost to the emptiness
			// it was being compared against, no audio was ever chosen, and the
			// video resolved to "no directly playable format available" while
			// publishing two perfectly good ones.
			if audio.URL == "" || preferAudio(chosenAudio, candidate) {
				chosenAudio = candidate
				audio = domain.MediaTrack{
					URL: f.URL, Codec: acodec, Bitrate: candidate.Bitrate,
					Language: candidate.Language,
				}
			}
		}
	}

	if len(byHeight) == 0 || audio.URL == "" {
		// Say what was on offer, not just that nothing suited.
		//
		// "no directly playable format available" is true and useless: it looks
		// identical whether YouTube published nothing, published only formats
		// behind a manifest, or published exactly what was wanted and a filter
		// here rejected it. That third case is the one worth catching, and the
		// only way to tell it apart afterwards is to have written the tally
		// down at the moment of the refusal.
		return domain.MediaTracks{}, fmt.Errorf("%w (saw %s)", domain.ErrNoProgressiveFormat, seen)
	}

	videos := make([]domain.MediaTrack, 0, len(byHeight))
	for _, v := range byHeight {
		videos = append(videos, v)
	}
	// Highest first, which is the order a master playlist wants and the order
	// `Best()` reads.
	sort.Slice(videos, func(i, j int) bool { return videos[i].Height > videos[j].Height })
	// Bounded. YouTube publishes as many as seven heights and every extra rung
	// is a URL to verify on every resolve — against the address §8 risk 6
	// counts — for a rung nobody on a LAN will ever drop to.
	if len(videos) > maxRenditions {
		videos = videos[:maxRenditions]
	}
	return domain.MediaTracks{Videos: videos, Audio: audio}, nil
}

// How many rungs the ladder may have.
//
// Seven, so a video that publishes them all offers 240 · 360 · 480 · 720 ·
// 1080 · 1440 · 2160.
//
// It was three, on the reasoning that "on a LAN the moves that matter are the
// first two". The mistake in that is worth keeping written down: the bottleneck
// is not the LAN. It is the road from googlevideo to this gateway, which §4 has
// measured refusing this address in waves and which nothing here controls. With
// three rungs the floor was 480p, so on a bad minute there was nowhere to go but
// stop — and stopping is the one outcome a ladder exists to avoid.
//
// The cost is one URL to verify per rung on every resolve, against the address
// §8 risk 6 counts. Since those probes now run concurrently (ResolveTracks) the
// extra rungs cost no extra wall clock — only requests.
const maxRenditions = 7

// The lowest rung worth offering.
//
// 240 rather than 144, and it is a decision rather than an accident of what
// YouTube publishes. CLAUDE.md §7 cuts 144p for good — "nobody watches 144p" —
// and it is the one rung where closing the tab beats watching what arrives.
//
// So it must be absent rather than merely last. An automatic ladder reaches
// anything it can reach, and on a bad minute it would: the viewer would then be
// looking at 144p instead of at the pause that would have told them something
// was wrong.
const minRenditionHeight = 240

// usableRenditionHeight reports whether a rung of this height belongs on the
// ladder at all, given the ceiling asked for.
//
// A predicate rather than two inline comparisons, so the floor and the ceiling
// can be asserted without a yt-dlp process between the test and the rule.
func usableRenditionHeight(h, ceiling int32) bool {
	return h <= ceiling && h >= minRenditionHeight
}

// preferRendition decides between two encodings of the same height.
//
// The old rule was "keep the best bitrate", and it was written when every
// candidate at a height was H.264 — comparing two encodings of one codec, where
// more bits is more picture. That stopped being true the moment the ceiling rose
// past 1080p and AV1 joined the ladder: at 1080p YouTube publishes both, and on
// one measured video avc1 carries 3358k against av01's 1619k for the same
// picture. Bitrate across codecs is not a quality comparison at all — AV1 at
// half the bits looks the same, which is the entire point of AV1.
//
// So compatibility decides first and bitrate only breaks ties within a codec.
// H.264 plays on every device in this house and every television this is aimed
// at; AV1 does not, and is here only because above 1080p there is nothing else.
// The effect is that 1080p and below are exactly what they were before this
// change, and AV1 appears only where it is the only thing on offer.
func preferRendition(current, next domain.MediaTrack) bool {
	currentH264 := isH264(current.Codec)
	nextH264 := isH264(next.Codec)
	if currentH264 != nextH264 {
		return nextH264
	}
	return next.Bitrate > current.Bitrate
}

func isH264(codec string) bool {
	return strings.HasPrefix(codec, "avc1") || strings.HasPrefix(codec, "avc3")
}

// audioCandidate is everything choosing between two audio tracks depends on.
//
// Its own type, and comparable by a pure function, because the choice turned
// out to be wrong in a way no amount of reading the loop would have shown:
// see tracks_audio_test.go for the twenty-one-way tie it lost.
type audioCandidate struct {
	Language     string
	LanguagePref int
	Bitrate      int
}

// The languages this household would rather hear, best first.
//
// Vietnamese ahead of English because a real dub is what the read-aloud feature
// is an imitation of: this app already translates subtitles and speaks them in
// a synthetic voice, and a track YouTube dubbed is the same idea done upstream.
// English next, because it is what the library is mostly in and what everyone
// here follows. Only then the original, whatever it happens to be.
//
// Primary subtag only, the same rule as CLAUDE.md §6: "vi-VN" and "vi" are one
// language, "en-US" and "en" are one language.
var preferredAudioLanguages = []string{"vi", "en"}

// audioRank scores a track against what this household wants to hear.
//
// Higher is better, and the ordering is the only thing this encodes: a
// preferred language beats the original, and the original beats a dub into a
// language nobody here reads.
func audioRank(c audioCandidate) int {
	primary := c.Language
	if i := strings.IndexAny(primary, "-_"); i > 0 {
		primary = primary[:i]
	}
	primary = strings.ToLower(primary)

	for i, want := range preferredAudioLanguages {
		if primary == want {
			return len(preferredAudioLanguages) - i + 1
		}
	}
	// YouTube marks the original "original (default)" with a preference of 10;
	// every dub is -1. Absent — a video with no dubs at all — is 0, and that is
	// the ordinary case rather than a bad one.
	if c.LanguagePref >= 10 {
		return 1
	}
	return 0
}

// preferAudio reports whether `next` should displace `current`.
//
// Language first, then how sure we are it is the original, then bitrate.
//
// YouTube auto-dubs and publishes every dub as its own audio-only format
// alongside the original — twenty-one of them on one video of this library, all
// at an identical bitrate — so a rule that looks only at bitrate is decided by
// the order the formats happen to arrive in. That order begins at Arabic and
// ends at the original, which is exactly how an English video came out in
// Arabic.
func preferAudio(current, next audioCandidate) bool {
	if r, c := audioRank(next), audioRank(current); r != c {
		return r > c
	}
	if next.LanguagePref != current.LanguagePref {
		return next.LanguagePref > current.LanguagePref
	}
	return next.Bitrate > current.Bitrate
}

// formatTally is what a resolve saw, for the error it writes when it found
// nothing usable.
type formatTally struct {
	total         int
	noURL         int
	notDirect     int
	videoOnly     int
	videoWrongExt int
	videoTooTall  int
	videoTooShort int
	audioOnly     int
	audioWrongExt int
}

func (t formatTally) String() string {
	return fmt.Sprintf(
		"%d formats: %d without a url, %d behind a manifest; "+
			"video-only %d (%d wrong container, %d too tall, %d too short); "+
			"audio-only %d (%d wrong container)",
		t.total, t.noURL, t.notDirect,
		t.videoOnly, t.videoWrongExt, t.videoTooTall, t.videoTooShort,
		t.audioOnly, t.audioWrongExt)
}
