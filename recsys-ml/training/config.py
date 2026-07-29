"""Configuration for the recommendation training pipeline.

Every tunable lives here rather than inline in the modules, so a run is fully
described by its config and nothing has to be found by reading code.

The one value that is *not* a free choice is the feature order used by the
ranking model: it is written to disk as part of the artifacts and read back by
the Go serving service. Training and serving disagreeing about which column is
which is the classic way a recommender silently rots, so the order is data, not
a convention that both sides are trusted to remember.
"""

from __future__ import annotations

import argparse
import logging
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

LOGGER = logging.getLogger(__name__)

#: Ranking features, in the exact order the model's input vector expects.
#: Exported to ``feature_spec.json`` and validated by the serving service.
RANKING_FEATURES: tuple[str, ...] = (
    "candidate_score",
    "completion_rate_avg",
    "hours_since_upload",
    "user_creator_affinity",
)

#: Name of the label. Deliberately watch ratio and never a click: a click is a
#: promise, watch time is what was actually delivered.
LABEL_COLUMN = "watch_ratio"


@dataclass(frozen=True)
class DataConfig:
    """Where events come from and how they are turned into training rows."""

    events_path: Path = Path("data/events.parquet")
    #: Fraction of the timeline used for training. The remainder validates.
    #: Split is by time, never at random: a random split lets the model see the
    #: future of a user it is then asked to predict, and scores come out
    #: flattering and wrong.
    train_fraction: float = 0.8
    #: Longest watch history fed to the user tower. Older events are dropped
    #: rather than summarised, because recency dominates what to play next.
    max_history_length: int = 50
    #: Watch ratios above this are clipped. Rewatching a chorus can push the
    #: ratio past 1.0, which would otherwise dominate a regression label.
    max_watch_ratio: float = 1.0
    #: Rows shorter than this are treated as "did not really watch" and kept as
    #: negatives rather than dropped, so the model learns what a bounce is.
    min_watch_ratio_positive: float = 0.25
    #: Users with fewer events than this cannot form a history and are dropped.
    min_events_per_user: int = 5


@dataclass(frozen=True)
class TwoTowerConfig:
    """Candidate generation model."""

    embedding_dim: int = 128
    #: 256 is a deliberate ceiling, not a default copied from a paper. This
    #: trains on a Mac mini with 24 GiB of unified memory that is also running
    #: Postgres, four Go services and a Vite dev server. Unified memory means
    #: the GPU is competing with all of them for the same pool, and MPS reports
    #: allocation failures rather than swapping, so a batch that is merely
    #: ambitious kills the run outright.
    batch_size: int = 256
    epochs: int = 10
    learning_rate: float = 1e-3
    weight_decay: float = 1e-5
    #: Softmax temperature over the similarity matrix. Below 1.0 sharpens the
    #: distribution, which helps when the catalogue is small and embeddings
    #: start out crowded.
    temperature: float = 0.07
    #: Hidden width of each tower's projection head.
    hidden_dim: int = 256
    dropout: float = 0.1
    #: Stop when validation recall@k has not improved for this many epochs.
    patience: int = 3
    #: k used for the validation recall metric.
    eval_k: int = 10
    seed: int = 20260729


@dataclass(frozen=True)
class RankingConfig:
    """LightGBM regression on watch ratio."""

    num_leaves: int = 31
    learning_rate: float = 0.05
    n_estimators: int = 500
    min_child_samples: int = 20
    subsample: float = 0.9
    subsample_freq: int = 1
    colsample_bytree: float = 0.9
    reg_lambda: float = 1.0
    early_stopping_rounds: int = 50
    seed: int = 20260729


@dataclass(frozen=True)
class PipelineConfig:
    """Everything one training run needs."""

    data: DataConfig = field(default_factory=DataConfig)
    two_tower: TwoTowerConfig = field(default_factory=TwoTowerConfig)
    ranking: RankingConfig = field(default_factory=RankingConfig)
    artifacts_dir: Path = Path("artifacts")
    log_level: str = "INFO"

    def as_dict(self) -> dict[str, Any]:
        """Returns a JSON-serialisable view, for logging and for provenance."""
        return _stringify_paths(asdict(self))


def _stringify_paths(value: Any) -> Any:
    """Recursively turns ``Path`` values into strings so ``json`` accepts them."""
    if isinstance(value, dict):
        return {key: _stringify_paths(item) for key, item in value.items()}
    if isinstance(value, Path):
        return str(value)
    return value


def configure_logging(level: str = "INFO") -> None:
    """Sets up module-level logging.

    Args:
        level: Any name accepted by :mod:`logging`, e.g. ``"DEBUG"``.
    """
    logging.basicConfig(
        level=getattr(logging, level.upper(), logging.INFO),
        format="%(asctime)s %(levelname)-8s %(name)s | %(message)s",
        datefmt="%H:%M:%S",
    )


def add_common_arguments(parser: argparse.ArgumentParser) -> argparse.ArgumentParser:
    """Adds the arguments every stage of the pipeline understands.

    Args:
        parser: Parser to extend.

    Returns:
        The same parser, for chaining.
    """
    parser.add_argument(
        "--events-path",
        type=Path,
        default=DataConfig.events_path,
        help="Parquet or CSV event log to train on.",
    )
    parser.add_argument(
        "--artifacts-dir",
        type=Path,
        default=PipelineConfig.artifacts_dir,
        help="Directory for models, embeddings and the feature spec.",
    )
    parser.add_argument(
        "--log-level",
        default=PipelineConfig.log_level,
        help="Logging level (DEBUG, INFO, WARNING, ERROR).",
    )
    return parser


def config_from_args(args: argparse.Namespace) -> PipelineConfig:
    """Builds a :class:`PipelineConfig` from parsed arguments.

    Arguments absent from ``args`` fall back to the dataclass defaults, so a
    stage may expose only the flags it actually uses.

    Args:
        args: Namespace produced by :func:`argparse.ArgumentParser.parse_args`.

    Returns:
        A fully populated configuration.
    """
    data = DataConfig(
        events_path=getattr(args, "events_path", DataConfig.events_path),
        train_fraction=getattr(args, "train_fraction", DataConfig.train_fraction),
        max_history_length=getattr(
            args, "max_history_length", DataConfig.max_history_length
        ),
    )
    two_tower = TwoTowerConfig(
        embedding_dim=getattr(args, "embedding_dim", TwoTowerConfig.embedding_dim),
        batch_size=getattr(args, "batch_size", TwoTowerConfig.batch_size),
        epochs=getattr(args, "epochs", TwoTowerConfig.epochs),
        learning_rate=getattr(args, "learning_rate", TwoTowerConfig.learning_rate),
    )
    return PipelineConfig(
        data=data,
        two_tower=two_tower,
        ranking=RankingConfig(),
        artifacts_dir=getattr(args, "artifacts_dir", PipelineConfig.artifacts_dir),
        log_level=getattr(args, "log_level", PipelineConfig.log_level),
    )
