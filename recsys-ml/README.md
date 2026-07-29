# recsys-ml — two-tower retrieval + LightGBM ranking, served from Go

Offline training in Python, real-time serving in Go, ONNX between them.
Candidate generation and ranking are separate models and stay separate.

```
event log (parquet)
        │
        ▼
┌─ training/ (Python, batch) ────────────────────────────────┐
│  data_prep         watch_ratio, history sequences,         │
│                    time-based train/validation split       │
│  two_tower_model   UserTower + VideoTower, in-batch        │
│                    negatives, MPS                          │
│  ranking_model     LightGBM regression on watch_ratio      │
│  export_onnx       → video_tower.onnx                      │
│                    → ranker.onnx                           │
│                    → video_embeddings.{parquet,json}       │
│                    → feature_spec.json  (the manifest)     │
└────────────────────────────────────────────────────────────┘
        │
        ▼
┌─ serving/ (Go, online) ────────────────────────────────────┐
│  vectorstore     ANN/brute-force search over embeddings    │
│  onnxmodel       loads ONNX, hot-reloads on new artifacts  │
│  recommendation  retrieve → build features → rank → topN   │
└────────────────────────────────────────────────────────────┘
```

---

## Read this first: what the data can and cannot support

This module was built inside a repository whose live database holds **2,159
interaction signals from one user across 93 videos** (measured 2026-07-29).

A two-tower model trained with in-batch negative sampling needs at least as many
distinct videos per batch as the batch size, and it learns a mapping from watch
history to taste — with a single user there is exactly one trajectory to learn
from. On that data the model memorises; it does not generalise. Two of the four
requested signals are also empty there: `skipped` has 0 rows and `liked` has 9.

So the pipeline ships with **`synth_events.py`**, which generates an event log
with the production schema and a planted latent structure — per-user category
preferences, favourite creators, video quality, a freshness effect — so the
metrics mean something rather than merely exist. Everything below runs on it
today. Point `--events-path` at a real log when there is one; nothing else
changes.

This module does **not** touch the heuristic recommender the main application
serves from (`services/recsys`). See `CLAUDE.md` §6 for why that one is
deliberately not ML.

---

## Prerequisites

Two system dependencies pip cannot install:

```bash
brew install libomp        # LightGBM's shared library links against OpenMP
brew install onnxruntime   # only needed to build the Go service with inference
```

Python environment:

```bash
cd recsys-ml
python3 -m venv .venv
.venv/bin/pip install -r training/requirements.txt
```

Versions in `requirements.txt` are the ones actually resolved and run on the
target machine (macOS, Apple M4, Python 3.14.6). If you pin an older Python,
re-resolve rather than forcing them — the constraint is the interpreter.

---

## Running the training pipeline

```bash
cd recsys-ml/training

# 1. generate a synthetic event log (skip if you have a real one)
../.venv/bin/python synth_events.py --events-path ../data/events.parquet

# 2. inspect what preparation produces, without training
../.venv/bin/python data_prep.py --events-path ../data/events.parquet

# 3. train everything and export every artifact
../.venv/bin/python export_onnx.py \
    --events-path ../data/events.parquet \
    --artifacts-dir ../artifacts
```

Individual stages, useful when iterating:

```bash
../.venv/bin/python export_onnx.py --stage towers --artifacts-dir ../artifacts
../.venv/bin/python export_onnx.py --stage ranker --artifacts-dir ../artifacts
```

### Why the pipeline runs as two subprocesses

PyTorch ships its own OpenMP runtime; LightGBM links the system's. Loading both
into one process on macOS **segfaults** — reproducibly, at the first LightGBM
`fit` after `import torch`, with no Python traceback (exit 139).

The usual workaround, `KMP_DUPLICATE_LIB_OK=TRUE`, is documented by Intel as
unsafe: it silences the guard rather than resolving the conflict and can produce
wrong numbers with no indication anything went wrong. Wrong numbers in a
training pipeline are worse than a crash, because a crash gets noticed. So
`export_onnx.py` re-invokes itself once per stage and merges the results. Cost:
a few seconds of interpreter startup.

### Output

| File | Purpose |
|---|---|
| `video_tower.onnx` | Video tower, dynamic batch axis |
| `ranker.onnx` | LightGBM regressor |
| `video_embeddings.parquet` | Whole catalogue embedded, for the vector DB |
| `video_embeddings.json` | Same table in the shape the Go service loads |
| `feature_spec.json` | Feature order, metrics, provenance — **the manifest** |

`feature_spec.json` is written **last**, and it is what the serving service keys
its hot-reload on. See "Hot reloading" below for why that matters.

---

## Running the Go service

```bash
cd recsys-ml/serving

go test ./...                                    # no ONNX Runtime needed
go build -tags onnx -o recserve ./cmd/recserve   # with inference

export ONNXRUNTIME_LIB_PATH=/opt/homebrew/lib/libonnxruntime.dylib
./recserve -addr :8190 -artifacts ../artifacts
```

**The `onnx` build tag is deliberate.** Without it the package still compiles,
the feature spec is still read and validated, and every other package builds and
tests on a machine with no ONNX Runtime installed. A binary built without it
runs and degrades to retrieval order or trending — which makes the fallback path
the one exercised by default, rather than one discovered during an incident.

`serving/` is a **separate Go module** from the repository root, so the cgo
dependency on ONNX Runtime never reaches `make check` at the top of the repo.

### End-to-end check

```bash
curl -s localhost:8190/healthz
# {"indexedVectors":2616,"modelLoaded":true,"status":"ok"}

curl -s -X POST localhost:8190/recommendations \
  -H 'Content-Type: application/json' \
  -d '{"userId":"user_00007","watchHistory":["vid_002112","vid_000733"],"topN":5}'
```

```json
{"results":[
  {"videoId":"vid_002272","score":0.892,"source":"ranked"},
  {"videoId":"vid_001168","score":0.865,"source":"ranked"}
]}
```

`source` is returned on purpose. A feed silently served by the fallback for a
week looks exactly like a working recommender from the outside.

| `source` | Meaning |
|---|---|
| `ranked` | Retrieval and the ranking model both ran |
| `retrieval` | Ranking unavailable; ordered by similarity alone |
| `trending` | Retrieval unavailable; fallback answered |

---

## Design notes

### Candidate generation and ranking never merge

They answer different questions on different budgets. Retrieval must score the
whole catalogue, so it is restricted to a dot product between precomputed
vectors. Ranking sees a few hundred survivors and can afford features retrieval
cannot express at all — freshness, audience retention, this viewer's affinity
for this creator. Merging them forces the expensive features onto every video in
the catalogue, or discards them.

### The user tower does not run at request time

Serving builds its query vector as the **mean of the embeddings of what the
viewer watched**, normalised — which is exactly the masked-mean pooling the user
tower performs. The equivalence is why retrieval needs only the vectors already
in the index. `compute_candidate_scores` in `ranking_model.py` does the same
arithmetic during training, so `candidate_score` means the same thing on both
sides.

### The label is watch ratio, never a click

A click measures a thumbnail. Watch ratio measures whether the video was worth
opening. `watch_ratio` is clipped at 1.0 — rewatching a chorus can push it past
one, and an unclipped label lets a handful of loops dominate the regression.

### Leakage is designed out, not hoped away

* The train/validation split is **by timestamp**, never at random. A random split
  lets the model see the future of the very user it is asked to predict.
* Each row's watch history contains only videos watched **strictly earlier**.
* `user_creator_affinity` is an **expanding mean over earlier rows only**. A
  plain groupby mean would put the label straight into the feature.

### In-batch negatives mask their own false negatives

When a video appears twice in a batch, the duplicate sitting in another row's
negative set is not a negative — it is that row's correct answer, and the loss
would punish the model for scoring it highly. `_mask_false_negatives` removes
those entries. On a catalogue of a few thousand videos the collisions are common
enough to visibly bend the loss; this is not an optional refinement.

### Vector store

`VectorStore` has two implementations behind one interface. Qdrant would be a
third and require no change above it.

* **`MemoryStore`** — brute-force scan. The right algorithm at this scale, not a
  placeholder: a few thousand videos at 128 dimensions is a few hundred thousand
  multiply-adds, with no network hop, no index rebuild after each training run,
  and exact results.
* **`PgVectorStore`** — the right answer once the catalogue outgrows a linear
  scan. Needs `brew install pgvector` and `CREATE EXTENSION vector`, neither of
  which is present on this machine yet. `Replace` truncates and re-inserts in
  one transaction: a half-replaced index mixes vectors from two runs, and
  distances between two runs' embeddings are meaningless.

### Hot reloading

A goroutine polls the artifacts directory. Polling rather than fsnotify because
a training run writes five files: a watcher fires on the first, when the set is
inconsistent, and on network mounts it may not fire at all. A missed poll costs
one interval of staleness; a missed watch event costs staleness until the next
restart, unreported.

The fingerprint is the **content hash of `feature_spec.json` alone**, after
checking the other artifacts exist. This is the second design — the first
fingerprinted all five files, and a real publish exposed the fault: the two
trainers are separate processes seconds apart, so between them the directory
holds a new video tower beside an old ranker. That state is perfectly stable, so
the "wait until it settles" guard reported it settled and the service loaded a
mismatched pair. Observed as two reloads per training run, the first on the torn
set. Keying on the manifest — written last — makes intermediate states
invisible. `TestFingerprintIgnoresAHalfPublishedRun` is the regression test.

Swapping is an atomic pointer store; the superseded bundle is closed after a
drain period, because closing an ONNX session under a running inference is a
use-after-free.

### Training/serving skew is caught at load time

`feature_spec.json` carries the ranking feature order. `FeatureSpec.Validate`
compares it to `recommendation.FeatureNames` and refuses a bundle that
disagrees, naming the offending position. Swap two feature columns and nothing
crashes — the model keeps returning plausible numbers computed from the wrong
inputs, and the feed quietly gets worse. This turns that into a refusal to load.

---

## What has actually been verified

Run on this machine, 2026-07-29, not inferred:

* Full pipeline end to end: 12,199 events / 400 users / 2,616 videos.
  Two-tower best validation recall@10 **0.1226**; ranker validation RMSE
  **0.1949**; feature importance `hours_since_upload` 759, `completion_rate_avg`
  636, `candidate_score` 548, `user_creator_affinity` 157.
* Both ONNX graphs load in `onnxruntime` and produce sane output. The video
  tower runs at batch 1, 3 and 64 with unit-norm rows (dynamic axis works).
* `go test ./...` passes without ONNX Runtime installed.
* `go build -tags onnx` links, the service loads the bundle, indexes 2,616
  vectors, and answers both a ranked request and a cold-start fallback.
* Hot reload observed: retraining while the service ran produced exactly one
  reload, at the new `generated_at`.

Two bugs were found by running it rather than by reading it, and both are fixed:

1. `video_tower.onnx` was an **invalid model** — the output name `embedding`
   collided with an internal value, and ONNX Runtime rejected it at load.
   Nothing before deployment would have caught it.
2. Torch wrote weights to a sidecar `.onnx.data` file, which the atomic-rename
   helper left under its temporary name; the graph referenced a file that no
   longer matched. Now exported with `external_data=False`.

## Not done

* `PgVectorStore` is written but **not executed** — pgvector is not installed on
  this machine. `MemoryStore` is what the verified runs used.
* `staticFeatureStore` and `staticTrending` in `cmd/recserve` are placeholders.
  Wiring them to real sources is a deployment concern the interfaces already
  isolate.
* No training run against the real `recsys.signals` table, for the reason at the
  top of this file.
