"""Turns a raw event log into the tables both models train on.

Two things here are load-bearing.

The split is by **time**, never at random. A random split lets a user's later
behaviour sit in training while their earlier behaviour is scored in
validation — the model gets to see the future of the very user it is asked to
predict, and every metric comes out flattering and wrong. Splitting on a
timestamp reproduces the only situation that ever occurs in production:
predicting forward from a fixed moment.

The watch history attached to each row is built from **strictly earlier** events
by that user. Building it from the whole log would leak the answer into the
question, since the video being predicted would appear in its own history.
"""

from __future__ import annotations

import argparse
import logging
from dataclasses import dataclass
from pathlib import Path

import numpy as np
import pandas as pd

from config import (
    LABEL_COLUMN,
    DataConfig,
    add_common_arguments,
    config_from_args,
    configure_logging,
)

LOGGER = logging.getLogger(__name__)

#: Columns an event log must provide. Anything else is optional enrichment.
REQUIRED_COLUMNS: tuple[str, ...] = (
    "user_id",
    "video_id",
    "category_id",
    "watched_seconds",
    "video_duration",
    "timestamp",
    "liked",
    "skipped",
)

#: Reserved index 0 in every vocabulary, used to pad short histories.
PAD_TOKEN = "<pad>"
PAD_INDEX = 0


class SchemaError(ValueError):
    """Raised when the event log does not carry the columns the pipeline needs."""


@dataclass
class Vocabulary:
    """Maps string ids to the contiguous integers an embedding table needs."""

    tokens: list[str]
    index_of: dict[str, int]

    @classmethod
    def build(cls, values: pd.Series) -> "Vocabulary":
        """Builds a vocabulary from a column, reserving index 0 for padding.

        Args:
            values: Column of ids. Order is sorted for reproducibility.

        Returns:
            A vocabulary whose index 0 is always :data:`PAD_TOKEN`.
        """
        unique = sorted(str(value) for value in values.dropna().unique())
        tokens = [PAD_TOKEN, *unique]
        return cls(tokens=tokens, index_of={token: i for i, token in enumerate(tokens)})

    def __len__(self) -> int:
        return len(self.tokens)

    def encode(self, values: pd.Series) -> np.ndarray:
        """Encodes a column of ids, mapping anything unseen to the pad index.

        Args:
            values: Column of ids.

        Returns:
            An ``int64`` array of the same length.
        """
        return values.astype(str).map(lambda v: self.index_of.get(v, PAD_INDEX)).to_numpy(
            dtype=np.int64
        )


@dataclass
class PreparedData:
    """Everything downstream stages need, already split."""

    train: pd.DataFrame
    validation: pd.DataFrame
    video_vocab: Vocabulary
    user_vocab: Vocabulary
    #: One row per video: the static side of the catalogue, used both to build
    #: the video tower's inputs and to embed the whole catalogue at export time.
    catalog: pd.DataFrame
    split_at: pd.Timestamp


def load_events(path: Path) -> pd.DataFrame:
    """Reads an event log from Parquet or CSV and validates its schema.

    Args:
        path: File to read. Suffix decides the reader.

    Returns:
        The event log with ``timestamp`` parsed as UTC.

    Raises:
        FileNotFoundError: If ``path`` does not exist.
        SchemaError: If any required column is missing.
    """
    if not path.exists():
        raise FileNotFoundError(f"event log not found: {path}")

    events = pd.read_csv(path) if path.suffix == ".csv" else pd.read_parquet(path)
    missing = [column for column in REQUIRED_COLUMNS if column not in events.columns]
    if missing:
        raise SchemaError(f"event log {path} is missing columns: {', '.join(missing)}")

    events["timestamp"] = pd.to_datetime(events["timestamp"], utc=True)
    LOGGER.info("loaded %d events from %s", len(events), path)
    return events


def compute_watch_ratio(events: pd.DataFrame, cfg: DataConfig) -> pd.DataFrame:
    """Adds the ``watch_ratio`` label.

    Ratios are clipped at :attr:`DataConfig.max_watch_ratio`. Rewatching part of
    a video can push watched seconds past the duration, and an unclipped label
    lets a handful of loops dominate a regression that is otherwise bounded.

    Rows with a non-positive duration cannot produce a ratio and are dropped
    rather than silently given one.

    Args:
        events: Raw event log.
        cfg: Data configuration.

    Returns:
        A copy with ``watch_ratio`` added and unusable rows removed.
    """
    usable = events[events["video_duration"] > 0].copy()
    dropped = len(events) - len(usable)
    if dropped:
        LOGGER.warning("dropped %d events with a non-positive duration", dropped)

    ratio = usable["watched_seconds"] / usable["video_duration"]
    usable[LABEL_COLUMN] = ratio.clip(lower=0.0, upper=cfg.max_watch_ratio).astype(
        np.float32
    )
    LOGGER.info(
        "watch_ratio | mean %.3f | median %.3f | p90 %.3f",
        usable[LABEL_COLUMN].mean(),
        usable[LABEL_COLUMN].median(),
        usable[LABEL_COLUMN].quantile(0.9),
    )
    return usable


def build_histories(events: pd.DataFrame, cfg: DataConfig) -> pd.DataFrame:
    """Attaches each event the watch history that preceded it.

    The history for a row contains only videos that user watched *strictly
    before* that row's timestamp, most recent last, truncated to
    :attr:`DataConfig.max_history_length`. Anything else would leak the label.

    Args:
        events: Event log carrying ``watch_ratio``.
        cfg: Data configuration.

    Returns:
        A copy ordered by user and time with a ``history`` column of video id
        lists. Rows whose history would be empty are kept: a first watch is a
        real, common situation the model has to handle.
    """
    ordered = events.sort_values(["user_id", "timestamp"]).reset_index(drop=True)

    histories: list[list[str]] = []
    for _, group in ordered.groupby("user_id", sort=False):
        seen: list[str] = []
        for video_id, ratio in zip(
            group["video_id"].astype(str), group[LABEL_COLUMN], strict=True
        ):
            histories.append(seen[-cfg.max_history_length :].copy())
            # Only a genuine watch belongs in a taste history. Counting a bounce
            # would teach the user tower that opening and leaving is interest.
            if ratio >= cfg.min_watch_ratio_positive:
                seen.append(video_id)

    ordered["history"] = histories
    LOGGER.info(
        "built histories | mean length %.1f | empty %.1f%%",
        float(np.mean([len(h) for h in histories])) if histories else 0.0,
        100.0 * float(np.mean([len(h) == 0 for h in histories])) if histories else 0.0,
    )
    return ordered


def split_by_time(
    events: pd.DataFrame, cfg: DataConfig
) -> tuple[pd.DataFrame, pd.DataFrame, pd.Timestamp]:
    """Splits the log at a timestamp quantile.

    Args:
        events: Event log with histories attached.
        cfg: Data configuration.

    Returns:
        The training frame, the validation frame, and the boundary timestamp.
    """
    boundary = events["timestamp"].quantile(cfg.train_fraction)
    train = events[events["timestamp"] <= boundary].copy()
    validation = events[events["timestamp"] > boundary].copy()
    LOGGER.info(
        "time split at %s | train %d | validation %d", boundary, len(train), len(validation)
    )
    if validation.empty:
        LOGGER.warning(
            "validation split is empty; the log may span too short a period to "
            "split on time at train_fraction=%.2f",
            cfg.train_fraction,
        )
    return train, validation, boundary


def build_catalog(events: pd.DataFrame) -> pd.DataFrame:
    """Derives the per-video table from the event log.

    ``completion_rate_avg`` is computed here rather than in the serving path on
    purpose: it is an aggregate over history, and recomputing it per request
    would put a full table scan inside a latency budget measured in
    milliseconds.

    Args:
        events: Event log carrying ``watch_ratio``.

    Returns:
        One row per video, with metadata and its average completion rate.
    """
    aggregations: dict[str, tuple[str, str]] = {
        "category_id": ("category_id", "first"),
        "video_duration": ("video_duration", "first"),
        "completion_rate_avg": (LABEL_COLUMN, "mean"),
        "watch_count": (LABEL_COLUMN, "size"),
    }
    if "creator_id" in events.columns:
        aggregations["creator_id"] = ("creator_id", "first")
    if "uploaded_at" in events.columns:
        aggregations["uploaded_at"] = ("uploaded_at", "first")

    catalog = events.groupby("video_id", sort=True).agg(**aggregations).reset_index()
    LOGGER.info("catalog | %d videos", len(catalog))
    return catalog


def prepare(cfg: DataConfig) -> PreparedData:
    """Runs the whole preparation, from raw log to split frames.

    Args:
        cfg: Data configuration.

    Returns:
        The prepared dataset.
    """
    events = load_events(cfg.events_path)
    events = compute_watch_ratio(events, cfg)

    # Users too sparse to form a history teach the user tower nothing and add
    # noise to in-batch negatives.
    counts = events.groupby("user_id")["video_id"].transform("size")
    before = len(events)
    events = events[counts >= cfg.min_events_per_user].copy()
    if len(events) != before:
        LOGGER.info(
            "dropped %d events from users with fewer than %d events",
            before - len(events),
            cfg.min_events_per_user,
        )

    events = build_histories(events, cfg)
    catalog = build_catalog(events)
    train, validation, boundary = split_by_time(events, cfg)

    return PreparedData(
        train=train,
        validation=validation,
        video_vocab=Vocabulary.build(catalog["video_id"]),
        user_vocab=Vocabulary.build(events["user_id"]),
        catalog=catalog,
        split_at=boundary,
    )


def main() -> None:
    """CLI entry point: prepares data and reports what it produced."""
    parser = argparse.ArgumentParser(description=__doc__)
    add_common_arguments(parser)
    parser.add_argument("--train-fraction", type=float, default=DataConfig.train_fraction)
    parser.add_argument(
        "--max-history-length", type=int, default=DataConfig.max_history_length
    )
    args = parser.parse_args()

    configure_logging(args.log_level)
    prepared = prepare(config_from_args(args).data)
    LOGGER.info(
        "prepared | videos %d | users %d | split %s",
        len(prepared.video_vocab) - 1,
        len(prepared.user_vocab) - 1,
        prepared.split_at,
    )


if __name__ == "__main__":
    main()
