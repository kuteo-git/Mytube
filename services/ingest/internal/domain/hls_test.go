package domain

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// box builds one MP4 box, so these tests describe a file rather than carry one.
func box(typ string, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(b, uint32(8+len(payload)))
	copy(b[4:], typ)
	copy(b[8:], payload)
	return b
}

func u32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

func u16(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

// sidxV0 builds a version-0 segment index over the given (size, duration) pairs,
// with the durations expressed in `timescale` units.
func sidxV0(timescale uint32, firstOffset uint32, refs [][2]uint32) []byte {
	var p []byte
	p = append(p, u32(0)...)           // version 0, no flags
	p = append(p, u32(1)...)           // reference_ID
	p = append(p, u32(timescale)...)   // timescale
	p = append(p, u32(0)...)           // earliest_presentation_time
	p = append(p, u32(firstOffset)...) // first_offset
	p = append(p, u16(0)...)           // reserved
	p = append(p, u16(uint16(len(refs)))...)
	for _, r := range refs {
		p = append(p, u32(r[0])...) // top bit clear: this is media
		p = append(p, u32(r[1])...) // subsegment_duration
		p = append(p, u32(0)...)    // SAP fields, unread here
	}
	return box("sidx", p)
}

// A track as YouTube serves one: ftyp, moov, then the index, then the segments.
func track(refs [][2]uint32) []byte {
	head := append(box("ftyp", make([]byte, 16)), box("moov", make([]byte, 100))...)
	return append(head, sidxV0(1000, 0, refs)...)
}

func TestIndexTrackReadsTheInitialisationSegmentAndEverySegment(t *testing.T) {
	// Two segments, 5s and 4s at a 1000-unit timescale.
	head := track([][2]uint32{{2000, 5000}, {3000, 4000}})

	got, err := IndexTrack(head)
	if err != nil {
		t.Fatalf("IndexTrack: %v", err)
	}

	// Everything before the index is the MAP, and a player has nothing without
	// it: ftyp (24) + moov (108).
	if got.InitLength != 132 {
		t.Errorf("InitLength = %d, want 132", got.InitLength)
	}

	// The segments follow the index box, one after another. Absolute offsets,
	// because that is what EXT-X-BYTERANGE takes.
	want := []Segment{
		{Offset: int64(len(head)), Length: 2000, Duration: 5},
		{Offset: int64(len(head)) + 2000, Length: 3000, Duration: 4},
	}
	if len(got.Segments) != len(want) {
		t.Fatalf("got %d segments, want %d", len(got.Segments), len(want))
	}
	for i, w := range want {
		if got.Segments[i] != w {
			t.Errorf("segment %d = %+v, want %+v", i, got.Segments[i], w)
		}
	}
	if got.Duration() != 9 {
		t.Errorf("Duration() = %v, want 9", got.Duration())
	}
}

// first_offset is a gap between the index and the first segment. Ignoring it
// would put every byte range in the file one gap out of step — the kind of
// fault that plays a few seconds and then falls apart.
func TestIndexTrackHonoursTheGapAfterTheIndex(t *testing.T) {
	head := append(box("ftyp", make([]byte, 8)), sidxV0(1000, 64, [][2]uint32{{500, 1000}})...)

	got, err := IndexTrack(head)
	if err != nil {
		t.Fatalf("IndexTrack: %v", err)
	}
	if want := int64(len(head)) + 64; got.Segments[0].Offset != want {
		t.Errorf("first segment at %d, want %d", got.Segments[0].Offset, want)
	}
}

// The head is fetched a fixed amount at a time, so "not far enough yet" is an
// ordinary answer and must be told apart from a file that makes no sense: one
// is fixed by asking for more, the other by giving up on the track.
func TestIndexTrackSaysWhenTheHeadIsTooShort(t *testing.T) {
	full := track([][2]uint32{{2000, 5000}})

	for _, tc := range []struct {
		name string
		head []byte
	}{
		{"nothing at all", nil},
		{"stops before the index", full[:100]},
		{"stops inside the index", full[:len(full)-4]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := IndexTrack(tc.head); !errors.Is(err, ErrNoSegmentIndex) {
				t.Fatalf("err = %v, want ErrNoSegmentIndex", err)
			}
		})
	}
}

func TestMediaPlaylistNamesTheMapAndEveryByteRange(t *testing.T) {
	indexed, err := IndexTrack(track([][2]uint32{{2000, 5000}, {3000, 4500}}))
	if err != nil {
		t.Fatalf("IndexTrack: %v", err)
	}

	got := MediaPlaylist(indexed, "/api/videos/abc/track/137")

	for _, want := range []string{
		"#EXT-X-VERSION:7",
		"#EXT-X-PLAYLIST-TYPE:VOD",
		// Rounded up from the longest segment, or the playlist is invalid.
		"#EXT-X-TARGETDURATION:5",
		`#EXT-X-MAP:URI="/api/videos/abc/track/137",BYTERANGE="132@0"`,
		"#EXTINF:5.000,",
		// Offsets taken from the index rather than written out here: the point
		// being checked is that the playlist carries what was parsed, and a
		// second copy of the arithmetic would only test itself.
		fmt.Sprintf("#EXT-X-BYTERANGE:2000@%d", indexed.Segments[0].Offset),
		"#EXTINF:4.500,",
		fmt.Sprintf("#EXT-X-BYTERANGE:3000@%d", indexed.Segments[1].Offset),
		"#EXT-X-ENDLIST",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("playlist is missing %q:\n%s", want, got)
		}
	}

	// One URL, many ranges — that is the whole point: nothing is cut up on disk.
	if n := strings.Count(got, "/api/videos/abc/track/137"); n != 3 {
		t.Errorf("URI appears %d times, want 3 (the map and two segments)", n)
	}
}

// The audio file is the same one whatever the video height, so it is offered
// once as a group. Repeating it per rendition would make a player re-fetch it
// on every quality change.
func TestMasterPlaylistOffersOneAudioGroupForEveryRendition(t *testing.T) {
	got := MasterPlaylist([]Rendition{
		{URI: "v720.m3u8", Codecs: "avc1.4d401f", Bandwidth: 1_500_000, Width: 1280, Height: 720},
		{URI: "v1080.m3u8", Codecs: "avc1.640028", Bandwidth: 4_000_000, Width: 1920, Height: 1080},
	}, "audio.m3u8", "mp4a.40.2")

	for _, want := range []string{
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Audio",DEFAULT=YES,AUTOSELECT=YES,URI="audio.m3u8"`,
		`#EXT-X-STREAM-INF:BANDWIDTH=1500000,CODECS="avc1.4d401f,mp4a.40.2",RESOLUTION=1280x720,AUDIO="audio"`,
		"v720.m3u8",
		`RESOLUTION=1920x1080`,
		"v1080.m3u8",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("master playlist is missing %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "audio.m3u8"); n != 1 {
		t.Errorf("audio listed %d times, want once", n)
	}
}
