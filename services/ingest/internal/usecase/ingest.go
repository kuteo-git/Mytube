// Package usecase holds the ingest application logic.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

type Ingest struct {
	downloader domain.Downloader
	channels   domain.ChannelSource
	store      domain.JobStore
	library    domain.Library
	// Read-only here: the scanner writes passes, this only lists them for the
	// Activity page. Optional, and set separately from New, because most of
	// what this type does has nothing to do with scan history and every test
	// of that work would otherwise have to say so.
	scans         domain.ScanStore
	defaultHeight int32
	newID         func() string
	logger        *slog.Logger
	resolved      *resolveCache
	backfill      *backfillState
	subtitles     *subtitleFetches
	// Gap between backfill fetches. Zero means the package default; tests set
	// it so they do not wait out a rate limit meant for YouTube.
	backfillDelay time.Duration
	// Asks YouTube whether a video is a Short. Optional and set separately from
	// New, like the scan store above: everything else this type does works
	// without it, and every test would otherwise have to supply one.
	shorts domain.ShortChecker
	// Gap between Short probes. Zero means the package default.
	shortDelay time.Duration
}

// WithShortChecker attaches the Short probe. Without one the pass does nothing,
// which is the right behaviour for a deployment that has not configured it: a
// video nobody has asked about is treated as not a Short and still shown.
func (i *Ingest) WithShortChecker(c domain.ShortChecker) *Ingest {
	i.shorts = c
	return i
}

func New(
	downloader domain.Downloader,
	channels domain.ChannelSource,
	store domain.JobStore,
	library domain.Library,
	defaultHeight int32,
	logger *slog.Logger,
) *Ingest {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Ingest{
		downloader:    downloader,
		channels:      channels,
		store:         store,
		library:       library,
		defaultHeight: defaultHeight,
		newID:         uuid.NewString,
		logger:        logger,
		resolved:      newResolveCache(),
		backfill:      &backfillState{},
		subtitles:     newSubtitleFetches(),
	}
}

// SetScanStore attaches the scan history the Activity page reads.
func (i *Ingest) SetScanStore(scans domain.ScanStore) {
	i.scans = scans
}

// Search looks upstream, always — the library is what the topics chose to
// bring in, and a person searching is by definition looking past that. Results
// are annotated with whether each video is already local, because that is the
// difference between playing in two seconds and waiting for a download.
func (i *Ingest) Search(ctx context.Context, query string, limit int32) ([]domain.ExternalVideo, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	videos, err := i.downloader.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}

	for idx := range videos {
		if _, found, err := i.library.FindBySourceURL(ctx, videos[idx].SourceURL); err == nil {
			videos[idx].InLibrary = found
		}
	}
	return videos, nil
}

// EnsureVideo writes the catalog row for a search result so the watch page can
// open it. Deliberately no topic: the feed stays what topics.yaml chose, and a
// video someone went looking for once should not start shaping it.
func (i *Ingest) EnsureVideo(ctx context.Context, url string) (string, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return "", fmt.Errorf("%w: url is required", domain.ErrInvalid)
	}

	if videoID, found, err := i.library.FindBySourceURL(ctx, url); err == nil && found {
		return videoID, nil
	}

	meta, err := i.downloader.Preview(ctx, url)
	if err != nil {
		return "", err
	}
	if meta.ID == "" {
		return "", fmt.Errorf("%w: upstream returned no video id", domain.ErrNotFound)
	}

	// Preview already paid for full metadata, so YouTube's own category is
	// here for free — that is where topics come from now (CLAUDE.md §7).
	// Catalog merges topics on conflict, so this adds to whatever a scan may
	// have already filed the video under rather than replacing it.
	meta.Topics = categoryTopics(meta)

	if err := i.library.UpsertChannel(ctx, meta); err != nil {
		return "", err
	}
	if err := i.library.UpsertVideo(ctx, meta, "ABSENT"); err != nil {
		return "", err
	}
	return meta.ID, nil
}

// fallbackChannelPage is the page size used when the browse API is unavailable
// and the flat playlist listing has to stand in.
const fallbackChannelPage = 30

// ListChannelUploads reads a channel's uploads straight from YouTube.
//
// The channel page uses this instead of the local catalog: a scan only ever
// brings in the most recent few dozen uploads, so browsing a channel through
// the catalog would hit a wall that has nothing to do with the channel.
//
// YouTube's internal browse API is tried first, because it is the only source
// that carries view counts, upload dates and the channel's own sort options. It
// is undocumented, so any failure falls back to the flat playlist listing —
// which always works but knows none of those three things.
// ResolveLive lists the HLS playlists of a broadcast in progress.
//
// Straight through to the downloader: there is nothing to decide here that the
// resolve does not already decide, and the caller — the gateway — is the one
// that has to turn these into something a browser can fetch, since every URL
// here is signed to the address that asked for it.
func (i *Ingest) ResolveLive(ctx context.Context, url string, maxHeight int32) (domain.LiveStream, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return domain.LiveStream{}, fmt.Errorf("%w: url is required", domain.ErrInvalid)
	}
	if maxHeight <= 0 {
		maxHeight = i.defaultHeight
	}
	return i.downloader.ResolveLive(ctx, url, maxHeight)
}

// FetchSubtitles gets the captions for a video without queueing a transfer.
//
// The same pass Submit starts, reached from the other side. With caching off
// there is no Submit, and captions would go silently — and with them the
// translation and the read-aloud, both of which read the .vtt this writes.
//
// Returns at once: `startSubtitleFetch` runs in the background under its own
// claim (`i.subtitles.begin`), so this and a download arriving a moment later
// cannot both run the same fetch.
func (i *Ingest) FetchSubtitles(url string, preferredHeight int32) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return fmt.Errorf("%w: url is required", domain.ErrInvalid)
	}
	if preferredHeight <= 0 {
		preferredHeight = i.defaultHeight
	}
	i.startSubtitleFetch(url, preferredHeight)
	return nil
}

// ResolveChannel says who a channel address names.
//
// One upstream request, and only for a channel the library has never seen. A
// pasted link usually carries a handle rather than an id, and the catalog is
// keyed by the id — so without this, a channel nobody here follows cannot be
// written down or opened at all.
//
// Reads through ChannelInfo, which is a flat listing of nothing (`PlaylistItems("0")`):
// it asks for the channel's header and none of its videos, so this costs the
// cheap kind of request rather than the counted kind (§8 risk 6).
func (i *Ingest) ResolveChannel(ctx context.Context, channel string) (domain.ChannelMetadata, error) {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return domain.ChannelMetadata{}, fmt.Errorf("%w: channel is required", domain.ErrInvalid)
	}

	meta, err := i.downloader.ChannelInfo(ctx, channelAddress(channel))
	if err != nil {
		return domain.ChannelMetadata{}, err
	}
	if meta.ID == "" {
		// A listing that answered without an id is not an answer: the id is the
		// catalog's key, and a row cannot be written without one.
		return domain.ChannelMetadata{}, fmt.Errorf("%w: %s named no channel", domain.ErrNotFound, channel)
	}
	return meta, nil
}

// channelAddress turns whatever was pasted into something yt-dlp will take.
//
// A handle and an id are both valid on their own in this system's own APIs, and
// neither is a URL. `/channel/<id>` for an id and `/<handle>` for a handle are
// the two forms YouTube resolves without a redirect.
func channelAddress(channel string) string {
	switch {
	case strings.HasPrefix(channel, "http://"), strings.HasPrefix(channel, "https://"):
		return channel
	case strings.HasPrefix(channel, "@"):
		return "https://www.youtube.com/" + channel
	case strings.HasPrefix(channel, "UC"):
		return "https://www.youtube.com/channel/" + channel
	}
	return "https://www.youtube.com/" + channel
}

func (i *Ingest) ListChannelUploads(ctx context.Context, channel, pageToken string) (domain.ChannelUploads, error) {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return domain.ChannelUploads{}, fmt.Errorf("%w: channel is required", domain.ErrInvalid)
	}

	if uploads, err := i.channelUploadsViaBrowse(ctx, channel, pageToken); err != nil {
		i.logger.Warn("channel uploads via browse", "channel", channel, "error", err)
	} else if len(uploads.Videos) > 0 {
		i.markInLibrary(ctx, uploads.Videos)
		return uploads, nil
	}

	// The fallback pages by offset rather than by token, so the page token is
	// simply the numeric offset the flat listing understands.
	offset, _ := strconv.Atoi(pageToken)
	if offset < 0 {
		offset = 0
	}

	_, videos, err := i.downloader.ListPlaylist(ctx, channelUploadsURL(channel), int32(offset), fallbackChannelPage)
	if err != nil {
		return domain.ChannelUploads{}, err
	}
	i.markInLibrary(ctx, videos)

	next := ""
	if len(videos) >= fallbackChannelPage {
		next = strconv.Itoa(offset + len(videos))
	}
	return domain.ChannelUploads{Videos: videos, NextPageToken: next}, nil
}

func (i *Ingest) channelUploadsViaBrowse(ctx context.Context, channel, pageToken string) (domain.ChannelUploads, error) {
	if i.channels == nil {
		return domain.ChannelUploads{}, fmt.Errorf("no channel source configured")
	}

	// A continuation token already encodes both the channel and the ordering it
	// belongs to, so resolving the id again would be wasted work.
	browseID := ""
	if pageToken == "" {
		id, err := i.channels.ResolveChannelID(ctx, channel)
		if err != nil {
			return domain.ChannelUploads{}, err
		}
		browseID = id
	}
	return i.channels.ChannelUploads(ctx, browseID, pageToken)
}

// markInLibrary annotates results with whether each video already has a catalog
// row — the difference between opening instantly and waiting for a fetch.
func (i *Ingest) markInLibrary(ctx context.Context, videos []domain.ExternalVideo) {
	for idx := range videos {
		if _, found, err := i.library.FindBySourceURL(ctx, videos[idx].SourceURL); err == nil {
			videos[idx].InLibrary = found
		}
	}
}

// channelUploadsURL accepts either a handle or a bare channel id, since the
// catalog stores both shapes depending on what the source listing carried.
func channelUploadsURL(channel string) string {
	if strings.HasPrefix(channel, "http://") || strings.HasPrefix(channel, "https://") {
		return channel
	}
	if strings.HasPrefix(channel, "@") {
		return "https://www.youtube.com/" + channel + "/videos"
	}
	return "https://www.youtube.com/channel/" + channel + "/videos"
}

// Preview resolves full metadata for one video. Used by the download worker,
// because flat listings omit fields the catalog row needs.
func (i *Ingest) Preview(ctx context.Context, url string) (domain.ExternalVideo, error) {
	if strings.TrimSpace(url) == "" {
		return domain.ExternalVideo{}, fmt.Errorf("%w: url is required", domain.ErrInvalid)
	}
	return i.downloader.Preview(ctx, url)
}

// ResolveStream returns a directly playable upstream URL for a video that is
// not on disk yet. This is the half of the hybrid model that makes clicking a
// result feel immediate; the other half is the background download.
//
// The URL is progressive — one file carrying both video and audio — so the
// browser can range-request it and the viewer can seek freely from the first
// second. That caps it at whatever muxed rendition YouTube still publishes,
// which is 360p, and that is the deliberate trade: a seekable 360p picture now
// beats a sharper one that has to be muxed first and cannot be seeked at all.
//
// Cached, because the resolve is the entire startup delay and the answer stays
// good for hours.
func (i *Ingest) ResolveStream(ctx context.Context, videoID string, refresh bool) (domain.StreamLocation, error) {
	if videoID == "" {
		return domain.StreamLocation{}, fmt.Errorf("%w: video_id is required", domain.ErrInvalid)
	}

	// The caller met a refusal on the URL this cache holds, so it is worthless
	// however long it has left to live. Drop it before reading, or the retry
	// gets handed back the very URL it just failed on.
	if refresh {
		i.resolved.forget(videoID)
	}

	if cached, ok := i.resolved.get(videoID); ok {
		return cached, nil
	}

	sourceURL, err := i.library.SourceURLFor(ctx, videoID)
	if err != nil {
		return domain.StreamLocation{}, err
	}
	if sourceURL == "" {
		return domain.StreamLocation{}, fmt.Errorf("video %s has no source url: %w", videoID, domain.ErrNotFound)
	}

	// Already refused: answer from the record rather than asking again. The
	// player resolves a stream on every open, so this is the request that would
	// keep asking upstream a question already answered.
	if refusal, found, checkErr := i.store.UnavailableSourceFor(ctx, sourceURL); checkErr == nil && found {
		return domain.StreamLocation{}, domain.NewUnavailable(refusal.Reason, refusal.Detail)
	}

	location, err := i.downloader.ResolveStream(ctx, sourceURL)
	if err != nil {
		i.recordUnavailable(ctx, sourceURL, videoID, err)
		return domain.StreamLocation{}, domain.AsUnavailable(err)
	}
	i.resolved.put(videoID, location)
	return location, nil
}

// Submit resolves metadata immediately so the video appears in the library
// straight away — marked as downloading rather than absent — and only then
// queues the transfer.
func (i *Ingest) Submit(ctx context.Context, url, requestedBy string, preferredHeight int32) (domain.Job, error) {
	return i.submit(ctx, url, requestedBy, preferredHeight, true)
}

// How long a URL is left alone after a transfer of it failed.
//
// Nothing here decides *why* it failed, and that is the point. A temporary 403
// is not recorded as a refusal — CLAUDE.md is emphatic that it must not be —
// so nothing stopped the next request queueing the same doomed transfer again.
// Measured on 53KMZ_uRJOc: three jobs in twenty-six seconds, each dying on the
// same 403, because the player polls the stream answer every five seconds and
// every poll schedules a download.
//
// Two minutes because the failures worth waiting out last minutes, and because
// a viewer who does not want to wait has a button that says Retry.
const failureCooldown = 2 * time.Minute

// submit carries the one distinction that matters here: whether a person asked
// for this.
//
// Automatic means the player, the prefetch, the repair, the scanner — anything
// that will come round again on its own and would otherwise come round again
// every five seconds. Deliberate means somebody pressed Retry, and a person
// pressing Retry is the evidence that overturns a wait, exactly as it already
// overturns a permanent refusal.
func (i *Ingest) submit(
	ctx context.Context, url, requestedBy string, preferredHeight int32, automatic bool,
) (domain.Job, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return domain.Job{}, fmt.Errorf("%w: url is required", domain.ErrInvalid)
	}
	if preferredHeight <= 0 {
		preferredHeight = i.defaultHeight
	}

	// Refused already, permanently. Every route into the library goes through
	// here — pressing play, prefetching, repairing a missing file, the scanner
	// — which is why the check is here and not at any one of them: thirteen
	// jobs for one members-only video were not thirteen presses of a button.
	if refusal, found, err := i.store.UnavailableSourceFor(ctx, url); err != nil {
		// The check failing is not a reason to refuse work. Losing it costs a
		// wasted extract; refusing on it would stop the library on a database
		// hiccup.
		i.logger.Warn("check unavailable source", "url", url, "error", err)
	} else if found {
		return domain.Job{}, fmt.Errorf("%w: %s",
			domain.NewUnavailable(refusal.Reason, refusal.Detail), url)
	}

	// Tried a moment ago and it did not work. Enqueue is idempotent only while
	// a job is still QUEUED or RUNNING, so a failure leaves nothing for the
	// next request to attach to and it starts another one.
	if automatic {
		if failedAt, found, err := i.store.LastFailureFor(ctx, url); err != nil {
			// The same reasoning as the refusal check above: losing this costs
			// one wasted transfer, refusing on it would stop the library on a
			// database hiccup.
			i.logger.Warn("check last failure", "url", url, "error", err)
		} else if since := time.Since(failedAt); found && since < failureCooldown {
			// time.Now rather than an injected clock: the failure time comes
			// from the store, so a test controls this by choosing that.
			return domain.Job{}, fmt.Errorf("%w: %s failed %s ago",
				domain.ErrInvalid, url, since.Truncate(time.Second))
		}
	}

	job, err := i.store.Enqueue(ctx, domain.Job{
		ID:              i.newID(),
		SourceURL:       url,
		PreferredHeight: preferredHeight,
		RequestedBy:     requestedBy,
		State:           domain.JobQueued,
	})
	if err != nil {
		return domain.Job{}, err
	}

	// Captions, started now rather than when the worker reaches this job. Only
	// pressing play gets here — hovering a card resolves the stream and stops
	// there — which is what keeps a feed scroll from turning into dozens of
	// upstream extracts. See subtitles.go.
	i.startSubtitleFetch(url, preferredHeight)

	return job, nil
}

func (i *Ingest) GetJob(ctx context.Context, jobID string) (domain.Job, error) {
	if jobID == "" {
		return domain.Job{}, fmt.Errorf("%w: job_id is required", domain.ErrInvalid)
	}
	return i.store.Get(ctx, jobID)
}

func (i *Ingest) ListJobs(ctx context.Context, activeOnly, hideDismissed bool, limit int32) ([]domain.Job, error) {
	return i.store.List(ctx, activeOnly, hideDismissed, limit)
}

func (i *Ingest) CancelJob(ctx context.Context, jobID string) error {
	if jobID == "" {
		return fmt.Errorf("%w: job_id is required", domain.ErrInvalid)
	}
	return i.store.Cancel(ctx, jobID)
}

// DismissJob takes a finished job off the Activity page.
//
// The one action a completed or failed job had was being read. Dismissing is
// what lets somebody clear the ones they have dealt with, so what remains on
// the page is what still wants attention.
func (i *Ingest) DismissJob(ctx context.Context, jobID string) error {
	if jobID == "" {
		return fmt.Errorf("%w: job_id is required", domain.ErrInvalid)
	}
	return i.store.Dismiss(ctx, jobID)
}

// DismissJobs hides every job with a given state, so the Activity page can
// be cleared in one action rather than one row at a time.
func (i *Ingest) DismissJobs(ctx context.Context, state string) (int64, error) {
	if state == "" {
		return 0, fmt.Errorf("%w: state is required", domain.ErrInvalid)
	}
	return i.store.DismissByState(ctx, state)
}

// RetryJob queues the same URL again.
//
// Worth having because the usual reason a job here failed is temporary — a 429
// from the caption endpoint, an IP block that has since lifted, a network that
// dropped (CLAUDE.md §8, risk 4). Before this, the only way to try again was to
// find the video and press play, and the only action offered on a failure was
// to hide it — which would have made hiding the way failures get dealt with.
//
// A new job rather than resetting the old row: attempts, timings and the error
// that was reported are what the page is for, and rewriting them in place would
// erase the record of the thing being retried.
func (i *Ingest) RetryJob(ctx context.Context, jobID, requestedBy string) (domain.Job, error) {
	if jobID == "" {
		return domain.Job{}, fmt.Errorf("%w: job_id is required", domain.ErrInvalid)
	}
	previous, err := i.store.Get(ctx, jobID)
	if err != nil {
		return domain.Job{}, err
	}
	if previous.State == domain.JobQueued || previous.State == domain.JobRunning {
		return domain.Job{}, fmt.Errorf("%w: job %s has not finished", domain.ErrInvalid, jobID)
	}
	if requestedBy == "" {
		requestedBy = previous.RequestedBy
	}

	// A person pressing Retry is the evidence that overturns a permanent
	// refusal. Members-only videos do get opened to everyone later, and the
	// alternative to this line is a judgement with no way back — so the block
	// is lifted here rather than given a clock of its own to be wrong about.
	if err := i.store.ClearUnavailable(ctx, previous.SourceURL); err != nil {
		i.logger.Warn("clear unavailable source", "url", previous.SourceURL, "error", err)
	}

	job, err := i.submit(ctx, previous.SourceURL, requestedBy, previous.PreferredHeight, false)
	if err != nil {
		return domain.Job{}, err
	}
	// The failure has been acted on, so it stops asking to be. Best effort: a
	// retry that queued is a success even if the old row stays visible.
	if err := i.store.Dismiss(ctx, jobID); err != nil {
		i.logger.Warn("dismiss after retry", "job", jobID, "error", err)
	}
	return job, nil
}

// ListScans reports what the scanner has been doing, newest first.
func (i *Ingest) ListScans(ctx context.Context, limit, offset int32) ([]domain.ScanResult, int32, error) {
	if i.scans == nil {
		return nil, 0, nil
	}
	return i.scans.ListScans(ctx, limit, offset)
}

// ClearScans deletes every scan row.
func (i *Ingest) ClearScans(ctx context.Context) error {
	if i.scans == nil {
		return nil
	}
	return i.scans.ClearScans(ctx)
}

// FetchComments reads YouTube comments for a video. The caller passes a video
// id; the source URL is resolved through the library so the caller does not need
// to know it.
func (i *Ingest) FetchComments(ctx context.Context, videoID string) ([]domain.YouTubeComment, error) {
	if videoID == "" {
		return nil, fmt.Errorf("%w: video_id is required", domain.ErrInvalid)
	}

	sourceURL, err := i.library.SourceURLFor(ctx, videoID)
	if err != nil {
		return nil, err
	}
	if sourceURL == "" {
		return nil, fmt.Errorf("video %s has no source url: %w", videoID, domain.ErrNotFound)
	}

	// Refused already: answer from the record rather than asking upstream the
	// question it has already refused.
	if refusal, found, err := i.store.UnavailableSourceFor(ctx, sourceURL); err == nil && found {
		return nil, domain.NewUnavailable(refusal.Reason, refusal.Detail)
	}

	comments, err := i.downloader.FetchComments(ctx, sourceURL)
	if err != nil {
		// Comments are how this was found: the video had never been downloaded,
		// so nothing else had ever asked upstream about it.
		i.recordUnavailable(ctx, sourceURL, videoID, err)
		return nil, domain.AsUnavailable(err)
	}
	return comments, nil
}

// CancelVideoDownload stops any transfer running for a video.
//
// Called when a viewer leaves the watch page. Pressing play schedules a copy so
// the video is there next time, but a copy nobody is waiting for is a request
// to YouTube nobody is waiting for either — and this library has already been
// blocked once for making too many of those. A transfer that outlives the
// interest in it is spend without a reason.
//
// Cancelling nothing is success: most departures are from a video that already
// had its copy, or never needed one.
func (i *Ingest) CancelVideoDownload(ctx context.Context, videoID string) (int, error) {
	if videoID == "" {
		return 0, fmt.Errorf("%w: video_id is required", domain.ErrInvalid)
	}
	cancelled, err := i.store.CancelForVideo(ctx, videoID)
	if err != nil {
		return 0, err
	}
	if cancelled > 0 {
		i.logger.Info("download cancelled on leaving", "video", videoID, "jobs", cancelled)
	}
	return cancelled, nil
}

// categoryTopics turns YouTube's own category into the video's topic list.
// Empty when the category is unknown, which leaves the video's existing topics
// untouched rather than clearing them.
func categoryTopics(v domain.ExternalVideo) []string {
	if v.Category == "" {
		return v.Topics
	}
	return []string{v.Category}
}

// recordUnavailable notes a permanent refusal and tells the catalogue, so the
// video stops being offered as something that could still arrive.
//
// Called from every path that talks to upstream — the transfer, comments,
// remux resolution — because any of them may be the first to be told, and the
// one that hears it is the one that knows. Comments and remux are how this was
// discovered in the first place: the video had never been downloaded at all.
//
// Anything that is not a permanent refusal passes straight through. That is the
// safe direction: a temporary failure recorded here would take a video out of
// the library until somebody pressed Retry by hand.
func (i *Ingest) recordUnavailable(ctx context.Context, sourceURL, videoID string, cause error) {
	wrapped := domain.AsUnavailable(cause)
	reason, permanent := domain.ReasonOf(wrapped)
	if !permanent {
		return
	}
	detail := ""
	var u *domain.Unavailable
	if errors.As(wrapped, &u) {
		detail = u.Detail
	}

	if err := i.store.MarkUnavailable(ctx, domain.UnavailableSource{
		SourceURL: sourceURL,
		VideoID:   videoID,
		Reason:    reason,
		Detail:    detail,
	}); err != nil {
		i.logger.Error("record unavailable source", "url", sourceURL, "error", err)
		return
	}
	i.logger.Info("upstream refused permanently",
		"url", sourceURL, "video", videoID, "reason", string(reason))

	i.reportUnavailable(ctx, sourceURL, videoID)
}

// reportUnavailable tells the catalogue, and remembers whether it heard.
//
// Two writes rather than one because they are in two services and only one of
// them can be trusted to be up. The row is written first; the catalogue is a
// report about it, and a report that did not arrive is retried at the next
// start rather than lost.
func (i *Ingest) reportUnavailable(ctx context.Context, sourceURL, videoID string) {
	if videoID == "" {
		// Nothing to mark. The refusal is still recorded, and the block on
		// queueing works from the URL alone.
		return
	}
	if err := i.library.SetMediaState(ctx, videoID, "UNAVAILABLE", "", 0, nil); err != nil {
		i.logger.Warn("mark video unavailable", "video", videoID, "error", err)
		return
	}
	if err := i.store.MarkUnavailableReported(ctx, sourceURL); err != nil {
		i.logger.Warn("record unavailable reported", "url", sourceURL, "error", err)
	}
}

// ReconcileUnavailable finishes reports the catalogue never received.
//
// Runs once at start. The refusal is recorded by whichever request met it,
// which may be at a moment when catalog is restarting — and a video left
// looking merely "queued" is one the feed goes on offering. Bounded and
// idempotent: rows that have been reported are not read at all.
func (i *Ingest) ReconcileUnavailable(ctx context.Context) {
	pending, err := i.store.UnreportedUnavailable(ctx, 200)
	if err != nil {
		i.logger.Warn("list unreported unavailable sources", "error", err)
		return
	}
	for _, u := range pending {
		videoID := u.VideoID
		if videoID == "" {
			videoID = videoIDFromURL(u.SourceURL)
		}
		i.reportUnavailable(ctx, u.SourceURL, videoID)
	}
	if len(pending) > 0 {
		i.logger.Info("reconciled unavailable videos", "count", len(pending))
	}
}

// NoteUpstreamFailure records a refusal met outside the job queue.
//
// The media handler serves bytes over plain HTTP rather than through the queue,
// so it meets upstream on its own — and a members-only video is met there
// first, because nothing ever downloaded it. Exported for that one caller;
// everything inside this package uses recordUnavailable directly.
func (i *Ingest) NoteUpstreamFailure(ctx context.Context, sourceURL, videoID string, cause error) {
	i.recordUnavailable(ctx, sourceURL, videoID, cause)
}

// Refusal reports whether a URL has already been refused permanently.
func (i *Ingest) Refusal(ctx context.Context, sourceURL string) (domain.UnavailableSource, bool) {
	u, found, err := i.store.UnavailableSourceFor(ctx, sourceURL)
	if err != nil {
		i.logger.Warn("check unavailable source", "url", sourceURL, "error", err)
		return domain.UnavailableSource{}, false
	}
	return u, found
}
