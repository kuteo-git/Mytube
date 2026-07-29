"""Generates a synthetic event log with the production schema.

Why this exists: the live database this repository serves holds 2,159 signals
from **one** user across 93 videos. A two-tower model trained with in-batch
negative sampling needs at least as many distinct videos per batch as the batch
size, and it learns a mapping from watch history to taste — with a single user
there is exactly one trajectory to learn from, so the model memorises instead of
generalising. Real data will get there; until it does, the pipeline still has to
be runnable and measurable end to end.

So the generator is not noise. It plants a latent structure the model can be
scored against: every user gets a preference vector over categories and a small
set of favourite creators, and watch ratio is drawn from that preference plus
video quality plus a recency effect. A model that recovers those preferences
will beat one that does not, which is what makes the validation numbers mean
something rather than merely exist.
"""

from __future__ import annotations

import argparse
import logging
from dataclasses import dataclass
from pathlib import Path

import numpy as np
import pandas as pd

from config import add_common_arguments, configure_logging

LOGGER = logging.getLogger(__name__)


@dataclass(frozen=True)
class SynthConfig:
    """Shape of the generated corpus."""

    num_users: int = 400
    num_videos: int = 3_000
    num_categories: int = 12
    num_creators: int = 150
    events_per_user_mean: float = 60.0
    #: Span of the generated timeline. The pipeline splits on time, so this has
    #: to be wide enough for a train/validation boundary to be meaningful.
    days: int = 90
    #: Chance a viewer opens something entirely outside their taste. Without it
    #: the log is perfectly separable and every model looks brilliant.
    exploration_rate: float = 0.15
    #: Share of events that are explicit skips (opened, watched almost nothing).
    skip_rate: float = 0.12
    seed: int = 20260729


def _make_catalog(rng: np.random.Generator, cfg: SynthConfig) -> pd.DataFrame:
    """Builds the video catalogue: id, category, creator, duration, quality.

    Args:
        rng: Seeded random generator.
        cfg: Corpus shape.

    Returns:
        One row per video.
    """
    video_ids = [f"vid_{index:06d}" for index in range(cfg.num_videos)]
    # Durations are lognormal: most videos are a few minutes, a long tail runs
    # to an hour. A normal distribution would produce a catalogue of clones.
    durations = np.clip(rng.lognormal(mean=5.6, sigma=0.8, size=cfg.num_videos), 30, 7200)
    return pd.DataFrame(
        {
            "video_id": video_ids,
            "category_id": rng.integers(0, cfg.num_categories, size=cfg.num_videos),
            "creator_id": rng.integers(0, cfg.num_creators, size=cfg.num_videos),
            "video_duration": durations.round().astype(np.int64),
            # Intrinsic watchability, independent of who is watching. This is
            # what `completion_rate_avg` will end up estimating.
            "quality": np.clip(rng.beta(a=2.5, b=2.5, size=cfg.num_videos), 0.05, 0.95),
            "upload_offset_days": rng.uniform(0, cfg.days, size=cfg.num_videos),
        }
    )


def _make_user_tastes(
    rng: np.random.Generator, cfg: SynthConfig
) -> tuple[np.ndarray, list[set[int]]]:
    """Draws each user's category preferences and favourite creators.

    Args:
        rng: Seeded random generator.
        cfg: Corpus shape.

    Returns:
        A ``(num_users, num_categories)`` preference matrix whose rows sum to
        one, and a per-user set of favourite creator ids.
    """
    # A low Dirichlet concentration makes tastes peaked: real viewers care about
    # a few things, not uniformly about everything.
    preferences = rng.dirichlet(alpha=np.full(cfg.num_categories, 0.4), size=cfg.num_users)
    favourites = [
        set(rng.choice(cfg.num_creators, size=rng.integers(2, 7), replace=False).tolist())
        for _ in range(cfg.num_users)
    ]
    return preferences, favourites


def generate_events(cfg: SynthConfig) -> pd.DataFrame:
    """Generates the event log.

    Args:
        cfg: Corpus shape.

    Returns:
        A frame with exactly the production schema: ``user_id``, ``video_id``,
        ``category_id``, ``watched_seconds``, ``video_duration``, ``timestamp``,
        ``liked``, ``skipped``.
    """
    rng = np.random.default_rng(cfg.seed)
    catalog = _make_catalog(rng, cfg)
    preferences, favourites = _make_user_tastes(rng, cfg)

    category_of = catalog["category_id"].to_numpy()
    creator_of = catalog["creator_id"].to_numpy()
    duration_of = catalog["video_duration"].to_numpy()
    quality_of = catalog["quality"].to_numpy()
    upload_offset_of = catalog["upload_offset_days"].to_numpy()

    rows: list[dict[str, object]] = []
    for user_index in range(cfg.num_users):
        event_count = max(
            1, int(rng.poisson(cfg.events_per_user_mean))
        )
        taste = preferences[user_index]
        favourite_creators = favourites[user_index]

        # Sampling weights: a video is likely to be opened in proportion to how
        # much the viewer likes its category, tempered by exploration so the log
        # is not perfectly separable.
        weights = taste[category_of] * (1.0 - cfg.exploration_rate)
        weights += cfg.exploration_rate / cfg.num_videos
        weights /= weights.sum()

        chosen = rng.choice(cfg.num_videos, size=event_count, replace=True, p=weights)
        # Watch order is chronological, which is what the history sequence means.
        offsets_days = np.sort(rng.uniform(0, cfg.days, size=event_count))

        for video_index, offset_days in zip(chosen, offsets_days, strict=True):
            # A video cannot be watched before it was uploaded.
            if offset_days < upload_offset_of[video_index]:
                continue

            affinity = taste[category_of[video_index]] * cfg.num_categories
            creator_bonus = 0.25 if creator_of[video_index] in favourite_creators else 0.0
            # Freshness: interest decays over the weeks after upload.
            age_days = offset_days - upload_offset_of[video_index]
            freshness = float(np.exp(-age_days / 30.0)) * 0.15

            expected = np.clip(
                0.15 + 0.45 * quality_of[video_index] + 0.20 * np.tanh(affinity)
                + creator_bonus + freshness,
                0.02,
                0.99,
            )
            skipped = bool(rng.random() < cfg.skip_rate)
            if skipped:
                ratio = float(np.clip(rng.beta(1.2, 12.0), 0.0, 0.2))
            else:
                # Beta around the expected ratio; concentration 8 keeps it noisy
                # enough that a perfect fit is impossible.
                concentration = 8.0
                ratio = float(
                    rng.beta(expected * concentration, (1 - expected) * concentration)
                )

            watched_seconds = float(ratio) * float(duration_of[video_index])
            rows.append(
                {
                    "user_id": f"user_{user_index:05d}",
                    "video_id": catalog.at[video_index, "video_id"],
                    "category_id": int(category_of[video_index]),
                    "creator_id": int(creator_of[video_index]),
                    "watched_seconds": round(watched_seconds, 2),
                    "video_duration": int(duration_of[video_index]),
                    "timestamp": pd.Timestamp("2026-05-01", tz="UTC")
                    + pd.Timedelta(days=float(offset_days)),
                    "uploaded_at": pd.Timestamp("2026-05-01", tz="UTC")
                    + pd.Timedelta(days=float(upload_offset_of[video_index])),
                    # Liking is rare and correlated with actually finishing.
                    "liked": bool(ratio > 0.8 and rng.random() < 0.35),
                    "skipped": skipped,
                }
            )

    events = pd.DataFrame(rows).sort_values("timestamp").reset_index(drop=True)
    LOGGER.info(
        "generated %d events | %d users | %d videos | %.1f%% skips | %.1f%% likes",
        len(events),
        events["user_id"].nunique(),
        events["video_id"].nunique(),
        100.0 * events["skipped"].mean(),
        100.0 * events["liked"].mean(),
    )
    return events


def main() -> None:
    """CLI entry point: writes a synthetic event log to disk."""
    parser = argparse.ArgumentParser(description=__doc__)
    add_common_arguments(parser)
    parser.add_argument("--num-users", type=int, default=SynthConfig.num_users)
    parser.add_argument("--num-videos", type=int, default=SynthConfig.num_videos)
    parser.add_argument("--days", type=int, default=SynthConfig.days)
    parser.add_argument("--seed", type=int, default=SynthConfig.seed)
    args = parser.parse_args()

    configure_logging(args.log_level)
    cfg = SynthConfig(
        num_users=args.num_users,
        num_videos=args.num_videos,
        days=args.days,
        seed=args.seed,
    )
    events = generate_events(cfg)

    destination: Path = args.events_path
    destination.parent.mkdir(parents=True, exist_ok=True)
    if destination.suffix == ".csv":
        events.to_csv(destination, index=False)
    else:
        events.to_parquet(destination, index=False)
    LOGGER.info("wrote %s", destination)


if __name__ == "__main__":
    main()
