package ytdlp

import "testing"

// Which of several audio tracks a playlist should carry.
//
// YouTube auto-dubs, and it publishes every dub as a separate audio-only format
// beside the original. Measured on `VeU6gScy92s` (2026-08-21): **twenty-one**
// audio tracks at itag 140, all at exactly 129.5 kbit/s — Arabic, Bangla,
// German, Spanish, French, Hindi, Indonesian, Italian, Hebrew, Japanese,
// Korean, Malayalam, Dutch, Punjabi, Polish, Portuguese, Russian, Tamil,
// Telugu, Ukrainian, and last of all the English original.
//
// The rule this replaces was "the m4a with the highest bitrate", which on that
// video is a twenty-one-way tie decided by list order. The list begins at
// Arabic. So an English video played in Arabic, and it was not a near miss —
// the original was the *last* candidate considered.
//
// yt-dlp marks the original with `language_preference: 10`; every dub is -1.
// That is the only field that separates them, since the bitrates are identical.
//
// What this household wants, in order: a Vietnamese dub if YouTube made one,
// then English, then whatever the original is. Vietnamese first because a real
// dub is what this app's read-aloud feature is an imitation of — it translates
// subtitles and speaks them in a synthetic voice, and a track YouTube dubbed is
// the same idea done upstream and done better.
//
// Note that neither the download nor the muxed tier had this fault: both ask
// yt-dlp for `bestaudio`, and yt-dlp's own ordering puts language preference
// first. Only the hand-rolled loop that builds the HLS playlist chose by hand,
// and it did not know to ask.
func TestPreferAudioTakesTheOriginalOverEveryDub(t *testing.T) {
	original := audioCandidate{Language: "en-US", LanguagePref: 10, Bitrate: 129_500}
	arabicDub := audioCandidate{Language: "ar", LanguagePref: -1, Bitrate: 129_500}

	if !preferAudio(arabicDub, original) {
		t.Error("the original must displace a dub at the same bitrate")
	}
	if preferAudio(original, arabicDub) {
		t.Error("a dub must never displace the original")
	}
}

// A louder dub is still a dub. This is the case the old rule would have got
// wrong even with a tie-break added afterwards: the dubs at itag 251 range from
// 111.7 up to 120.7 kbit/s, and choosing by bitrate alone picks Tamil.
func TestPreferAudioKeepsTheOriginalEvenWhenADubHasTheHigherBitrate(t *testing.T) {
	original := audioCandidate{Language: "en-US", LanguagePref: 10, Bitrate: 100_000}
	tamilDub := audioCandidate{Language: "ta", LanguagePref: -1, Bitrate: 120_700}

	if preferAudio(original, tamilDub) {
		t.Error("a louder dub displaced the original")
	}
}

// Among equals — every dub, or a video with no dubs at all — the bitrate
// decides, which is what the original rule was for and is still right.
func TestPreferAudioFallsBackToBitrateWhenNothingSeparatesTheLanguages(t *testing.T) {
	quiet := audioCandidate{Language: "en", LanguagePref: 10, Bitrate: 48_800}
	loud := audioCandidate{Language: "en", LanguagePref: 10, Bitrate: 129_500}

	if !preferAudio(quiet, loud) {
		t.Error("the better encoding of the same track should win")
	}
	if preferAudio(loud, quiet) {
		t.Error("bitrate comparison is the wrong way round")
	}
}

// A video with no dubs reports -1, and -1 must not lose to an empty candidate.
//
// This is the case the first version of these tests guessed at and got wrong.
// It assumed `language_preference` would be absent on a video without dubs —
// `deref` turning that into 0 — and asserted 0 beats the empty candidate, which
// it does. Measured on `6JSXvUV4Uns` (2026-08-21), YouTube reports **-1**:
//
//	itag 139 | language: None | language_preference: -1
//	itag 140 | language: None | language_preference: -1
//
// Against an empty candidate holding 0, every real track lost. No audio was
// chosen, and a video publishing twelve usable video formats and two usable
// audio ones resolved to "no directly playable format available".
//
// So the caller asks "have I chosen anything yet" outright, and this records
// why comparing against emptiness is not the same question.
func TestPreferAudioDoesNotRankARealTrackBelowAnEmptyOne(t *testing.T) {
	nothing := audioCandidate{}
	realTrack := audioCandidate{Language: "", LanguagePref: -1, Bitrate: 129_482}

	// Documenting the trap rather than the fix: this comparison is *false*, and
	// that is correct — -1 really is less than 0. The bug was asking it at all.
	if preferAudio(nothing, realTrack) {
		t.Error("preferAudio changed shape; the caller no longer needs its own emptiness check")
	}
}

// A Vietnamese dub is what this household would rather hear, even over the
// original — which is the whole point of the ordering.
func TestPreferAudioTakesTheVietnameseDubAheadOfEverything(t *testing.T) {
	original := audioCandidate{Language: "en-US", LanguagePref: 10, Bitrate: 129_500}
	vietnamese := audioCandidate{Language: "vi", LanguagePref: -1, Bitrate: 129_500}

	if !preferAudio(original, vietnamese) {
		t.Error("a Vietnamese dub must displace the original")
	}
	if preferAudio(vietnamese, original) {
		t.Error("the original displaced the Vietnamese dub")
	}
}

// English comes next, and beats the original when the original is neither.
func TestPreferAudioTakesEnglishOverAnOriginalInAnotherLanguage(t *testing.T) {
	japaneseOriginal := audioCandidate{Language: "ja", LanguagePref: 10, Bitrate: 129_500}
	englishDub := audioCandidate{Language: "en-US", LanguagePref: -1, Bitrate: 129_500}
	vietnameseDub := audioCandidate{Language: "vi-VN", LanguagePref: -1, Bitrate: 129_500}

	if !preferAudio(japaneseOriginal, englishDub) {
		t.Error("English should be preferred to an original nobody here follows")
	}
	// And Vietnamese still outranks English.
	if !preferAudio(englishDub, vietnameseDub) {
		t.Error("Vietnamese should outrank English")
	}
}

// Region subtags do not make a different language, the same rule as §6.
func TestPreferAudioReadsOnlyThePrimarySubtag(t *testing.T) {
	dub := audioCandidate{Language: "ar", LanguagePref: -1, Bitrate: 200_000}
	for _, tag := range []string{"vi", "vi-VN", "VI"} {
		if !preferAudio(dub, audioCandidate{Language: tag, LanguagePref: -1, Bitrate: 1}) {
			t.Errorf("%q was not recognised as Vietnamese", tag)
		}
	}
}
