"""Ranking model: LightGBM regression on watch ratio.

Ranking is kept strictly separate from candidate generation, and the separation
is not organisational tidiness. The two answer different questions on different
budgets: retrieval must score a whole catalogue, so it is restricted to a dot
product between precomputed vectors; ranking sees a few hundred survivors and
can afford features that retrieval cannot express at all — how fresh the upload
is, how well the video holds an audience, how much this viewer likes this
creator. Folding them into one model would force the expensive features onto
every item in the catalogue, or throw them away. Neither is acceptable.

The label is watch ratio, never a click. A click measures a thumbnail; watch
ratio measures whether the video was worth opening.
"""

from __future__ import annotations

import argparse
import logging
from dataclasses import dataclass
from pathlib import Path

import lightgbm as lgb
import numpy as np
import pandas as pd

from config import (
    LABEL_COLUMN,
    RANKING_FEATURES,
    RankingConfig,
    add_common_arguments,
    config_from_args,
    configure_logging,
)
from data_prep import PreparedData, prepare

LOGGER = logging.getLogger(__name__)

#: Seconds in an hour, for the ``hours_since_upload`` feature.
SECONDS_PER_HOUR = 3600.0


@dataclass
class TrainedRanker:
    """A fitted ranker and the numbers describing how it did."""

    model: lgb.LGBMRegressor
    feature_names: tuple[str, ...]
    validation_rmse: float
    feature_importance: dict[str, float]


def compute_candidate_scores(
    events: pd.DataFrame, embeddings: pd.DataFrame
) -> pd.Series:
    """Recomputes retrieval scores exactly the way the serving path will.

    For each row: average the embeddings of the videos in that row's history,
    normalise, and take the cosine with the candidate's embedding. That is
    precisely what ``recommendation.retrieve`` does in Go, and doing the same
    arithmetic here is the point — a ``candidate_score`` derived any other way
    would train the ranker on a feature that does not exist at serving time.

    Args:
        events: Prepared events carrying a ``history`` column.
        embeddings: Frame of ``video_id`` and ``embedding`` from the exported
            catalogue.

    Returns:
        Cosine similarity per row, ``0.0`` where the history is empty or none of
        it is in the index — which is the same neutral value serving falls back
        to when it cannot build a query vector.
    """
    lookup = {
        str(video_id): np.asarray(vector, dtype=np.float32)
        for video_id, vector in zip(
            embeddings["video_id"], embeddings["embedding"], strict=True
        )
    }

    scores = np.zeros(len(events), dtype=np.float32)
    for position, (history, video_id) in enumerate(
        zip(events["history"], events["video_id"].astype(str), strict=True)
    ):
        target = lookup.get(video_id)
        if target is None:
            continue
        watched = [lookup[str(h)] for h in history if str(h) in lookup]
        if not watched:
            continue
        query = np.mean(watched, axis=0)
        magnitude = float(np.linalg.norm(query))
        if magnitude == 0.0:
            continue
        scores[position] = float(np.dot(query / magnitude, target))

    covered = float((scores != 0).mean()) if len(scores) else 0.0
    LOGGER.info("candidate_score computed for %.1f%% of rows", 100.0 * covered)
    return pd.Series(scores, index=events.index, dtype=np.float32)


def build_ranking_features(
    events: pd.DataFrame,
    catalog: pd.DataFrame,
    candidate_scores: pd.Series | None = None,
) -> pd.DataFrame:
    """Assembles the ranking feature matrix.

    Every feature here is an aggregate or a lookup, never a per-request
    computation. That is a serving constraint reaching back into training: the
    Go service reads these values from its feature store, so anything training
    derives on the fly would have no counterpart at inference time and the two
    would silently diverge.

    Args:
        events: Prepared events, carrying ``watch_ratio`` and ``timestamp``.
        catalog: Per-video table from :func:`data_prep.build_catalog`.
        candidate_scores: Optional retrieval scores aligned to ``events``. When
            absent — the first training run, before a two-tower model exists —
            the column is filled with a neutral value rather than dropped, so
            the feature order stays fixed across runs.

    Returns:
        A frame whose columns are exactly :data:`config.RANKING_FEATURES`, in
        that order, plus the label.
    """
    merged = events.merge(
        catalog[["video_id", "completion_rate_avg"]], on="video_id", how="left"
    )

    if candidate_scores is not None:
        merged["candidate_score"] = candidate_scores.to_numpy()
    else:
        LOGGER.warning(
            "no candidate scores supplied; filling with 0.0. The ranker will "
            "learn nothing from that feature until retrieval scores are joined in."
        )
        merged["candidate_score"] = 0.0

    if "uploaded_at" in merged.columns:
        age = merged["timestamp"] - pd.to_datetime(merged["uploaded_at"], utc=True)
        merged["hours_since_upload"] = (
            age.dt.total_seconds() / SECONDS_PER_HOUR
        ).clip(lower=0.0)
    else:
        LOGGER.warning("event log has no uploaded_at; hours_since_upload will be 0")
        merged["hours_since_upload"] = 0.0

    merged["user_creator_affinity"] = _creator_affinity(merged)

    # completion_rate_avg is missing for videos the split never saw. The neutral
    # fill is the global mean rather than zero: zero would assert that nobody
    # finishes them, which is a claim the data does not make.
    merged["completion_rate_avg"] = merged["completion_rate_avg"].fillna(
        merged[LABEL_COLUMN].mean()
    )

    features = merged[list(RANKING_FEATURES)].astype(np.float32)
    features[LABEL_COLUMN] = merged[LABEL_COLUMN].astype(np.float32).to_numpy()
    return features


def _creator_affinity(events: pd.DataFrame) -> pd.Series:
    """Computes how much each viewer has liked each creator so far.

    Uses an expanding mean over that user's *earlier* watches of the creator, so
    the value never contains the row it helps predict. A plain groupby mean
    would leak the label straight into the feature and produce a validation
    score that collapses the moment it meets real traffic.

    Args:
        events: Prepared events, sorted by user and time.

    Returns:
        Affinity per row, with a viewer's first encounter of a creator set to
        the global mean watch ratio.
    """
    if "creator_id" not in events.columns:
        LOGGER.warning("event log has no creator_id; user_creator_affinity will be neutral")
        return pd.Series(events[LABEL_COLUMN].mean(), index=events.index, dtype=np.float32)

    ordered = events.sort_values(["user_id", "creator_id", "timestamp"])
    expanding = (
        ordered.groupby(["user_id", "creator_id"])[LABEL_COLUMN]
        .transform(lambda values: values.shift(1).expanding().mean())
    )
    return (
        expanding.reindex(events.index)
        .fillna(events[LABEL_COLUMN].mean())
        .astype(np.float32)
    )


def train_ranker(
    train_features: pd.DataFrame, validation_features: pd.DataFrame, cfg: RankingConfig
) -> TrainedRanker:
    """Fits the LightGBM regressor with early stopping.

    Args:
        train_features: Training matrix from :func:`build_ranking_features`.
        validation_features: Validation matrix, from a strictly later period.
        cfg: Ranking hyper-parameters.

    Returns:
        The fitted model and its scores.

    Raises:
        ValueError: If either split is empty.
    """
    if train_features.empty:
        raise ValueError("training split is empty; nothing to fit")
    if validation_features.empty:
        raise ValueError(
            "validation split is empty; early stopping needs a later period to "
            "score against. Widen the event log or lower train_fraction."
        )

    columns = list(RANKING_FEATURES)
    model = lgb.LGBMRegressor(
        objective="regression",
        num_leaves=cfg.num_leaves,
        learning_rate=cfg.learning_rate,
        n_estimators=cfg.n_estimators,
        min_child_samples=cfg.min_child_samples,
        subsample=cfg.subsample,
        subsample_freq=cfg.subsample_freq,
        colsample_bytree=cfg.colsample_bytree,
        reg_lambda=cfg.reg_lambda,
        random_state=cfg.seed,
        verbose=-1,
    )
    model.fit(
        train_features[columns],
        train_features[LABEL_COLUMN],
        eval_set=[(validation_features[columns], validation_features[LABEL_COLUMN])],
        eval_metric="rmse",
        callbacks=[
            lgb.early_stopping(cfg.early_stopping_rounds, verbose=False),
            lgb.log_evaluation(period=0),
        ],
    )

    predictions = model.predict(validation_features[columns])
    rmse = float(np.sqrt(np.mean((predictions - validation_features[LABEL_COLUMN]) ** 2)))

    importance = dict(
        zip(columns, (float(v) for v in model.feature_importances_), strict=True)
    )
    LOGGER.info("validation RMSE %.4f (best iteration %s)", rmse, model.best_iteration_)
    # A feature contributing nothing is worth knowing about: it usually means it
    # is constant, or that the serving side is not really supplying it.
    for name, value in sorted(importance.items(), key=lambda kv: -kv[1]):
        LOGGER.info("feature importance | %-24s %8.1f", name, value)

    return TrainedRanker(
        model=model,
        feature_names=RANKING_FEATURES,
        validation_rmse=rmse,
        feature_importance=importance,
    )


def build_and_train(
    prepared: PreparedData,
    cfg: RankingConfig,
    embeddings: pd.DataFrame | None = None,
) -> TrainedRanker:
    """Builds features from prepared data and fits the ranker.

    Args:
        prepared: Output of :func:`data_prep.prepare`.
        cfg: Ranking hyper-parameters.
        embeddings: Exported catalogue embeddings. Supplying them makes
            ``candidate_score`` a real feature instead of a constant; without
            them the ranker is fitted on three features and one dead column.

    Returns:
        The fitted ranker.
    """
    train_scores = (
        compute_candidate_scores(prepared.train, embeddings)
        if embeddings is not None
        else None
    )
    validation_scores = (
        compute_candidate_scores(prepared.validation, embeddings)
        if embeddings is not None
        else None
    )
    train_features = build_ranking_features(prepared.train, prepared.catalog, train_scores)
    validation_features = build_ranking_features(
        prepared.validation, prepared.catalog, validation_scores
    )
    LOGGER.info(
        "ranking matrices | train %d rows | validation %d rows",
        len(train_features),
        len(validation_features),
    )
    return train_ranker(train_features, validation_features, cfg)


def main() -> None:
    """CLI entry point: trains the ranker and saves it."""
    parser = argparse.ArgumentParser(description=__doc__)
    add_common_arguments(parser)
    args = parser.parse_args()

    configure_logging(args.log_level)
    cfg = config_from_args(args)
    prepared = prepare(cfg.data)
    trained = build_and_train(prepared, cfg.ranking)

    destination: Path = cfg.artifacts_dir / "ranker.txt"
    destination.parent.mkdir(parents=True, exist_ok=True)
    trained.model.booster_.save_model(str(destination))
    LOGGER.info("wrote %s", destination)


if __name__ == "__main__":
    main()
