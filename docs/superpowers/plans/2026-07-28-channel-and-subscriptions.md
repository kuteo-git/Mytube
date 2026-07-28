# Channel Page & Subscriptions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a channel a place you can go to — banner, avatar, subscriber count, its videos — and make Subscribe mean something: it adds the channel as a content source, so its uploads start arriving in the feed.

**Architecture:** Most of the backend is already built and unused. `GetChannel`, `ListChannelVideos` and `SetSubscription` exist all the way down through catalog's usecase, repository and RPC layers; recsys already reads `SUBSCRIBE` signals and scores with `weightSubscribed`. What is missing is the gateway routes, a `ListSubscriptions` read, real channel artwork, the `ui/` layer, and — the one genuinely new idea — subscriptions feeding the scanner.

**The reversal this locks in:** `topics.yaml` stops being the only content source. Subscribing writes a source row that the scanner treats exactly like a `topics.yaml` entry. Without that, Subscribe on a channel discovered through search would be a dead control: the catalog would hold one video by that channel and the feed would have nothing to promote.

**Tech Stack:** Go (ConnectRPC, pgx, `lrstanley/go-ytdlp`), React 19 + TypeScript + TanStack Query v5 + Tailwind v4.

## Global Constraints

- **All source code, identifiers, comments, commit messages and in-app UI copy MUST be in English.** (CLAUDE.md §4b)
- **No service queries another service's database.** Ingest reads subscriptions by calling catalog, never by touching its tables. (CLAUDE.md §3 rule 1)
- Feature-sliced frontend; `ui/` never calls `fetch`. (CLAUDE.md §5)
- **No dead controls.** Specifically: do not build the Shorts, Releases or Playlists tabs from the reference screenshot. There is no data behind any of them, and `--flat-playlist` does not return view counts, so a "Popular" tab would be empty or wrong. Only a Videos grid is built. (CLAUDE.md §5)
- Avatars and banners are **served from `/media`, stored as relative paths**, never as absolute upstream URLs — the client must work under any LAN hostname or scheme. (`Channel.avatar_path` comment, `catalog.proto:84-86`)
- Verification: `go test ./...`, `make check`, `npx tsc --noEmit -p tsconfig.app.json`.

## Expected build breakage from the port changes

This plan adds four methods to two interfaces: `ChannelInfo` and `FetchChannelArtwork` to `domain.Downloader`, `UpsertChannelArtwork` and `ListSubscribedChannels` to `domain.Library`.

Every test fake implementing those interfaces will stop compiling the moment the method is added. As of this plan there are four, all created by earlier plans in this batch:

- `fakeDownloader`, `fakeLibrary` in `services/ingest/internal/usecase/worker_test.go` (player-playback-fixes, Task 5)
- `deepenDownloader`, `recordingLibrary` in `services/ingest/internal/usecase/expand_test.go` (infinite-feed, Task 4)

This is not a problem to work around — it is the compiler listing everything that needs updating. Add the new methods to each fake as no-ops returning zero values, in the same commit as the port change, so the tree never sits broken. `sourceLibrary` in Task 3 of this plan already implements the full set.

---

### Task 1: Fetch and store channel artwork

`avatar_path` exists in the schema (`0001_init.sql:9`) and in the proto, and **nothing has ever written to it**. There is no banner column at all. Both are needed for the reference layout.

**Files:**
- Create: `services/catalog/migrations/0002_channel_banner.sql`
- Modify: `proto/catalog/v1/catalog.proto` (`Channel.banner_path`)
- Modify: `services/catalog/internal/domain/catalog.go` (`Channel`)
- Modify: `services/catalog/internal/adapter/postgres/repository.go` (`GetChannel`, `UpsertChannel`)
- Modify: `services/ingest/internal/adapter/ytdlp/downloader.go` (new `ChannelInfo` method)
- Modify: `services/ingest/internal/domain/ingest.go` (port + `ChannelMetadata` type)
- Modify: `services/ingest/internal/usecase/scanner.go` (fetch artwork per source)

**Interfaces:**
- Produces:
  - `domain.ChannelMetadata{ID, Name, Handle, AvatarURL, BannerURL string; SubscriberCount int64; Verified bool}`
  - `domain.Downloader.ChannelInfo(ctx context.Context, channelURL string) (ChannelMetadata, error)`
  - `domain.Library.UpsertChannelArtwork(ctx context.Context, m ChannelMetadata, avatarPath, bannerPath string) error`
  - `Channel.BannerPath string` on catalog's domain and proto

- [ ] **Step 1: Add the column**

Create `services/catalog/migrations/0002_channel_banner.sql`:

```sql
-- Channel artwork, served by Caddy under /media like every other asset. Paths
-- are relative so the client works under any LAN hostname or scheme.
ALTER TABLE channels ADD COLUMN IF NOT EXISTS banner_path text NOT NULL DEFAULT '';
```

Check the existing migration filenames and follow their numbering convention.

- [ ] **Step 2: Add the proto field**

In `proto/catalog/v1/catalog.proto`, add to `Channel` using the next free field number (7 is taken by `subscribed`, so use 8):

```proto
  // Wide header image, path under /media. Empty when the source had none.
  string banner_path = 8;
```

Run: `make proto`

- [ ] **Step 3: Carry it through catalog**

Add `BannerPath string` to the `Channel` struct in `services/catalog/internal/domain/catalog.go`. Then update, in `services/catalog/internal/adapter/postgres/repository.go`:
- `GetChannel` (line 261): add `banner_path` to the SELECT list and to the `Scan` targets.
- `UpsertChannel` (line 308): add `banner_path` to the INSERT column list, the values, and the `DO UPDATE SET` clause — following exactly the pattern `avatar_path` already uses in that statement.

Then update the proto conversion in `services/catalog/internal/adapter/rpc/server.go` wherever `Channel` is mapped, adding `BannerPath: c.BannerPath` and the reverse.

- [ ] **Step 4: Fetch the metadata in ingest**

Add to `services/ingest/internal/domain/ingest.go`:

```go
// ChannelMetadata is everything a channel page needs that a flat video listing
// does not carry. Fetched once per source per scan rather than per video.
type ChannelMetadata struct {
	ID              string
	Name            string
	Handle          string
	AvatarURL       string
	BannerURL       string
	SubscriberCount int64
	Verified        bool
}
```

Add to the `Downloader` interface:

```go
	// ChannelInfo reads a channel's own metadata — artwork, handle, subscriber
	// count — none of which appears in a flat playlist listing.
	ChannelInfo(ctx context.Context, channelURL string) (ChannelMetadata, error)
```

Add to the `Library` interface:

```go
	// UpsertChannelArtwork records artwork already downloaded to the media root.
	UpsertChannelArtwork(ctx context.Context, m ChannelMetadata, avatarPath, bannerPath string) error
```

- [ ] **Step 5: Implement `ChannelInfo`**

In `services/ingest/internal/adapter/ytdlp/downloader.go`, add:

```go
// ChannelInfo reads a channel's own metadata by asking yt-dlp for the channel
// URL with no entries at all. `--playlist-items 0` is the cheap way to do it:
// it returns the container's metadata and skips every video in it.
func (d *Downloader) ChannelInfo(ctx context.Context, channelURL string) (domain.ChannelMetadata, error) {
	result, err := ytdlp.New().
		DumpSingleJSON().
		FlatPlaylist().
		PlaylistItems("0").
		NoWarnings().
		Run(ctx, channelURL)
	if err != nil {
		return domain.ChannelMetadata{}, fmt.Errorf("channel info %q: %w", channelURL, err)
	}

	var payload struct {
		ID          string `json:"channel_id"`
		Uploader    string `json:"uploader"`
		Channel     string `json:"channel"`
		UploaderID  string `json:"uploader_id"`
		Followers   int64  `json:"channel_follower_count"`
		Thumbnails  []struct {
			URL    string `json:"url"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
			ID     string `json:"id"`
		} `json:"thumbnails"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		return domain.ChannelMetadata{}, fmt.Errorf("channel info %q: %w", channelURL, err)
	}

	name := payload.Channel
	if name == "" {
		name = payload.Uploader
	}

	meta := domain.ChannelMetadata{
		ID:              payload.ID,
		Name:            name,
		Handle:          payload.UploaderID,
		SubscriberCount: payload.Followers,
	}

	// yt-dlp labels channel artwork by aspect: "avatar_uncropped" is the round
	// picture, "banner_uncropped" the wide header. Falling back to the widest
	// image for the banner is safe; falling back for the avatar is not, because
	// a wide image in a round frame looks broken.
	for _, t := range payload.Thumbnails {
		switch {
		case strings.HasPrefix(t.ID, "avatar"):
			meta.AvatarURL = t.URL
		case strings.HasPrefix(t.ID, "banner"):
			meta.BannerURL = t.URL
		}
	}
	return meta, nil
}
```

Add `encoding/json` and `strings` to the imports if absent. **Verify the flag method names** against the installed library: `go doc github.com/lrstanley/go-ytdlp | grep -iE "playlistitems|dumpsinglejson|flatplaylist"`. Use whatever the library actually exposes.

Add a small downloader helper for saving the images:

```go
// saveChannelImage downloads artwork into the media root and returns its path
// relative to that root, or "" if there was nothing to fetch. Artwork is
// optional decoration: a failure here must never fail a scan.
func (d *Downloader) saveChannelImage(ctx context.Context, url, channelID, kind string) string {
	if url == "" {
		return ""
	}

	dir := filepath.Join(d.mediaRoot, "channels", channelID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	name := kind + ".jpg"
	file, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return ""
	}
	return filepath.Join("channels", channelID, name)
}
```

Expose it through a method the scanner can call:

```go
// FetchChannelArtwork downloads the avatar and banner and returns their paths
// under the media root.
func (d *Downloader) FetchChannelArtwork(ctx context.Context, m domain.ChannelMetadata) (avatarPath, bannerPath string) {
	return d.saveChannelImage(ctx, m.AvatarURL, m.ID, "avatar"),
		d.saveChannelImage(ctx, m.BannerURL, m.ID, "banner")
}
```

Add `FetchChannelArtwork(ctx context.Context, m ChannelMetadata) (string, string)` to the `Downloader` port.

- [ ] **Step 6: Call it once per source per scan**

In `services/ingest/internal/usecase/scanner.go`, inside `scanSource`, before listing the videos:

```go
	// Channel artwork, once per source per scan. It is decoration: a failure
	// here is logged and stepped over, because a scan that fetched every video
	// but no banner is a successful scan.
	if meta, err := s.fetch.ChannelInfo(ctx, source); err != nil {
		s.logger.Warn("channel info", "source", source, "error", err)
	} else if meta.ID != "" {
		avatar, banner := s.fetch.FetchChannelArtwork(ctx, meta)
		if err := s.library.UpsertChannelArtwork(ctx, meta, avatar, banner); err != nil {
			s.logger.Warn("store channel artwork", "channel", meta.ID, "error", err)
		}
	}
```

Implement `UpsertChannelArtwork` in `services/ingest/internal/adapter/catalogclient/library.go` by calling catalog's existing `UpsertChannel` RPC with the artwork fields populated.

- [ ] **Step 7: Verify**

Run: `make check && go test ./...`
Then run `scripts/dev.sh` and force a scan: `curl -X POST localhost:8080/api/topics/refresh`

Expected: `media/channels/<id>/avatar.jpg` and `banner.jpg` exist for the channels in `topics.yaml`; `SELECT id, name, avatar_path, banner_path FROM catalog.channels;` shows non-empty paths.

**Note:** some channels genuinely have no banner. An empty `banner_path` is a correct result, not a failure — the page must render without one.

- [ ] **Step 8: Commit**

```bash
git add proto/ gen/ services/catalog/ services/ingest/
git commit -m "Fetch the artwork a channel page needs"
```

---

### Task 2: Expose channels through the gateway

**Files:**
- Modify: `services/gateway/internal/api/router.go` (routes + handlers)
- Modify: `services/gateway/internal/api/dto.go` (channel DTO)
- Modify: `services/gateway/internal/api/writes.go` (subscription write)
- Modify: `proto/catalog/v1/catalog.proto` (`ListSubscriptions`)
- Modify: `services/catalog/` (usecase, repository, RPC for `ListSubscriptions`)

**Interfaces:**
- Produces:
  - `GET /api/channels/{id}` → `{ channel: channelDTO, videoCount: number }`
  - `GET /api/channels/{id}/videos` → `feedResponse`
  - `POST /api/channels/{id}/subscription` with body `{ "subscribed": true }` → 204
  - `GET /api/subscriptions` → `{ channels: channelDTO[] }`
  - `channelDTO{ id, name, handle, avatarPath, bannerPath, subscriberCount, verified, subscribed }`

- [ ] **Step 1: Add `ListSubscriptions` to catalog**

In `proto/catalog/v1/catalog.proto`, add to the reads section:

```proto
  rpc ListSubscriptions(ListSubscriptionsRequest) returns (ListSubscriptionsResponse);
```

```proto
message ListSubscriptionsRequest {
  string user_id = 1;
}

message ListSubscriptionsResponse {
  repeated Channel channels = 1;
}
```

Run `make proto`, then implement down the stack, following the shape of `ListTopics` at each layer:

Repository (`services/catalog/internal/adapter/postgres/repository.go`), next to `SetSubscription`:

```go
// ListSubscriptions returns the channels a user follows, most recently
// subscribed first — the order the sidebar shows them in.
func (r *Repository) ListSubscriptions(ctx context.Context, userID string) ([]domain.Channel, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, c.name, c.handle, c.avatar_path, c.banner_path,
		       c.subscriber_count, c.verified
		FROM subscriptions s
		JOIN channels c ON c.id = s.channel_id
		WHERE s.user_id = $1
		ORDER BY s.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Channel
	for rows.Next() {
		var c domain.Channel
		if err := rows.Scan(&c.ID, &c.Name, &c.Handle, &c.AvatarPath, &c.BannerPath,
			&c.SubscriberCount, &c.Verified); err != nil {
			return nil, err
		}
		c.Subscribed = true
		out = append(out, c)
	}
	return out, rows.Err()
}
```

**Check the `subscriptions` table's actual column names first** — read `0001_init.sql`. If there is no `created_at`, order by whatever timestamp exists, or by `c.name` if none does; do not invent a column.

Then add the matching `Catalog.ListSubscriptions` usecase method and RPC handler.

- [ ] **Step 2: Add the gateway routes**

In `services/gateway/internal/api/router.go`, register:

```go
	mux.HandleFunc("GET /api/channels/{id}", g.handleGetChannel)
	mux.HandleFunc("GET /api/channels/{id}/videos", g.handleChannelVideos)
	mux.HandleFunc("POST /api/channels/{id}/subscription", g.handleSetSubscription)
	mux.HandleFunc("GET /api/subscriptions", g.handleListSubscriptions)
```

Add `channelDTO` and a `toChannelDTO` converter to `dto.go`, mirroring how `toVideoDTO` is written. Add the four handlers, following the shape of `handleGetVideo` and `handleHistory`.

`handleSetSubscription` must also record the recsys signal, exactly as `handleReaction` does (`writes.go:109-111`):

```go
	signalType := recsysv1.SignalType_SIGNAL_TYPE_SUBSCRIBE
	if !body.Subscribed {
		signalType = recsysv1.SignalType_SIGNAL_TYPE_UNSUBSCRIBE
	}
	go g.recordSignal(userID, signalType, "", "", 0)
```

- [ ] **Step 3: Verify**

Run: `make check && go test ./...`

With the stack up:

```bash
CHANNEL=$(curl -s localhost:8080/api/feed | python3 -c 'import json,sys;print(json.load(sys.stdin)["videos"][0]["channel"]["id"])')
curl -s "localhost:8080/api/channels/$CHANNEL" | python3 -m json.tool
curl -s "localhost:8080/api/channels/$CHANNEL/videos" | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["videos"]))'
curl -s -X POST "localhost:8080/api/channels/$CHANNEL/subscription" -d '{"subscribed":true}'
curl -s localhost:8080/api/subscriptions | python3 -m json.tool
```

Expected: channel metadata with non-empty `avatarPath`; a non-zero video count; the subscription appearing in the list.

- [ ] **Step 4: Commit**

```bash
git add proto/ gen/ services/catalog/ services/gateway/
git commit -m "Serve channels and subscriptions over the gateway"
```

---

### Task 3: Make Subscribe add a content source

Without this, Subscribe on a channel found through search promotes videos that do not exist. This is the piece that reverses the charter.

**Files:**
- Modify: `services/ingest/internal/usecase/scanner.go`
- Modify: `services/ingest/internal/domain/ingest.go` (`Library` gains a read)
- Modify: `services/ingest/internal/adapter/catalogclient/library.go`
- Create: `services/ingest/internal/usecase/scanner_sources_test.go`

**Interfaces:**
- Produces:
  - `domain.Library.ListSubscribedChannels(ctx context.Context) ([]SubscribedChannel, error)`, where `SubscribedChannel{ID, Handle, Name string}`
  - `(*Scanner).sources(ctx) ([]scanTarget, error)` where `scanTarget{TopicName, URL string}` — the merged list of `topics.yaml` entries and subscription-derived channel URLs

- [ ] **Step 1: Write the failing test**

Create `services/ingest/internal/usecase/scanner_sources_test.go`:

```go
package usecase

import (
	"context"
	"testing"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

type sourceLibrary struct{ channels []domain.SubscribedChannel }

func (s sourceLibrary) ListSubscribedChannels(context.Context) ([]domain.SubscribedChannel, error) {
	return s.channels, nil
}
func (sourceLibrary) FindBySourceURL(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (sourceLibrary) UpsertChannel(context.Context, domain.ExternalVideo) error { return nil }
func (sourceLibrary) UpsertVideo(context.Context, domain.ExternalVideo, string) error {
	return nil
}
func (sourceLibrary) UpsertChannelArtwork(context.Context, domain.ChannelMetadata, string, string) error {
	return nil
}
func (sourceLibrary) SetMediaState(context.Context, string, string, string, int64, []domain.SubtitleTrack) error {
	return nil
}
func (sourceLibrary) SourceURLFor(context.Context, string) (string, error) { return "", nil }

func TestSourcesMergesTopicsAndSubscriptions(t *testing.T) {
	scanner := &Scanner{
		topics: stubTopics{},
		library: sourceLibrary{channels: []domain.SubscribedChannel{
			{ID: "UC123", Handle: "@soobin", Name: "SOOBIN"},
		}},
	}

	targets, err := scanner.sources(context.Background())
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("got %d targets, want 2 (one curated source plus one subscription)", len(targets))
	}

	var subscribed *scanTarget
	for i := range targets {
		if targets[i].TopicName == "" {
			subscribed = &targets[i]
		}
	}
	if subscribed == nil {
		t.Fatal("subscription target missing")
	}
	// A handle resolves; a bare channel id needs the /channel/ form.
	if subscribed.URL != "https://www.youtube.com/@soobin/videos" {
		t.Errorf("URL = %q", subscribed.URL)
	}
}

func TestSubscriptionWithoutHandleFallsBackToChannelID(t *testing.T) {
	scanner := &Scanner{
		topics:  stubTopics{},
		library: sourceLibrary{channels: []domain.SubscribedChannel{{ID: "UC123", Name: "No Handle"}}},
	}

	targets, err := scanner.sources(context.Background())
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	for _, target := range targets {
		if target.TopicName == "" && target.URL != "https://www.youtube.com/channel/UC123/videos" {
			t.Errorf("URL = %q", target.URL)
		}
	}
}
```

This reuses `stubTopics` from `expand_test.go` in the same package.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./services/ingest/internal/usecase/ -run TestSources -v`
Expected: FAIL to build — `undefined: scanTarget`, `scanner.sources undefined`, `undefined: domain.SubscribedChannel`.

- [ ] **Step 3: Add the port and the type**

In `services/ingest/internal/domain/ingest.go`:

```go
// SubscribedChannel is a channel a user chose to follow. Subscriptions are a
// content source alongside topics.yaml: the file is what the owner curated
// ahead of time, a subscription is what someone chose while using the system.
// Both feed the same scanner.
type SubscribedChannel struct {
	ID     string
	Handle string
	Name   string
}
```

and add to the `Library` interface:

```go
	ListSubscribedChannels(ctx context.Context) ([]SubscribedChannel, error)
```

- [ ] **Step 4: Implement the merge**

In `services/ingest/internal/usecase/scanner.go`:

```go
// scanTarget is one thing to scan. An empty TopicName marks a subscription:
// videos from it join the library without being filed under a topic, because
// nobody said the channel belongs to one.
type scanTarget struct {
	TopicName string
	URL       string
}

// sources merges the two content sources this system has.
//
// topics.yaml is curated ahead of time and lives in git. Subscriptions are
// chosen while using the app and live in the database. Merging them here — at
// the point of scanning, rather than by writing subscriptions back into the
// file — keeps the file something the owner edits and the app never touches.
func (s *Scanner) sources(ctx context.Context) ([]scanTarget, error) {
	config, err := s.topics.Load(ctx)
	if err != nil {
		return nil, err
	}

	var targets []scanTarget
	for _, topic := range config.Topics {
		for _, url := range topic.Sources {
			targets = append(targets, scanTarget{TopicName: topic.Name, URL: url})
		}
	}

	channels, err := s.library.ListSubscribedChannels(ctx)
	if err != nil {
		// A subscription list that cannot be read must not stop the curated
		// sources from being scanned.
		s.logger.Warn("list subscribed channels", "error", err)
		return targets, nil
	}

	for _, c := range channels {
		targets = append(targets, scanTarget{URL: channelVideosURL(c)})
	}
	return targets, nil
}

// channelVideosURL prefers the handle, which is stable and readable. Some
// channels have none — Tinh tế is the known case in topics.yaml — and those
// need the channel id form instead.
func channelVideosURL(c domain.SubscribedChannel) string {
	if c.Handle != "" {
		handle := c.Handle
		if !strings.HasPrefix(handle, "@") {
			handle = "@" + handle
		}
		return "https://www.youtube.com/" + handle + "/videos"
	}
	return "https://www.youtube.com/channel/" + c.ID + "/videos"
}
```

Then rewrite `ScanNow` to iterate `s.sources(ctx)` rather than reading `config.Topics` directly, passing `target.TopicName` where it currently passes the topic name. Read the existing `ScanNow` in full before changing it and keep its concurrency, error collection and `ScanResult` accounting intact.

- [ ] **Step 5: Implement the catalog-client read**

In `services/ingest/internal/adapter/catalogclient/library.go`, implement `ListSubscribedChannels` by calling `ListSubscriptions` from Task 2. It needs a user id; Phase 1 has a seeded dev user, so read it from the same environment variable the gateway uses for `devUserID` and pass it through the `Library` constructor. Do **not** hardcode it in the adapter.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./services/ingest/... -v`
Expected: PASS.

- [ ] **Step 7: Verify end to end**

With the stack up, subscribe to a channel that is not in `topics.yaml` — search for one in the UI, open a video, subscribe — then force a scan:

```bash
curl -s -X POST localhost:8080/api/topics/refresh | python3 -m json.tool
```

Expected: `sourcesScanned` is one higher than the number of sources in `topics.yaml`, and `videosAdded` is non-zero. Confirm the new videos carry no topic:

```sql
SELECT count(*) FROM catalog.video_topics vt
JOIN catalog.videos v ON v.id = vt.video_id
WHERE v.channel_id = '<the subscribed channel id>';
```

Expected: 0. (Adjust the table name to whatever the schema actually uses for the video↔topic join.)

- [ ] **Step 8: Commit**

```bash
git add services/ingest/
git commit -m "Make subscribing add a real content source"
```

---

### Task 4: Build the channel page

**Files:**
- Create: `web/src/features/catalog/ui/ChannelHeader.tsx`
- Create: `web/src/pages/ChannelPage.tsx`
- Modify: `web/src/features/catalog/infrastructure/catalogRepository.ts`
- Modify: `web/src/features/catalog/application/queries.ts`
- Modify: `web/src/features/catalog/domain/video.ts` (`Channel` type gains `bannerPath`)
- Modify: `web/src/main.tsx` (route)
- Modify: `web/src/features/catalog/ui/VideoCard.tsx:54-57` (link the channel name)
- Modify: `web/src/features/watch/ui/VideoActions.tsx:26-34` (link the channel, add Subscribe)

**Interfaces:**
- Produces:
  - `repo.getChannel(id)`, `repo.listChannelVideos(id, pageToken?)`, `repo.setSubscription(id, subscribed)`, `repo.listSubscriptions()`
  - `useChannel(id)`, `useChannelVideos(id)`, `useSetSubscription(channelId)`, `useSubscriptions()`
  - Route `/channel/:channelId`

- [ ] **Step 1: Extend the repository and hooks**

Add the four methods to `catalogRepository.ts` following the existing style, and add `bannerPath: string` to the `Channel` type in `domain/video.ts`. Then in `queries.ts`:

```ts
export function useChannel(channelId: string | undefined) {
  return useQuery({
    queryKey: ['channel', channelId],
    queryFn: () => repo.getChannel(channelId!),
    enabled: Boolean(channelId),
  })
}

export function useChannelVideos(channelId: string | undefined) {
  return useInfiniteQuery({
    queryKey: ['channel-videos', channelId],
    queryFn: ({ pageParam }) => repo.listChannelVideos(channelId!, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextPageToken || undefined,
    enabled: Boolean(channelId),
  })
}

export function useSubscriptions() {
  return useQuery({
    queryKey: ['subscriptions'],
    queryFn: () => repo.listSubscriptions(),
    staleTime: 60_000,
  })
}

/**
 * Subscribing is not only a ranking signal here: it registers the channel as a
 * content source, so the scanner starts bringing its uploads in. The feed cache
 * is invalidated for the first effect; the second lands on the next scan.
 */
export function useSetSubscription(channelId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (subscribed: boolean) => repo.setSubscription(channelId, subscribed),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['channel', channelId] })
      void queryClient.invalidateQueries({ queryKey: ['subscriptions'] })
      void queryClient.invalidateQueries({ queryKey: ['feed'] })
    },
  })
}
```

- [ ] **Step 2: Build the header**

Create `web/src/features/catalog/ui/ChannelHeader.tsx`:

```tsx
import type { Channel } from '../domain/video'
import { useSetSubscription } from '../application/queries'
import { Avatar } from '@/shared/ui/primitives'
import { formatSubscribers } from '@/shared/lib/format'
import { hueFromId } from '@/shared/lib/hue'

/**
 * Channel identity, matching the reference layout in Example/channel.png.
 *
 * The tabs from that screenshot — Shorts, Releases, Playlists, and a Popular
 * row sorted by view count — are deliberately absent. Flat listings return no
 * view count and this library holds no Shorts or releases, so those controls
 * would be decoration over nothing.
 */
export function ChannelHeader({ channel, videoCount }: { channel: Channel; videoCount: number }) {
  const setSubscription = useSetSubscription(channel.id)

  return (
    <header>
      {channel.bannerPath ? (
        <img
          src={`/media/${channel.bannerPath}`}
          alt=""
          className="aspect-[6/1] w-full rounded-xl object-cover"
        />
      ) : (
        // Not every channel has a banner. An empty strip in the brand hue keeps
        // the page's proportions without inventing artwork.
        <div
          className="aspect-[6/1] w-full rounded-xl"
          style={{ background: `linear-gradient(120deg, hsl(${hueFromId(channel.id)} 40% 28%), #0f0f0f)` }}
        />
      )}

      <div className="mt-4 flex flex-wrap items-center gap-4">
        {channel.avatarPath ? (
          <img
            src={`/media/${channel.avatarPath}`}
            alt=""
            className="h-24 w-24 rounded-full object-cover"
          />
        ) : (
          <Avatar hue={hueFromId(channel.id)} name={channel.name} size={96} />
        )}

        <div className="min-w-0">
          <h1 className="text-2xl font-medium">{channel.name}</h1>
          <p className="mt-1 text-sm text-text-2">
            {channel.handle && <span>{channel.handle} · </span>}
            {channel.subscriberCount > 0 && (
              <span>{formatSubscribers(channel.subscriberCount)} · </span>
            )}
            <span>{videoCount} videos in your library</span>
          </p>

          <button
            type="button"
            aria-pressed={channel.subscribed}
            disabled={setSubscription.isPending}
            onClick={() => setSubscription.mutate(!channel.subscribed)}
            className={
              'mt-3 rounded-full px-4 py-2 text-sm font-medium transition-colors duration-150 ease-out disabled:opacity-60 ' +
              (channel.subscribed
                ? 'bg-surface hover:bg-surface-hover'
                : 'bg-text text-bg hover:bg-text/90')
            }
          >
            {channel.subscribed ? 'Subscribed' : 'Subscribe'}
          </button>
        </div>
      </div>
    </header>
  )
}
```

- [ ] **Step 3: Build the page**

Create `web/src/pages/ChannelPage.tsx` using `useChannel`, `useChannelVideos`, `ChannelHeader`, `VideoCard` and the existing `InfiniteList` from `@/shared/ui/InfiniteList` — read `HomePage.tsx` first and mirror its grid and paging exactly, so the two pages behave identically.

Include an honest empty state:

```tsx
        <p className="mt-8 text-sm text-text-2">
          No videos from this channel are in your library yet. Subscribing adds the
          channel as a source, and its uploads arrive on the next scan.
        </p>
```

- [ ] **Step 4: Register the route and link to it**

In `web/src/main.tsx`:

```tsx
            <Route path="/channel/:channelId" element={<ChannelPage />} />
```

In `web/src/features/catalog/ui/VideoCard.tsx`, wrap the channel name (line 55) in a link:

```tsx
            <Link to={`/channel/${video.channel.id}`} className="hover:text-text">
              {video.channel.name}
            </Link>
```

In `web/src/features/watch/ui/VideoActions.tsx`, wrap the avatar and name in a link to the same route, and add the Subscribe button next to them, reusing `useSetSubscription`. **Delete the doc-comment claim that "Subscribe has no meaning here"** (lines 12-14) — it is now false, and leaving it would mislead the next reader.

- [ ] **Step 5: Add the sidebar subscription list**

In `web/src/features/navigation/ui/Sidebar.tsx`, add a Subscriptions section above Topics, matching `Example/subcribed-channel.png`: each row is the channel avatar (`/media/<avatarPath>`, falling back to `Avatar`) plus its name, linking to `/channel/:id`. Render the whole section only when `useSubscriptions()` returns at least one channel — an empty heading is a dead control.

- [ ] **Step 6: Verify it type-checks**

Run: `cd web && npx tsc --noEmit -p tsconfig.app.json`
Expected: no output.

- [ ] **Step 7: Verify in the browser**

Expected, in order:
1. On the home grid, clicking a channel name opens `/channel/:id` with a real banner and avatar.
2. The grid lists that channel's videos and pages as you scroll.
3. Subscribe flips to "Subscribed"; the channel appears in the sidebar with its avatar.
4. Reload — the subscription is still there.
5. `curl -X POST localhost:8080/api/topics/refresh` then reload the channel page: more videos than before.
6. On a watch page, the channel row links to the channel and carries the same Subscribe button, in agreement with the channel page's state.

- [ ] **Step 8: Commit**

```bash
git add web/
git commit -m "Give channels a page, and subscriptions somewhere to live"
```

---

### Task 5: Update the charter

**Files:**
- Modify: `CLAUDE.md` §5, §7, §8b

- [ ] **Step 1: Record the reversal**

Also correct the doc comment on `domain.Topic` (`services/ingest/internal/domain/topics.go:8-10`), which claims topics are "the only way content enters the system". That is no longer true and a stale comment in the domain layer is worse than none:

```go
// A Topic is a name plus the YouTube playlists or channels it draws from.
// Topics are configuration, not user data: they live in topics.yaml and are
// curated ahead of time. They are one of two content sources — the other is
// subscriptions, chosen while using the app and stored in the database. Both
// are scanned the same way.
```

In `CLAUDE.md` §5, the table row `| Subscribe | Bookmark kênh (P1) → thật khi có auto-follow (P3) |` is now wrong. Replace with:

```
| Subscribe | **Thật ngay ở P1**: thêm kênh thành nguồn ingest động, scanner quét như mọi source |
```

In §8b under "Quyết định đã bị đảo trong quá trình làm", add:

```
- **Nguồn nội dung**: từng chốt "topics.yaml là nguồn duy nhất" → **đảo lại**: có hai nguồn,
  topics.yaml (curate trước, nằm trong git) và subscription (chọn trong lúc dùng, nằm trong DB).
  Cả hai đổ vào cùng scanner. Lý do: subscribe một kênh lạ mà không kéo nội dung về thì nó là
  nút chết — catalog chỉ có 1 video của kênh đó, feed không có gì để đẩy lên.
  **App không bao giờ tự ghi vào topics.yaml** — file đó là của người dùng.
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "Record that subscriptions are a content source now"
```

---

## Verification of the whole plan

```bash
make check
go test ./...
cd web && npx tsc --noEmit -p tsconfig.app.json
```

Then the reported defects, end to end:

1. Click "SOOBIN Official" anywhere → the channel page opens with banner, avatar, subscriber count and its videos.
2. Subscribe → it appears in the sidebar; after a scan, its uploads are in the feed.
3. No Shorts / Releases / Playlists tab exists anywhere — by decision, not omission.
