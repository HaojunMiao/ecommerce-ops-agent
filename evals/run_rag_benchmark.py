#!/usr/bin/env python3
"""Run the expanded offline RAG benchmark with real Qwen embeddings.

This is an algorithm-quality benchmark.  It reuses the production GSE tokenizer
through a tiny Go helper, but BM25 and fusion run in Python so BM25 can be tested
without modifying the deployed PostgreSQL instance.
"""
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
CORPUS = ROOT / "evals" / "corpus" / "rag-benchmark"
CASES_PATH = ROOT / "evals" / "rag_benchmark_cases.jsonl"
OUT = ROOT / "evals" / "results" / "rag_benchmark_results.json"
CSV_OUT = ROOT / "evals" / "results" / "rag_benchmark_summary.csv"


def load_ablation_module():
    path = ROOT / "scripts" / "rag-eval" / "run.py"
    spec = importlib.util.spec_from_file_location("rag_eval_core", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


AB = load_ablation_module()


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


class GSETokenCache:
    def __init__(self) -> None:
        self.values: dict[str, list[str]] = {}

    def preload(self, texts: list[str]) -> None:
        unique = list(dict.fromkeys(text for text in texts if text not in self.values))
        if not unique:
            return
        proc = subprocess.run(
            ["go", "run", "./evals/rag_tokenize"],
            cwd=ROOT,
            input=json.dumps(unique, ensure_ascii=False),
            text=True,
            capture_output=True,
            check=False,
        )
        if proc.returncode != 0:
            raise RuntimeError(f"production GSE tokenizer failed: {proc.stderr[-1000:]}")
        token_lists = json.loads(proc.stdout)
        if len(token_lists) != len(unique):
            raise RuntimeError("production GSE tokenizer returned wrong item count")
        self.values.update(zip(unique, token_lists))

    def __call__(self, text: str) -> list[str]:
        if text not in self.values:
            self.preload([text])
        return self.values[text]


def collect_chunk_texts(docs_by_subset: dict[str, list[dict]], configs: list[tuple]) -> list[str]:
    texts: list[str] = []
    for docs in docs_by_subset.values():
        for chunker, size, overlap in configs:
            for doc in docs:
                texts.extend(chunker(doc["text"], size, overlap))
    return list(dict.fromkeys(texts))


def build_chunks(docs: list[dict], chunker, size: int, overlap: int, tokenize, embedder):
    pending = []
    for doc in docs:
        for index, text in enumerate(chunker(doc["text"], size, overlap)):
            tokens = tokenize(text)
            pending.append((f"{doc['id']}#{index}", doc["id"], text, AB.term_freq(tokens)))
    vectors = embedder.embed_many([item[2] for item in pending])
    return [
        AB.Chunk(
            id=chunk_id,
            doc_id=doc_id,
            text=text,
            embedding=vector,
            terms=terms,
            length=sum(terms.values()),
        )
        for (chunk_id, doc_id, text, terms), vector in zip(pending, vectors)
    ]


def evaluate(chunks, cases, tokenize, mode: str, top_k: int = 5, rrf_k: int = 60, cand: int = 50) -> dict:
    latencies: list[float] = []
    rows: list[dict] = []
    for case in cases:
        started = time.perf_counter()
        hits = AB.search(chunks, case["query"], tokenize, mode, k=top_k, cand=cand, rrf_k=rrf_k)
        latencies.append((time.perf_counter() - started) * 1000)
        relevant = [1.0 if case["gold_span"] in chunk.text else 0.0 for chunk, _ in hits[:top_k]]
        first = next((index + 1 for index, value in enumerate(relevant) if value), 0)
        ideal = sorted(relevant, reverse=True)
        ndcg = AB.dcg(relevant) / AB.dcg(ideal) if any(ideal) else 0.0
        retired_id = case["gold_docs"][0].replace("_current", "_retired")
        retired_rank = next((index + 1 for index, (chunk, _) in enumerate(hits[:top_k]) if chunk.doc_id == retired_id), 0)
        context_chars = sum(len(chunk.text) for chunk, _ in hits[:top_k])
        rows.append({
            "id": case["id"],
            "category": case["category"],
            "span_hit": 1.0 if first else 0.0,
            "mrr": 0.0 if not first else 1.0 / first,
            "ndcg": ndcg,
            "first_rank": first,
            "retired_rank": retired_rank,
            "retired_before_gold": 1.0 if retired_rank and (not first or retired_rank < first) else 0.0,
            "context_chars": context_chars,
            "hits": [{"chunk_id": c.id, "doc_id": c.doc_id, "score": round(score, 8)} for c, score in hits],
        })
    count = len(rows)
    return {
        "span_recall_at_k": sum(row["span_hit"] for row in rows) / count,
        "mrr": sum(row["mrr"] for row in rows) / count,
        "ndcg_at_k": sum(row["ndcg"] for row in rows) / count,
        "retired_before_gold_rate": sum(row["retired_before_gold"] for row in rows) / count,
        "avg_context_chars": sum(row["context_chars"] for row in rows) / count,
        "search_ms_p50": percentile(latencies, 0.50),
        "search_ms_p95": percentile(latencies, 0.95),
        "cases": rows,
    }


def summarized(run: dict) -> dict:
    return {key: value for key, value in run.items() if key != "cases"}


def main() -> None:
    if not CORPUS.exists():
        subprocess.run([sys.executable, "evals/build_rag_benchmark.py"], cwd=ROOT, check=True)
    cases = load_cases()
    docs_by_subset = {name: load_docs(name) for name in ("core", "noise40", "noise120")}
    chunk_configs = [
        (AB.chunk_fixed, 300, 0),
        (AB.chunk_fixed, 300, 50),
        (AB.chunk_fixed, 500, 0),
        (AB.chunk_fixed, 500, 100),
        (AB.chunk_fixed, 800, 0),
        (AB.chunk_fixed, 800, 100),
        (AB.chunk_heading, 500, 100),
    ]
    all_chunk_texts = collect_chunk_texts(docs_by_subset, chunk_configs)
    gse = GSETokenCache()
    gse.preload(all_chunk_texts + [case["query"] for case in cases])

    embedder = AB.RemoteEmbedder()
    AB.EMBEDDER = embedder
    # Prewarm all experiment texts and queries so quality experiments do not
    # accidentally benchmark provider/network variance.
    embedder.embed_many(all_chunk_texts + [case["query"] for case in cases])

    built: dict[tuple, list] = {}

    def chunks_for(subset: str, chunker=AB.chunk_fixed, size=500, overlap=100):
        key = (subset, chunker.__name__, size, overlap)
        if key not in built:
            built[key] = build_chunks(docs_by_subset[subset], chunker, size, overlap, gse, embedder)
        return built[key]

    runs: list[dict] = []

    # A. Retriever and corpus-noise ablation.
    for subset in ("core", "noise40", "noise120"):
        chunks = chunks_for(subset)
        for mode in ("bm25", "vector", "hybrid"):
            metrics = evaluate(chunks, cases, gse, mode)
            runs.append({
                "group": "retriever_noise",
                "name": f"{subset}/{mode}",
                "subset": subset,
                "mode": mode,
                "top_k": 5,
                "chunk_size": 500,
                "overlap": 100,
                "chunker": "fixed",
                "n_docs": len(docs_by_subset[subset]),
                "n_chunks": len(chunks),
                **metrics,
            })

    # B. Chunk size / overlap / heading strategy, on the medium-noise corpus.
    for chunker, size, overlap in chunk_configs:
        chunks = chunks_for("noise40", chunker, size, overlap)
        metrics = evaluate(chunks, cases, gse, "hybrid")
        runs.append({
            "group": "chunking",
            "name": f"{chunker.__name__}/size={size}/overlap={overlap}",
            "subset": "noise40",
            "mode": "hybrid",
            "top_k": 5,
            "chunk_size": size,
            "overlap": overlap,
            "chunker": chunker.__name__,
            "n_docs": len(docs_by_subset["noise40"]),
            "n_chunks": len(chunks),
            **metrics,
        })

    # C. Top-K/context trade-off on the largest corpus.
    baseline = chunks_for("noise120")
    for top_k in (1, 3, 5, 8, 10):
        metrics = evaluate(baseline, cases, gse, "hybrid", top_k=top_k)
        runs.append({
            "group": "top_k",
            "name": f"noise120/hybrid/top_k={top_k}",
            "subset": "noise120",
            "mode": "hybrid",
            "top_k": top_k,
            "chunk_size": 500,
            "overlap": 100,
            "chunker": "fixed",
            "n_docs": len(docs_by_subset["noise120"]),
            "n_chunks": len(baseline),
            **metrics,
        })

    # D. RRF constant and branch candidate count.
    for rrf_k in (10, 30, 60, 100):
        for cand in (20, 50, 100):
            metrics = evaluate(baseline, cases, gse, "hybrid", rrf_k=rrf_k, cand=cand)
            runs.append({
                "group": "rrf",
                "name": f"noise120/rrf={rrf_k}/cand={cand}",
                "subset": "noise120",
                "mode": "hybrid",
                "top_k": 5,
                "rrf_k": rrf_k,
                "candidate_k": cand,
                "chunk_size": 500,
                "overlap": 100,
                "chunker": "fixed",
                "n_docs": len(docs_by_subset["noise120"]),
                "n_chunks": len(baseline),
                **metrics,
            })

    # E. Document-lifecycle upper bound. The synthetic IDs expose retired
    # policies so we can quantify the value of filtering stale versions before
    # retrieval; production must implement this with metadata, not filenames.
    active_only = [chunk for chunk in baseline if not chunk.doc_id.endswith("_retired")]
    for mode in ("bm25", "vector", "hybrid"):
        metrics = evaluate(active_only, cases, gse, mode)
        runs.append({
            "group": "lifecycle_filter",
            "name": f"noise120/active_only/{mode}",
            "subset": "noise120",
            "mode": mode,
            "top_k": 5,
            "chunk_size": 500,
            "overlap": 100,
            "chunker": "fixed",
            "n_docs": len(docs_by_subset["noise120"]) - 16,
            "n_chunks": len(active_only),
            **metrics,
        })

    # F. Production GSE contribution versus cheap generic tokenizers.
    for tokenizer_name, tokenizer in (("gse", gse), ("char", AB.tokenize_char), ("bigram", AB.tokenize_bigram)):
        chunks = build_chunks(docs_by_subset["noise40"], AB.chunk_fixed, 500, 100, tokenizer, embedder)
        for mode in ("bm25", "hybrid"):
            metrics = evaluate(chunks, cases, tokenizer, mode)
            runs.append({
                "group": "tokenizer",
                "name": f"noise40/{tokenizer_name}/{mode}",
                "subset": "noise40",
                "tokenizer": tokenizer_name,
                "mode": mode,
                "top_k": 5,
                "chunk_size": 500,
                "overlap": 100,
                "chunker": "fixed",
                "n_docs": len(docs_by_subset["noise40"]),
                "n_chunks": len(chunks),
                **metrics,
            })

    payload = {
        "protocol": {
            "scope": "expanded offline algorithm benchmark",
            "synthetic_corpus": True,
            "subsets": {name: len(docs) for name, docs in docs_by_subset.items()},
            "queries": len(cases),
            "gold": "current document + exact answer span",
            "hard_negative": "near-duplicate retired policy per current policy",
            "embedding": embedder.description(),
            "tokenizer": "production Go/GSE via evals/rag_tokenize",
            "bm25": "Okapi BM25 k1=1.5 b=0.75, Python evaluation implementation",
            "baseline": "fixed size=500 overlap=100, top_k=5, branch candidates=50, rrf_k=60",
            "latency_note": "algorithm latency after embedding prewarm; not provider or PostgreSQL latency",
            "embedding_api_calls": embedder.api_calls,
            "embedding_api_ms": round(embedder.api_ms, 2),
        },
        "summary": [summarized(run) for run in runs],
        "runs": runs,
    }
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    embedder.flush()

    summary = payload["summary"]
    columns = sorted({key for row in summary for key in row})
    with CSV_OUT.open("w", encoding="utf-8", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=columns)
        writer.writeheader()
        writer.writerows(summary)
    print(f"wrote {OUT} and {CSV_OUT}; runs={len(runs)} api_calls={embedder.api_calls}")
    for row in summary:
        if row["group"] in {"retriever_noise", "chunking", "top_k", "lifecycle_filter", "tokenizer"}:
            print(
                row["group"], row["name"],
                f"recall={row['span_recall_at_k']:.4f}",
                f"mrr={row['mrr']:.4f}",
                f"stale={row['retired_before_gold_rate']:.4f}",
                f"ctx={row['avg_context_chars']:.0f}",
            )


if __name__ == "__main__":
    main()
