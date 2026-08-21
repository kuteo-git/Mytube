package domain

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

// HLS from YouTube's own files, without muxing anything.
//
// YouTube publishes 720p and 1080p as **adaptive** tracks: one file with only
// picture, one with only sound, and nothing above 360p carrying both. Combining
// them is unavoidable; doing it *here* is not, and doing it here is what every
// hard problem in the player is made of — ffmpeg per playback, a fragmented MP4
// down a pipe, no index in it, so no seeking, so a mark and a lead and a
// handover timed to the second.
//
// The alternative is to describe the two files rather than combine them. Both
// are already fragmented MP4 with a segment index (`sidx`) at the front, which
// is exactly what HLS needs: an initialisation segment and a list of byte
// ranges. A playlist naming those ranges lets the *browser* fetch and combine
// them — and on the device this is for, measured, that costs nothing:
//
//	MediaSource         no
//	ManagedMediaSource  YES
//	HLS natively        maybe   ← "" means no; "maybe" is a yes
//
// So no player library, no ffmpeg, no pipe, and seeking is the browser's own.
// CLAUDE.md §4 chose this for Phase 2 before any of this week's faults: "remux
// `-c copy` into HLS → real ABR at ≈ 0 CPU".
//
// Nothing here fetches. It is given the first bytes of a track and returns what
// can be said about them, so the arithmetic can be tested without a network.

// MediaTrack is one adaptive file as upstream describes it, before anything has
// been read from it.
//
// The codec string is the part that cannot be guessed: a player reads CODECS in
// the playlist to decide whether it can play a stream at all, and decides it
// before fetching a byte.
type MediaTrack struct {
	URL     string
	Codec   string
	Width   int
	Height  int
	Bitrate int
	// Language of the audio, as YouTube tags it ("en-US", "vi"). Empty on video
	// tracks and on anything published without dubs.
	//
	// Carried so the log can say which of twenty-one audio tracks was chosen.
	// It was not carried before, and the consequence was that a video playing in
	// Arabic looked identical from the server side to one playing in English.
	Language string
}

// MediaTracks is what a playlist is built from: the video renditions on offer,
// highest first, and the one audio track they all share.
//
// One audio track, not one per rendition, because it is literally the same
// file — YouTube publishes sound once and pairs it with every height. Saying so
// in the playlist is what lets a player change quality without re-fetching a
// note of it, and it is why the audio is an `EXT-X-MEDIA` group.
//
// `Videos` was a single `Video` while the ladder had one rung. That was
// inherited from the muxed tier, where a second rendition meant a second ffmpeg
// process and the height was chosen once, on the viewer's behalf, to keep
// preparation short. Nothing is prepared here — the browser fetches segments —
// so the choice can be the browser's, which is what HLS is for.
type MediaTracks struct {
	Videos []MediaTrack
	Audio  MediaTrack
}

// Best is the highest rendition on offer, or the zero track when there is none.
func (t MediaTracks) Best() MediaTrack {
	if len(t.Videos) == 0 {
		return MediaTrack{}
	}
	return t.Videos[0]
}

// ErrNoSegmentIndex means the head handed over did not reach the `sidx` box.
//
// Recoverable by the caller, and it must be told apart from a malformed file:
// the answer is to fetch more of the front, not to give up on the track.
var ErrNoSegmentIndex = errors.New("no sidx in the bytes provided")

// Segment is one media segment: a byte range of the track, and how long it
// plays for.
type Segment struct {
	Offset   int64
	Length   int64
	Duration float64
}

// Track is an adaptive rendition, indexed.
//
// InitLength is the initialisation segment, which is everything before the
// segment index — `ftyp` and `moov`. HLS calls it the MAP, and a player must
// have it before any segment means anything.
type Track struct {
	InitLength int64
	Segments   []Segment
}

// Duration is how long the whole track plays for.
func (t Track) Duration() float64 {
	var total float64
	for _, s := range t.Segments {
		total += s.Duration
	}
	return total
}

// IndexTrack reads the front of a fragmented MP4 and reports its initialisation
// segment and every media segment inside it.
//
// The head does not have to be the whole file — only far enough to include the
// segment index, which YouTube places immediately after `moov`. Measured on
// this library: `ftyp`+`moov` ends at 740 bytes and the index at 1408, for a
// 9.8 MB video track. Fetching 64 KiB of the front is therefore generous.
func IndexTrack(head []byte) (Track, error) {
	sidxStart, sidxEnd, err := findBox(head, "sidx")
	if err != nil {
		return Track{}, err
	}

	// Everything before the index is the initialisation segment. Taken as "what
	// comes before sidx" rather than "the moov box", because a file may carry
	// other boxes in front and a player needs all of them.
	segments, err := parseSegmentIndex(head[sidxStart:sidxEnd], sidxEnd)
	if err != nil {
		return Track{}, err
	}
	return Track{InitLength: sidxStart, Segments: segments}, nil
}

// findBox walks the top-level boxes and returns where the named one starts and
// ends. Byte offsets, not indices into a list: everything downstream is a range.
func findBox(data []byte, want string) (start, end int64, err error) {
	var offset int64
	for offset+8 <= int64(len(data)) {
		size := int64(binary.BigEndian.Uint32(data[offset:]))
		typ := string(data[offset+4 : offset+8])
		header := int64(8)

		switch size {
		case 1:
			// 64-bit size, in the eight bytes after the type.
			if offset+16 > int64(len(data)) {
				return 0, 0, ErrNoSegmentIndex
			}
			size = int64(binary.BigEndian.Uint64(data[offset+8:]))
			header = 16
		case 0:
			// Runs to the end of the file, so nothing follows it.
			size = int64(len(data)) - offset
		}
		if size < header {
			return 0, 0, fmt.Errorf("box %q at %d has an impossible size %d", typ, offset, size)
		}

		if typ == want {
			if offset+size > int64(len(data)) {
				// Found it, but it is cut off — the caller has not fetched enough.
				return 0, 0, ErrNoSegmentIndex
			}
			return offset, offset + size, nil
		}
		offset += size
	}
	return 0, 0, ErrNoSegmentIndex
}

// parseSegmentIndex reads a `sidx` box into segments.
//
// Layout is ISO/IEC 14496-12. Each reference gives a size in bytes and a
// duration in the box's own timescale; the segments themselves follow the box,
// one after another, starting at `first_offset` past its end.
func parseSegmentIndex(box []byte, boxEnd int64) ([]Segment, error) {
	const minimum = 8 + 4 + 4 + 4 + 4 + 2 + 2 // header, ids, v0 times, count
	if len(box) < minimum {
		return nil, fmt.Errorf("sidx is %d bytes, too short to read", len(box))
	}

	version := box[8]
	p := 12 // past the box header and the version/flags word

	p += 4 // reference_ID, which is of no use here
	timescale := binary.BigEndian.Uint32(box[p:])
	p += 4
	if timescale == 0 {
		return nil, errors.New("sidx declares a timescale of zero")
	}

	var firstOffset int64
	if version == 0 {
		p += 4 // earliest_presentation_time
		firstOffset = int64(binary.BigEndian.Uint32(box[p:]))
		p += 4
	} else {
		if len(box) < p+16+4 {
			return nil, errors.New("sidx claims 64-bit times but is too short")
		}
		p += 8 // earliest_presentation_time
		firstOffset = int64(binary.BigEndian.Uint64(box[p:]))
		p += 8
	}

	p += 2 // reserved
	if len(box) < p+2 {
		return nil, errors.New("sidx ends before its reference count")
	}
	count := int(binary.BigEndian.Uint16(box[p:]))
	p += 2

	if len(box) < p+count*12 {
		return nil, fmt.Errorf("sidx claims %d references but carries %d bytes", count, len(box)-p)
	}

	// The first segment begins after the index box, which is what makes these
	// ranges absolute rather than relative to anything the caller must remember.
	at := boxEnd + firstOffset
	segments := make([]Segment, 0, count)
	for range count {
		// The top bit is the reference type — 1 means it points at another index
		// rather than at media. Nothing here produces those, and a player asked
		// to treat one as media would fetch nonsense, so they are refused.
		word := binary.BigEndian.Uint32(box[p:])
		if word>>31 == 1 {
			return nil, errors.New("sidx points at a further index, which is not supported")
		}
		size := int64(word & 0x7FFFFFFF)
		duration := float64(binary.BigEndian.Uint32(box[p+4:])) / float64(timescale)
		p += 12

		segments = append(segments, Segment{Offset: at, Length: size, Duration: duration})
		at += size
	}
	if len(segments) == 0 {
		return nil, errors.New("sidx lists no segments")
	}
	return segments, nil
}

// MediaPlaylist renders one track as an HLS media playlist.
//
// Every segment is a byte range of the same URL, which is what `EXT-X-BYTERANGE`
// is for and what makes this possible without cutting the file into pieces on
// disk. The URI is the caller's: these files are signed to the address that
// resolved them (CLAUDE.md §4), so it has to point at something of ours that
// proxies them.
func MediaPlaylist(t Track, uri string) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	// 7 is the version that introduced fMP4 segments; anything lower describes a
	// playlist a player would try to read as MPEG-TS.
	b.WriteString("#EXT-X-VERSION:7\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")

	longest := 0.0
	for _, s := range t.Segments {
		longest = math.Max(longest, s.Duration)
	}
	// Must be at least the longest segment, rounded up, or a player is entitled
	// to consider the playlist invalid.
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", int(math.Ceil(longest)))
	fmt.Fprintf(&b, "#EXT-X-MAP:URI=%q,BYTERANGE=\"%d@0\"\n", uri, t.InitLength)

	for _, s := range t.Segments {
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n", s.Duration)
		fmt.Fprintf(&b, "#EXT-X-BYTERANGE:%d@%d\n", s.Length, s.Offset)
		b.WriteString(uri)
		b.WriteString("\n")
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// Rendition is one video quality offered in the master playlist.
type Rendition struct {
	URI       string
	Codecs    string
	Bandwidth int
	Width     int
	Height    int
}

// MasterPlaylist offers the video renditions with one shared audio track.
//
// The audio is a separate group rather than a copy per rendition because it is
// literally the same file: YouTube publishes one audio track and pairs it with
// every video height. Listing it once is what lets a player change quality
// without re-fetching a note of it.
func MasterPlaylist(video []Rendition, audioURI, audioCodec string) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	fmt.Fprintf(&b,
		"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"Audio\",DEFAULT=YES,AUTOSELECT=YES,URI=%q\n",
		audioURI)

	for _, r := range video {
		codecs := r.Codecs
		if audioCodec != "" {
			codecs += "," + audioCodec
		}
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,CODECS=%q,RESOLUTION=%dx%d,AUDIO=\"audio\"\n",
			r.Bandwidth, codecs, r.Width, r.Height)
		b.WriteString(r.URI)
		b.WriteString("\n")
	}
	return b.String()
}

// ValidCodec reports whether s is usable as an RFC 6381 CODECS value.
//
// A player reads CODECS to decide whether it can play a stream before fetching
// a byte of it, so a value it does not understand is a refusal with no
// diagnosis: no request is made, nothing is logged, and the element reports a
// generic error. On iPhone that is the end of the road — measured 2026-08-20,
// iOS has `ManagedMediaSource` but no `MediaSource`, so hls.js cannot stand
// behind native HLS there the way it can on Chrome.
//
// The value arrives from yt-dlp's `vcodec`/`acodec`, which are not promised to
// be RFC 6381 — "vp9" and "none" are both things it says. So this is checked
// rather than trusted, and a playlist that cannot be written correctly is not
// written at all.
//
// The test is deliberately shallow: a family, a dot, and at least one parameter,
// made only of characters that survive a quoted attribute. Validating the
// profile bits per family would be a codec registry, and getting *that* wrong
// would reject streams that play.
func ValidCodec(s string) bool {
	family, params, found := strings.Cut(s, ".")
	if !found || family == "" || params == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_':
		default:
			// Anything else — a space, a comma, a quote — either ends the
			// attribute early or breaks out of it.
			return false
		}
	}
	return true
}
