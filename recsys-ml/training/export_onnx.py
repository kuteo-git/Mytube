"""Runs the pipeline and exports every artifact.

Produces five files:

``video_tower.onnx``
    The video tower, with a dynamic batch axis so one graph serves one video or
    ten thousand.
``ranker.onnx``
    The LightGBM regressor, converted with ``onnxmltools``.
``video_embeddings.parquet``
    Every video already embedded. Retrieval never runs the video tower at
    request time — it searches these.
``video_embeddings.json``
    The same table in the format the Go service loads directly, so the serving
    binary needs no Parquet reader.
``feature_spec.json``
    Ranking feature order, metrics and provenance. The Go service validates
    itself against this and refuses a bundle that disagrees.

# Why this runs as two subprocesses

PyTorch ships its own OpenMP runtime; LightGBM links the system's. Loading both
into one process on macOS segfaults — reproducibly, at the first LightGBM
``fit`` after ``import torch``, with no Python traceback. The usual workaround,
``KMP_DUPLICATE_LIB_OK=TRUE``, is documented by Intel as unsafe: it silences
the guard rather than fixing the conflict and can produce wrong numbers with no
indication anything went wrong. Wrong numbers in a training pipeline are worse
than a crash, because a crash is noticed.

So the two trainers never share an interpreter. This module re-invokes itself
once per stage and merges the results, which costs a few seconds of process
startup and removes the failure mode entirely.

Every artifact is written to a temporary name and renamed into place. The
serving service reloads on file change, and rename is atomic where a partial
write is not.
"""

from __future__ import annotations

import argparse
import json
import logging
import shutil
import subprocess
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable

from config import (
    LABEL_COLUMN,
    RANKING_FEATURES,
    PipelineConfig,
    add_common_arguments,
    config_from_args,
    configure_logging,
)

LOGGER = logging.getLogger(__name__)

#: Bumped whenever the artifact contract changes shape. The serving service
#: compares it and refuses a bundle it was not built to read.
ARTIFACT_SCHEMA_VERSION = 1

#: ONNX opset for the towers. 17 carries everything they use and is comfortably
#: supported by current ONNX Runtime releases.
ONNX_OPSET = 17

#: ONNX opset for the ranker. Lower than the towers' because onnxmltools'
#: LightGBM converter refuses anything above 15 — it emits only tree ensemble
#: operators, which have not changed in years, so the older opset costs nothing.
#: Runtimes load a graph per its own declared opset, so the two need not match.
ONNX_OPSET_LIGHTGBM = 15

#: Per-stage metric files, merged into feature_spec.json by the orchestrator.
TOWER_METRICS_FILE = "_towers_metrics.json"
RANKER_METRICS_FILE = "_ranker_metrics.json"


def _atomic_write(target: Path, write: Callable[[Path], None]) -> None:
    """Writes via a temporary file and renames into place.

    Args:
        target: Final path.
        write: Callable given the temporary path to write to.
    """
    target.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(
        dir=target.parent, suffix=target.suffix, delete=False
    ) as handle:
        temporary = Path(handle.name)
    try:
        write(temporary)
        shutil.move(str(temporary), str(target))
    finally:
        temporary.unlink(missing_ok=True)


# --------------------------------------------------------------------------
# Stage 1 — towers. Imports torch. Must not import lightgbm.
# --------------------------------------------------------------------------


def run_towers_stage(config: PipelineConfig) -> None:
    """Trains the two-tower model, exports it, and embeds the catalogue.

    Args:
        config: Pipeline configuration.
    """
    import numpy as np
    import torch

    from data_prep import PAD_INDEX, prepare
    from two_tower_model import train_two_tower

    prepared = prepare(config.data)
    trained = train_two_tower(prepared, config.two_tower, config.data.max_history_length)

    artifacts = config.artifacts_dir
    tower = trained.video_tower
    tower.eval()

    def write_tower(path: Path) -> None:
        torch.onnx.export(
            tower,
            (torch.tensor([1], dtype=torch.int64), torch.tensor([0], dtype=torch.int64)),
            str(path),
            input_names=["video_index", "category_index"],
            # Not "embedding": the exporter already names internal values after
            # the nn.Embedding submodules, and reusing the bare word produces a
            # graph with a duplicate definition that ONNX Runtime rejects at
            # load time — an invalid artifact that nothing before deployment
            # would have caught.
            output_names=["video_embedding"],
            dynamic_axes={
                "video_index": {0: "batch"},
                "category_index": {0: "batch"},
                "video_embedding": {0: "batch"},
            },
            opset_version=ONNX_OPSET,
            # Weights inline, not in a sidecar `.onnx.data` file. External data
            # is referenced by filename, and this graph is written under a
            # temporary name before being renamed into place — so the sidecar
            # would keep the temporary name and the renamed graph would point at
            # a file nobody can find. The tower is a couple of megabytes; there
            # is nothing to gain by splitting it.
            external_data=False,
        )

    _atomic_write(artifacts / "video_tower.onnx", write_tower)
    LOGGER.info("wrote %s", artifacts / "video_tower.onnx")

    # Embed the whole catalogue once, offline. This is what makes retrieval a
    # dot product at request time instead of a forward pass per candidate.
    catalog = prepared.catalog
    indices = prepared.video_vocab.encode(catalog["video_id"])
    categories = catalog["category_id"].astype(np.int64).to_numpy()
    known = indices != PAD_INDEX
    if not known.all():
        LOGGER.warning(
            "%d catalogue videos are outside the trained vocabulary and cannot "
            "be retrieved until the next training run",
            int((~known).sum()),
        )

    vectors = []
    with torch.no_grad():
        for start in range(0, len(indices), config.two_tower.batch_size):
            stop = start + config.two_tower.batch_size
            vectors.append(
                tower(
                    torch.from_numpy(indices[start:stop].copy()),
                    torch.from_numpy(categories[start:stop].copy()),
                ).numpy()
            )
    embeddings = np.concatenate(vectors, axis=0)

    frame = catalog.copy()
    frame["embedding"] = [row.astype(np.float32).tolist() for row in embeddings]
    frame = frame.loc[known].reset_index(drop=True)

    _atomic_write(
        artifacts / "video_embeddings.parquet", lambda path: frame.to_parquet(path, index=False)
    )
    _atomic_write(
        artifacts / "video_embeddings.json",
        lambda path: path.write_text(_embeddings_json(frame), encoding="utf-8"),
    )
    LOGGER.info(
        "wrote embeddings | %d videos × %d dims",
        len(frame),
        config.two_tower.embedding_dim,
    )

    _atomic_write(
        artifacts / TOWER_METRICS_FILE,
        lambda path: path.write_text(
            json.dumps({"two_tower_validation_recall": trained.best_recall}), encoding="utf-8"
        ),
    )


def _embeddings_json(frame: Any) -> str:
    """Renders the embedding table in the shape the Go service reads.

    Args:
        frame: Catalogue frame carrying an ``embedding`` column.

    Returns:
        A JSON array of objects.
    """
    import pandas as pd

    rows = []
    for record in frame.to_dict("records"):
        uploaded_at = record.get("uploaded_at")
        uploaded_unix = 0
        if uploaded_at is not None and not pd.isna(uploaded_at):
            uploaded_unix = int(pd.Timestamp(uploaded_at).timestamp())
        rows.append(
            {
                "video_id": str(record["video_id"]),
                "embedding": [float(value) for value in record["embedding"]],
                "completion_rate_avg": float(record.get("completion_rate_avg", 0.0)),
                "uploaded_at_unix": uploaded_unix,
                "creator_id": str(record.get("creator_id", "")),
                "category_id": int(record.get("category_id", 0)),
            }
        )
    return json.dumps(rows)


# --------------------------------------------------------------------------
# Stage 2 — ranker. Imports lightgbm. Must not import torch.
# --------------------------------------------------------------------------


def run_ranker_stage(config: PipelineConfig) -> None:
    """Trains the ranking model and converts it to ONNX.

    Args:
        config: Pipeline configuration.

    Raises:
        ImportError: If the ONNX conversion packages are missing.
    """
    import pandas as pd

    from data_prep import prepare
    from ranking_model import build_and_train

    artifacts = config.artifacts_dir
    embeddings_path = artifacts / "video_embeddings.parquet"
    embeddings = pd.read_parquet(embeddings_path) if embeddings_path.exists() else None
    if embeddings is None:
        LOGGER.warning(
            "no embeddings at %s; candidate_score will be a dead column", embeddings_path
        )

    prepared = prepare(config.data)
    trained = build_and_train(prepared, config.ranking, embeddings)

    try:
        from onnxmltools import convert_lightgbm
        from onnxmltools.convert.common.data_types import FloatTensorType
    except ImportError as error:  # pragma: no cover - environment problem
        raise ImportError(
            "onnxmltools is required to export the ranker; "
            "install it with `pip install -r training/requirements.txt`"
        ) from error

    onnx_model = convert_lightgbm(
        trained.model.booster_,
        initial_types=[("input", FloatTensorType([None, len(trained.feature_names)]))],
        target_opset=ONNX_OPSET_LIGHTGBM,
    )
    _atomic_write(
        artifacts / "ranker.onnx", lambda path: path.write_bytes(onnx_model.SerializeToString())
    )
    LOGGER.info("wrote %s", artifacts / "ranker.onnx")

    _atomic_write(
        artifacts / RANKER_METRICS_FILE,
        lambda path: path.write_text(
            json.dumps(
                {
                    "ranker_validation_rmse": trained.validation_rmse,
                    "feature_importance": trained.feature_importance,
                }
            ),
            encoding="utf-8",
        ),
    )


# --------------------------------------------------------------------------
# Orchestration
# --------------------------------------------------------------------------


def _read_metrics(path: Path) -> dict[str, Any]:
    """Reads a stage's metrics file, tolerating its absence."""
    if not path.exists():
        return {}
    return json.loads(path.read_text(encoding="utf-8"))


def write_feature_spec(config: PipelineConfig) -> None:
    """Merges the stage metrics and writes the serving contract.

    Args:
        config: Pipeline configuration.
    """
    artifacts = config.artifacts_dir
    metrics = {
        **_read_metrics(artifacts / TOWER_METRICS_FILE),
        **_read_metrics(artifacts / RANKER_METRICS_FILE),
    }

    spec: dict[str, Any] = {
        "schema_version": ARTIFACT_SCHEMA_VERSION,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "label": LABEL_COLUMN,
        "ranking_features": list(RANKING_FEATURES),
        "embedding_dim": config.two_tower.embedding_dim,
        "metrics": metrics,
        "config": config.as_dict(),
    }
    _atomic_write(
        artifacts / "feature_spec.json",
        lambda path: path.write_text(
            json.dumps(spec, indent=2, sort_keys=True), encoding="utf-8"
        ),
    )
    LOGGER.info("wrote %s", artifacts / "feature_spec.json")

    for name in (TOWER_METRICS_FILE, RANKER_METRICS_FILE):
        (artifacts / name).unlink(missing_ok=True)


def _run_stage_subprocess(stage: str, argv: list[str]) -> None:
    """Re-invokes this module for one stage, in a fresh interpreter.

    Args:
        stage: ``towers`` or ``ranker``.
        argv: Arguments to forward.

    Raises:
        RuntimeError: If the stage exits non-zero. The message names the signal
            for a crash, because a segfault here has a known cause and saying so
            saves the next person the same afternoon.
    """
    command = [sys.executable, str(Path(__file__).resolve()), "--stage", stage, *argv]
    LOGGER.info("running stage %s in a separate interpreter", stage)
    result = subprocess.run(command, check=False)
    if result.returncode == 0:
        return
    if result.returncode < 0 or result.returncode > 128:
        raise RuntimeError(
            f"stage {stage} crashed (exit {result.returncode}). If this is a "
            "segfault, check that torch and lightgbm are not being imported "
            "into the same process — they ship conflicting OpenMP runtimes."
        )
    raise RuntimeError(f"stage {stage} failed with exit code {result.returncode}")


def main() -> None:
    """CLI entry point."""
    parser = argparse.ArgumentParser(description=__doc__)
    add_common_arguments(parser)
    parser.add_argument(
        "--stage",
        choices=("towers", "ranker"),
        default=None,
        help="Run a single stage. Omit to run the whole pipeline.",
    )
    parser.add_argument("--embedding-dim", type=int, default=None)
    parser.add_argument("--batch-size", type=int, default=None)
    parser.add_argument("--epochs", type=int, default=None)
    args = parser.parse_args()

    configure_logging(args.log_level)
    stage = args.stage
    for name in ("embedding_dim", "batch_size", "epochs"):
        if getattr(args, name) is None:
            delattr(args, name)
    delattr(args, "stage")
    config = config_from_args(args)

    if stage == "towers":
        run_towers_stage(config)
        return
    if stage == "ranker":
        run_ranker_stage(config)
        return

    forwarded = [
        "--events-path", str(config.data.events_path),
        "--artifacts-dir", str(config.artifacts_dir),
        "--log-level", config.log_level,
    ]
    _run_stage_subprocess("towers", forwarded)
    _run_stage_subprocess("ranker", forwarded)
    write_feature_spec(config)
    LOGGER.info("pipeline complete | artifacts in %s", config.artifacts_dir)


if __name__ == "__main__":
    main()
