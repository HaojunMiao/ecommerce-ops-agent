#!/usr/bin/env python3
"""Run the fixed offline RAG retrieval evaluation and ablations."""
from __future__ import annotations

import csv
import importlib.util
import json
import math
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CORPUS = ROOT / "evals" / "corpus" / "rag-evaluation"
CASES_PATH = ROOT / "evals" / "rag_evaluation_cases.jsonl"
OUT = ROOT / "evals" / "results" / "rag_evaluation_offline.json"
CSV_OUT = ROOT / "evals" / "results" / "rag_evaluation_offline.csv"


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


SHORT = load_module("rag_offline_core", ROOT / "evals" / "_rag_offline_core.py")
AB = SHORT


def percentile(values: list[float], p: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    pos = (len(ordered) - 1) * p
    lo, hi = math.floor(pos), math.ceil(pos)
    if lo == hi:
        return ordered[lo]
    return ordered[lo] * (hi - pos) + ordered[hi] * (pos - lo)


def load_docs(subset: str) -> list[dict]:
    return [
        {"id": path.stem, "text": path.read_text(encoding="utf-8")}
        for path in sorted((CORPUS / subset).glob("*.md"))
    ]


def load_cases() -> list[dict]:
    return [json.loads(line) for line in CASES_PATH.read_text(encoding="utf-8").splitlines() if line.strip()]


def aggregate(rows: list[dict]) -> dict:
    if not rows:
        return {}
    latencies = [row["latency_ms"] for row in rows]
    return {
        "queries": len(rows),
        "hit_rate_at_k": sum(row["hit"] for row in rows) / len(rows),
        "precision_at_k": sum(row["precision"] for row in rows) / len(rows),
        "chunk_recall_at_k": sum(row["chunk_recall"] for row in rows) / len(rows),
        "mrr": sum(row["mrr"] for row in rows) / len(rows),
        "ndcg_at_k": sum(row["ndcg"] for row in rows) / len(rows),
        "avg_context_chars": sum(row["context_chars"] for row in rows) / len(rows),
        "search_ms_p50": percentile(latencies, 0.50),
        "search_ms_p95": percentile(latencies, 0.95),
    }


def evaluate(chunks, cases, tokenize, mode: str, top_k=5, rrf_k=60, cand=50) -> dict:
    rows = []
    for case in cases:
        gold_docs = set(case["gold_docs"])
        relevant_ids = {
            chunk.id for chunk in chunks
            if chunk.doc_id in gold_docs and case["gold_span"] in chunk.text
        }
        started = time.perf_counter()
        hits = AB.search(chunks, case["query"], tokenize, mode, k=top_k, cand=cand, rrf_k=rrf_k)
        elapsed = (time.perf_counter() - started) * 1000
        relevance = [1.0 if chunk.id in relevant_ids else 0.0 for chunk, _ in hits[:top_k]]
        first = next((index + 1 for index, value in enumerate(relevance) if value), 0)
        ideal = [1.0] * min(len(relevant_ids), top_k)
        if len(ideal) < top_k:
            ideal.extend([0.0] * (top_k - len(ideal)))
        retrieved_relevant = int(sum(relevance))
        ideal_dcg = AB.dcg(ideal)
        rows.append({
            "id": case["id"],
            "split": case["split"],
            "category": case["category"],
            "hit": 1.0 if first else 0.0,
            "precision": retrieved_relevant / len(hits[:top_k]) if hits[:top_k] else 0.0,
            "chunk_recall": retrieved_relevant / len(relevant_ids) if relevant_ids else 0.0,
            "relevant_chunks": len(relevant_ids),
            "retrieved_relevant": retrieved_relevant,
            "mrr": 0.0 if not first else 1.0 / first,
            "ndcg": AB.dcg(relevance) / ideal_dcg if ideal_dcg > 0 else 0.0,
            "first_rank": first,
            "context_chars": sum(len(chunk.text) for chunk, _ in hits[:top_k]),
            "latency_ms": elapsed,
            "hits": [
                {"chunk_id": chunk.id, "doc_id": chunk.doc_id, "score": round(score, 8)}
                for chunk, score in hits
            ],
        })
    result = aggregate(rows)
    result["by_split"] = {
        split: aggregate([row for row in rows if row["split"] == split])
        for split in ("dev", "test")
    }
    result["by_category"] = {
        category: aggregate([row for row in rows if row["category"] == category])
        for category in sorted({row["category"] for row in rows})
    }
    result["cases"] = rows
    return result


def summary(run: dict) -> dict:
    out = {key: value for key, value in run.items() if key not in {"cases", "by_split", "by_category"}}
    for split in ("dev", "test"):
        for key, value in run["by_split"][split].items():
            if key != "queries":
                out[f"{split}_{key}"] = value
    if "boundary" in run["by_category"]:
        for key, value in run["by_category"]["boundary"].items():
            if key != "queries":
                out[f"boundary_{key}"] = value
    return out


def main() -> None:
    if not CORPUS.exists() or not CASES_PATH.exists():
        subprocess.run([sys.executable, "evals/build_rag_evaluation.py"], cwd=ROOT, check=True)
    docs = {name: load_docs(name) for name in ("core", "noise120")}
    cases = load_cases()
    dev_cases = [case for case in cases if case["split"] == "dev"]
    test_cases = [case for case in cases if case["split"] == "test"]
    configs = [
        (AB.chunk_fixed, 300, 0),
        (AB.chunk_fixed, 300, 60),
        (AB.chunk_fixed, 500, 0),
        (AB.chunk_fixed, 500, 100),
        (AB.chunk_fixed, 800, 0),
        (AB.chunk_fixed, 800, 160),
        (AB.chunk_fixed, 1200, 0),
        (AB.chunk_fixed, 1200, 200),
        (AB.chunk_heading, 500, 100),
    ]

    gse = SHORT.GSETokenCache()
    embedder = AB.RemoteEmbedder()
    AB.EMBEDDER = embedder
    gse.preload([case["query"] for case in cases])
    embedder.embed_many([case["query"] for case in cases])

    built = {}

    def chunks_for(subset: str, chunker, size: int, overlap: int):
        key = (subset, chunker.__name__, size, overlap)
        if key not in built:
            texts = []
            for doc in docs[subset]:
                texts.extend(chunker(doc["text"], size, overlap))
            gse.preload(texts)
            built[key] = SHORT.build_chunks(docs[subset], chunker, size, overlap, gse, embedder)
        return built[key]

    runs = []
    chunk_dev_results = []
    for chunker, size, overlap in configs:
        chunks = chunks_for("core", chunker, size, overlap)
        metrics = evaluate(chunks, cases, gse, "hybrid")
        destroyed = [row["id"] for row in metrics["cases"] if row["relevant_chunks"] == 0]
        if destroyed:
            metrics["destroyed_gold_queries"] = destroyed
        run = {
            "group": "chunking",
            "name": f"{chunker.__name__}/size={size}/overlap={overlap}",
            "subset": "core",
            "mode": "hybrid",
            "chunker": chunker.__name__,
            "chunk_size": size,
            "overlap": overlap,
            "n_docs": len(docs["core"]),
            "n_chunks": len(chunks),
            "chunks_per_doc": len(chunks) / len(docs["core"]),
            **metrics,
        }
        runs.append(run)
        chunk_dev_results.append(run)

    # Select only on dev topics. Prefer MRR, then hit rate, then less context.
    selected = max(
        chunk_dev_results,
        key=lambda run: (
            run["by_split"]["dev"]["mrr"],
            run["by_split"]["dev"]["hit_rate_at_k"],
            -run["by_split"]["dev"]["avg_context_chars"],
        ),
    )
    selected_chunker = AB.chunk_heading if selected["chunker"] == "chunk_heading" else AB.chunk_fixed
    selected_size = selected["chunk_size"]
    selected_overlap = selected["overlap"]

    # Retriever x noise, evaluated with the dev-selected chunk configuration.
    for subset in ("core", "noise120"):
        chunks = chunks_for(subset, selected_chunker, selected_size, selected_overlap)
        for mode in ("bm25", "vector", "hybrid"):
            metrics = evaluate(chunks, cases, gse, mode)
            runs.append({
                "group": "retriever_noise",
                "name": f"{subset}/{mode}",
                "subset": subset,
                "mode": mode,
                "chunker": selected["chunker"],
                "chunk_size": selected_size,
                "overlap": selected_overlap,
                "top_k": 5,
                "n_docs": len(docs[subset]),
                "n_chunks": len(chunks),
                "chunks_per_doc": len(chunks) / len(docs[subset]),
                **metrics,
            })

    large_chunks = chunks_for("noise120", selected_chunker, selected_size, selected_overlap)
    for top_k in (1, 3, 5, 8, 10):
        metrics = evaluate(large_chunks, cases, gse, "hybrid", top_k=top_k)
        runs.append({
            "group": "top_k",
            "name": f"noise120/hybrid/top_k={top_k}",
            "subset": "noise120",
            "mode": "hybrid",
            "top_k": top_k,
            "chunker": selected["chunker"],
            "chunk_size": selected_size,
            "overlap": selected_overlap,
            "n_docs": len(docs["noise120"]),
            "n_chunks": len(large_chunks),
            **metrics,
        })

    summaries = [summary(run) for run in runs]
    payload = {
        "protocol": {
            "scope": "long-document independent-query retrieval benchmark",
            "synthetic_corpus": True,
            "subsets": {name: len(items) for name, items in docs.items()},
            "queries": len(cases),
            "dev_queries": len(dev_cases),
            "test_queries": len(test_cases),
            "split": "policy-level; parameters selected on 10 dev policies, reported on 25 unseen test policies",
            "query_authorship": "manually authored separately from document templates",
            "embedding": embedder.description(),
            "tokenizer": "production Go/GSE",
            "bm25": "Okapi BM25 k1=1.5 b=0.75 in Python",
            "selected_on_dev": selected["name"],
            "embedding_api_calls": embedder.api_calls,
            "embedding_api_ms": round(embedder.api_ms, 2),
        },
        "summary": summaries,
        "runs": runs,
    }
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    embedder.flush()
    columns = sorted({key for row in summaries for key in row})
    with CSV_OUT.open("w", encoding="utf-8", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=columns, lineterminator="\n")
        writer.writeheader()
        writer.writerows(summaries)

    print(f"selected_on_dev={selected['name']}")
    print(f"wrote {OUT} and {CSV_OUT}; runs={len(runs)} api_calls={embedder.api_calls}")
    for row in summaries:
        print(
            row["group"], row["name"],
            f"test_hit={row.get('test_hit_rate_at_k', 0):.4f}",
            f"test_p={row.get('test_precision_at_k', 0):.4f}",
            f"test_mrr={row.get('test_mrr', 0):.4f}",
            f"chunks={row.get('n_chunks', 0)}",
        )


if __name__ == "__main__":
    main()
