"""Two-tower candidate generation model.

The user tower turns a watch history into a taste vector; the video tower turns
a video's identity and category into a point in the same space. Retrieval is
then a nearest-neighbour lookup, which is what lets candidate generation scan a
whole catalogue in microseconds while the ranking model — far more expensive per
item — only ever sees a few hundred candidates.

Training uses in-batch negative sampling: every other positive in the batch acts
as a negative for this one, so a batch of N gives N-1 negatives per example for
free. That trick has one sharp edge, and it matters more the smaller the
catalogue is: when the same video appears twice in a batch, the duplicate is
labelled a negative for the row it is actually the correct answer to. The model
is then punished for being right. :func:`_mask_false_negatives` removes those
entries, which is not an optional refinement on a catalogue of a few thousand
videos — collisions are common enough to visibly bend the loss.
"""

from __future__ import annotations

import argparse
import logging
import os
from dataclasses import dataclass
from pathlib import Path

# Must be set before torch is imported. Some ops the towers use have no MPS
# kernel; without this the run dies on the first one instead of quietly falling
# back to the CPU for that op alone.
os.environ.setdefault("PYTORCH_ENABLE_MPS_FALLBACK", "1")

import numpy as np  # noqa: E402
import torch  # noqa: E402
from torch import Tensor, nn  # noqa: E402
from torch.utils.data import DataLoader, Dataset  # noqa: E402

from config import (  # noqa: E402
    LABEL_COLUMN,
    TwoTowerConfig,
    add_common_arguments,
    config_from_args,
    configure_logging,
)
from data_prep import PAD_INDEX, PreparedData, Vocabulary, prepare  # noqa: E402

LOGGER = logging.getLogger(__name__)


def select_device() -> torch.device:
    """Picks the best available device.

    Returns:
        ``mps`` on Apple silicon, else ``cuda`` if present, else ``cpu``.
    """
    if torch.backends.mps.is_available():
        return torch.device("mps")
    if torch.cuda.is_available():
        return torch.device("cuda")
    return torch.device("cpu")


class InteractionDataset(Dataset[dict[str, Tensor]]):
    """Positive interactions, each with the history that preceded it."""

    def __init__(
        self,
        history: np.ndarray,
        video_index: np.ndarray,
        category_index: np.ndarray,
    ) -> None:
        """
        Args:
            history: ``(rows, max_history)`` padded history indices.
            video_index: ``(rows,)`` index of the watched video.
            category_index: ``(rows,)`` category of the watched video.
        """
        self._history = torch.from_numpy(history)
        self._video = torch.from_numpy(video_index)
        self._category = torch.from_numpy(category_index)

    def __len__(self) -> int:
        return int(self._video.shape[0])

    def __getitem__(self, index: int) -> dict[str, Tensor]:
        return {
            "history": self._history[index],
            "video": self._video[index],
            "category": self._category[index],
        }


class UserTower(nn.Module):
    """Embeds a watch history into the shared space.

    History is pooled by masked mean rather than by a recurrent or attention
    layer. With histories capped at a few dozen ids and a catalogue in the
    thousands, a sequence model has far more capacity than there is signal, and
    it costs an export headache for a gain that does not show up in validation.
    """

    def __init__(
        self, num_videos: int, embedding_dim: int, hidden_dim: int, dropout: float
    ) -> None:
        """
        Args:
            num_videos: Vocabulary size, including the padding token.
            embedding_dim: Width of the shared space.
            hidden_dim: Width of the projection head.
            dropout: Dropout applied inside the head.
        """
        super().__init__()
        self.video_embedding = nn.Embedding(num_videos, embedding_dim, padding_idx=PAD_INDEX)
        self.head = nn.Sequential(
            nn.Linear(embedding_dim, hidden_dim),
            nn.GELU(),
            nn.Dropout(dropout),
            nn.Linear(hidden_dim, embedding_dim),
        )

    def forward(self, history: Tensor) -> Tensor:
        """
        Args:
            history: ``(batch, max_history)`` padded video indices.

        Returns:
            ``(batch, embedding_dim)`` L2-normalised taste vectors.
        """
        embedded = self.video_embedding(history)
        mask = (history != PAD_INDEX).unsqueeze(-1).to(embedded.dtype)
        # clamp(min=1) keeps an all-padding history — a user's very first watch —
        # from dividing by zero and poisoning the batch with NaNs.
        pooled = (embedded * mask).sum(dim=1) / mask.sum(dim=1).clamp(min=1.0)
        return nn.functional.normalize(self.head(pooled), dim=-1)


class VideoTower(nn.Module):
    """Embeds a video into the shared space.

    This is the tower that gets exported: at serving time the whole catalogue is
    embedded once, offline, and only the user tower would ever need to run per
    request — and in this design not even that, because candidate generation
    searches with a vector the serving service assembles from stored embeddings.
    """

    def __init__(
        self,
        num_videos: int,
        num_categories: int,
        embedding_dim: int,
        hidden_dim: int,
        dropout: float,
    ) -> None:
        """
        Args:
            num_videos: Video vocabulary size, including padding.
            num_categories: Number of distinct categories, including padding.
            embedding_dim: Width of the shared space.
            hidden_dim: Width of the projection head.
            dropout: Dropout applied inside the head.
        """
        super().__init__()
        self.video_embedding = nn.Embedding(num_videos, embedding_dim, padding_idx=PAD_INDEX)
        self.category_embedding = nn.Embedding(num_categories, embedding_dim)
        self.head = nn.Sequential(
            nn.Linear(embedding_dim * 2, hidden_dim),
            nn.GELU(),
            nn.Dropout(dropout),
            nn.Linear(hidden_dim, embedding_dim),
        )

    def forward(self, video: Tensor, category: Tensor) -> Tensor:
        """
        Args:
            video: ``(batch,)`` video indices.
            category: ``(batch,)`` category indices.

        Returns:
            ``(batch, embedding_dim)`` L2-normalised video vectors.
        """
        combined = torch.cat(
            [self.video_embedding(video), self.category_embedding(category)], dim=-1
        )
        return nn.functional.normalize(self.head(combined), dim=-1)


@dataclass
class TrainedTwoTower:
    """A trained model plus everything needed to reproduce its encoding."""

    user_tower: UserTower
    video_tower: VideoTower
    video_vocab: Vocabulary
    num_categories: int
    best_recall: float


def _mask_false_negatives(logits: Tensor, video: Tensor) -> Tensor:
    """Removes in-batch negatives that are actually the correct answer.

    When a video appears more than once in a batch, the copy sitting in another
    row's negative set is not a negative at all. Left alone, the loss penalises
    the model for scoring it highly — which is exactly the behaviour wanted.

    Args:
        logits: ``(batch, batch)`` similarity matrix.
        video: ``(batch,)`` video indices for the batch.

    Returns:
        The matrix with off-diagonal duplicates set to ``-inf``.
    """
    duplicates = video.unsqueeze(0) == video.unsqueeze(1)
    diagonal = torch.eye(video.shape[0], dtype=torch.bool, device=video.device)
    return logits.masked_fill(duplicates & ~diagonal, float("-inf"))


def _encode_rows(
    frame, video_vocab: Vocabulary, max_history: int
) -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    """Encodes a prepared frame into padded arrays.

    Args:
        frame: Prepared events with a ``history`` column.
        video_vocab: Video vocabulary.
        max_history: Width to pad histories to.

    Returns:
        Padded history indices, video indices and category indices.
    """
    history = np.full((len(frame), max_history), PAD_INDEX, dtype=np.int64)
    for row, ids in enumerate(frame["history"]):
        if not len(ids):
            continue
        encoded = [video_vocab.index_of.get(str(i), PAD_INDEX) for i in ids][-max_history:]
        history[row, max_history - len(encoded) :] = encoded

    video = video_vocab.encode(frame["video_id"])
    category = frame["category_id"].astype(np.int64).to_numpy()
    return history, video, category


@torch.no_grad()
def evaluate_recall(
    user_tower: UserTower,
    video_tower: VideoTower,
    loader: DataLoader[dict[str, Tensor]],
    device: torch.device,
    k: int,
) -> float:
    """Measures recall@k against the videos present in each batch.

    This is recall within the batch, not against the full catalogue: it is cheap,
    it moves in the same direction as the real metric, and it is the number
    early stopping needs. Catalogue-wide recall belongs in an offline
    evaluation, not in the training loop.

    Args:
        user_tower: User tower, already on ``device``.
        video_tower: Video tower, already on ``device``.
        loader: Validation loader.
        device: Device to run on.
        k: Cut-off.

    Returns:
        Recall@k in ``[0, 1]``, or ``0.0`` when the loader is empty.
    """
    user_tower.eval()
    video_tower.eval()

    hits = 0
    total = 0
    for batch in loader:
        history = batch["history"].to(device)
        video = batch["video"].to(device)
        category = batch["category"].to(device)
        if video.shape[0] < 2:
            continue

        logits = user_tower(history) @ video_tower(video, category).T
        cut = min(k, logits.shape[1])
        top = logits.topk(cut, dim=1).indices
        target = torch.arange(logits.shape[0], device=device).unsqueeze(1)
        hits += int((top == target).any(dim=1).sum().item())
        total += int(logits.shape[0])

    return hits / total if total else 0.0


def train_two_tower(
    prepared: PreparedData, cfg: TwoTowerConfig, max_history: int
) -> TrainedTwoTower:
    """Trains both towers with in-batch negative sampling.

    Args:
        prepared: Output of :func:`data_prep.prepare`.
        cfg: Model and optimisation settings.
        max_history: History width, from the data configuration.

    Returns:
        The trained towers and the best validation recall observed.
    """
    torch.manual_seed(cfg.seed)
    device = select_device()
    LOGGER.info("training two-tower on device=%s", device)

    # Only genuine watches are positives. Training a retrieval model on bounces
    # teaches it to retrieve things people leave.
    positives = prepared.train[prepared.train[LABEL_COLUMN] >= 0.25]
    LOGGER.info(
        "positives %d of %d training rows (%.1f%%)",
        len(positives),
        len(prepared.train),
        100.0 * len(positives) / max(len(prepared.train), 1),
    )

    num_categories = (
        int(
            max(
                prepared.catalog["category_id"].max(),
                prepared.validation["category_id"].max() if len(prepared.validation) else 0,
            )
        )
        + 1
    )

    train_arrays = _encode_rows(positives, prepared.video_vocab, max_history)
    train_loader: DataLoader[dict[str, Tensor]] = DataLoader(
        InteractionDataset(*train_arrays),
        batch_size=cfg.batch_size,
        shuffle=True,
        # Dropping a ragged final batch keeps the similarity matrix square and
        # avoids a last step whose negatives are far scarcer than the rest.
        drop_last=True,
    )

    validation_positives = prepared.validation[prepared.validation[LABEL_COLUMN] >= 0.25]
    validation_loader: DataLoader[dict[str, Tensor]] = DataLoader(
        InteractionDataset(*_encode_rows(validation_positives, prepared.video_vocab, max_history)),
        batch_size=cfg.batch_size,
        shuffle=False,
        drop_last=False,
    )

    user_tower = UserTower(
        len(prepared.video_vocab), cfg.embedding_dim, cfg.hidden_dim, cfg.dropout
    ).to(device)
    video_tower = VideoTower(
        len(prepared.video_vocab),
        num_categories,
        cfg.embedding_dim,
        cfg.hidden_dim,
        cfg.dropout,
    ).to(device)

    optimiser = torch.optim.AdamW(
        list(user_tower.parameters()) + list(video_tower.parameters()),
        lr=cfg.learning_rate,
        weight_decay=cfg.weight_decay,
    )
    criterion = nn.CrossEntropyLoss()

    best_recall = -1.0
    best_state: dict[str, dict[str, Tensor]] = {}
    epochs_without_improvement = 0

    for epoch in range(1, cfg.epochs + 1):
        user_tower.train()
        video_tower.train()
        running_loss = 0.0
        steps = 0

        for batch in train_loader:
            history = batch["history"].to(device)
            video = batch["video"].to(device)
            category = batch["category"].to(device)

            user_vectors = user_tower(history)
            video_vectors = video_tower(video, category)
            # Both towers emit unit vectors, so this is cosine similarity.
            logits = (user_vectors @ video_vectors.T) / cfg.temperature
            logits = _mask_false_negatives(logits, video)

            # The correct video for row i sits at column i by construction.
            targets = torch.arange(logits.shape[0], device=device)
            loss = criterion(logits, targets)

            optimiser.zero_grad(set_to_none=True)
            loss.backward()
            optimiser.step()

            running_loss += float(loss.item())
            steps += 1

        recall = evaluate_recall(user_tower, video_tower, validation_loader, device, cfg.eval_k)
        LOGGER.info(
            "epoch %2d | loss %.4f | val recall@%d %.4f",
            epoch,
            running_loss / max(steps, 1),
            cfg.eval_k,
            recall,
        )

        if recall > best_recall:
            best_recall = recall
            best_state = {
                "user": {k: v.detach().cpu().clone() for k, v in user_tower.state_dict().items()},
                "video": {k: v.detach().cpu().clone() for k, v in video_tower.state_dict().items()},
            }
            epochs_without_improvement = 0
        else:
            epochs_without_improvement += 1
            if epochs_without_improvement >= cfg.patience:
                LOGGER.info("early stop after %d epochs without improvement", cfg.patience)
                break

        # Unified memory means this GPU shares one pool with Postgres, four Go
        # services and a dev server. Releasing between epochs is what keeps a
        # long run from being killed by something else's allocation.
        if device.type == "mps":
            torch.mps.empty_cache()

    if best_state:
        user_tower.load_state_dict(best_state["user"])
        video_tower.load_state_dict(best_state["video"])

    LOGGER.info("best validation recall@%d: %.4f", cfg.eval_k, best_recall)
    return TrainedTwoTower(
        user_tower=user_tower.cpu().eval(),
        video_tower=video_tower.cpu().eval(),
        video_vocab=prepared.video_vocab,
        num_categories=num_categories,
        best_recall=max(best_recall, 0.0),
    )


def main() -> None:
    """CLI entry point: trains the towers and saves their weights."""
    parser = argparse.ArgumentParser(description=__doc__)
    add_common_arguments(parser)
    parser.add_argument("--embedding-dim", type=int, default=TwoTowerConfig.embedding_dim)
    parser.add_argument("--batch-size", type=int, default=TwoTowerConfig.batch_size)
    parser.add_argument("--epochs", type=int, default=TwoTowerConfig.epochs)
    parser.add_argument("--learning-rate", type=float, default=TwoTowerConfig.learning_rate)
    args = parser.parse_args()

    configure_logging(args.log_level)
    cfg = config_from_args(args)
    prepared = prepare(cfg.data)
    trained = train_two_tower(prepared, cfg.two_tower, cfg.data.max_history_length)

    destination: Path = cfg.artifacts_dir / "two_tower.pt"
    destination.parent.mkdir(parents=True, exist_ok=True)
    torch.save(
        {
            "user_tower": trained.user_tower.state_dict(),
            "video_tower": trained.video_tower.state_dict(),
            "video_tokens": trained.video_vocab.tokens,
            "num_categories": trained.num_categories,
            "embedding_dim": cfg.two_tower.embedding_dim,
            "hidden_dim": cfg.two_tower.hidden_dim,
        },
        destination,
    )
    LOGGER.info("wrote %s", destination)


if __name__ == "__main__":
    main()
