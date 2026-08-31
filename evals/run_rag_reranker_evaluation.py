#!/usr/bin/env python3
"""Run a 2x2 chunking/reranker ablation on the fixed 100-query test set.

The four cells are 500/100 and 1200/200, each with equal-RRF Top-5 or
equal-RRF Top-10 reranked to Top-5. Chunking was selected on the separate
dev split; the reranker model and candidate size were fixed before this run.
"""
from __future__ import annotations

import hashlib
import importlib.util
import json
import os
import random
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "evals" / "results" / "rag_evaluation_reranker.json"
CACHE = ROOT / "evals" / "cache" / "reranker_qwen3_4b.json"


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


LONG = load_module("rag_evaluation_shared", ROOT / "evals" / "run_rag_evaluation.py")
SHORT = LONG.SHORT
AB = LONG.AB


class RemoteReranker:
    def __init__(self) -> None:
        AB.load_dotenv(ROOT / ".env")
        self.base_url = os.getenv(
            "KBOT_RERANKER_BASE_URL", os.getenv("KBOT_EMBEDDER_BASE_URL", "")
        ).strip().rstrip("/")
        self.api_key = os.getenv(
            "KBOT_RERANKER_API_KEY", os.getenv("KBOT_EMBEDDER_API_KEY", "")
        ).strip()
        self.model = os.getenv("KBOT_RERANKER_MODEL", "Qwen/Qwen3-Reranker-4B").strip()
        if not self.base_url or not self.api_key or not self.model:
            raise RuntimeError("missing reranker base URL, API key or model")
        self.cache = json.loads(CACHE.read_text(encoding="utf-8")) if CACHE.exists() else {}
        self.api_calls = 0
        self.api_ms = 0.0

    def rerank(self, query: str, hits, top_n: int):
        documents = [chunk.text for chunk, _ in hits]
        cache_key = hashlib.sha256(
            json.dumps(
                [self.base_url, self.model, query, documents, top_n],
                ensure_ascii=False,
                separators=(",", ":"),
            ).encode("utf-8")
        ).hexdigest()
        cached = self.cache.get(cache_key)
        if cached is None:
            body = json.dumps(
                {
                    "model": self.model,
                    "query": query,
                    "documents": documents,
                    "top_n": top_n,
                    "return_documents": False,
                },
                ensure_ascii=False,
            ).encode("utf-8")
            request = urllib.request.Request(
                self.base_url + "/rerank",
                data=body,
                headers={
                    "Authorization": "Bearer " + self.api_key,
                    "Content-Type": "application/json",
                    "User-Agent": "ecommerce-ops-agent-reranker-eval/1.0",
                },
                method="POST",
            )
            last_error = None
            for attempt in range(3):
                started = time.perf_counter()
                try:
                    with urllib.request.urlopen(request, timeout=60) as response:
                        payload = json.load(response)
                    self.api_calls += 1
                    self.api_ms += (time.perf_counter() - started) * 1000
                    cached = payload.get("results", [])
                    break
                except urllib.error.HTTPError as exc:
                    detail = exc.read().decode("utf-8", "replace")[:500]
                    last_error = RuntimeError(f"rerank HTTP {exc.code}: {detail}")
                    if exc.code not in (429, 500, 502, 503, 504):
                        break
                except (urllib.error.URLError, TimeoutError) as exc:
                    last_error = exc
                if attempt < 2:
                    time.sleep(2**attempt)
            if cached is None:
                raise RuntimeError(f"rerank request failed: {last_error}") from last_error
            self.cache[cache_key] = cached
            CACHE.parent.mkdir(parents=True, exist_ok=True)
            CACHE.write_text(json.dumps(self.cache, separators=(",", ":")), encoding="utf-8")

        output = []
        seen = set()
        for result in sorted(cached, key=lambda item: item["relevance_score"], reverse=True):
            index = int(result["index"])
            if index < 0 or index >= len(hits) or index in seen:
                raise RuntimeError(f"invalid rerank result index: {index}")
            seen.add(index)
            output.append((hits[index][0], float(result["relevance_score"])))
            if len(output) == top_n:
                break
        if len(output) != min(top_n, len(hits)):
            raise RuntimeError(f"reranker returned {len(output)} results, expected {min(top_n, len(hits))}")
        return output


def metric_row(case: dict, chunks, hits, latency_ms: float) -> dict:
    gold_docs = set(case["gold_docs"])
    relevant_ids = {
        chunk.id for chunk in chunks
        if chunk.doc_id in gold_docs and case["gold_span"] in chunk.text
    }
    relevance = [1.0 if chunk.id in relevant_ids else 0.0 for chunk, _ in hits[:5]]
    first = next((index + 1 for index, value in enumerate(relevance) if value), 0)
    ideal = [1.0] * min(len(relevant_ids), 5)
    ideal.extend([0.0] * (5 - len(ideal)))
    retrieved_relevant = int(sum(relevance))
    ideal_dcg = AB.dcg(ideal)
    return {
        "id": case["id"],
        "split": case["split"],
        "category": case["category"],
        "hit": 1.0 if first else 0.0,
        "precision": retrieved_relevant / len(hits[:5]) if hits[:5] else 0.0,
        "chunk_recall": retrieved_relevant / len(relevant_ids) if relevant_ids else 0.0,
        "mrr": 0.0 if not first else 1.0 / first,
        "ndcg": AB.dcg(relevance) / ideal_dcg if ideal_dcg else 0.0,
        "first_rank": first,
        "context_chars": sum(len(chunk.text) for chunk, _ in hits[:5]),
        "latency_ms": latency_ms,
        "hits": [
            {"chunk_id": chunk.id, "doc_id": chunk.doc_id, "score": round(score, 8)}
            for chunk, score in hits[:5]
        ],
    }


def report(rows: list[dict]) -> dict:
    result = LONG.aggregate(rows)
    result["by_split"] = {
        split: LONG.aggregate([row for row in rows if row["split"] == split])
        for split in sorted({row["split"] for row in rows})
    }
    result["by_category"] = {
        category: LONG.aggregate([row for row in rows if row["category"] == category])
        for category in sorted({row["category"] for row in rows})
    }
    result["cases"] = rows
    return result


def compare(base_rows: list[dict], treatment_rows: list[dict]) -> dict:
    base = {row["id"]: row for row in base_rows}
    wins = ties = losses = 0
    changed = []
    for row in treatment_rows:
        delta = row["mrr"] - base[row["id"]]["mrr"]
        if delta > 0:
            wins += 1
        elif delta < 0:
            losses += 1
        else:
            ties += 1
        if delta != 0:
            changed.append({
                "id": row["id"],
                "split": row["split"],
                "before_rank": base[row["id"]]["first_rank"],
                "after_rank": row["first_rank"],
                "mrr_delta": delta,
            })
    return {"mrr_wins": wins, "mrr_ties": ties, "mrr_losses": losses, "changed_cases": changed}


def metric_delta(base: dict, treatment: dict) -> dict:
    return {
        "hit_rate_at_5": treatment["hit_rate_at_k"] - base["hit_rate_at_k"],
        "precision_at_5": treatment["precision_at_k"] - base["precision_at_k"],
        "mrr": treatment["mrr"] - base["mrr"],
        "ndcg_at_5": treatment["ndcg_at_k"] - base["ndcg_at_k"],
        "avg_context_chars": treatment["avg_context_chars"] - base["avg_context_chars"],
    }


def cluster_bootstrap_ci(
    base_rows: list[dict], treatment_rows: list[dict], samples: int = 10000
) -> dict:
    """Bootstrap paired deltas by policy, keeping each policy's four queries together."""
    base = {row["id"]: row for row in base_rows}
    treatment = {row["id"]: row for row in treatment_rows}
    policies: dict[str, list[str]] = {}
    for case_id in sorted(base):
        policies.setdefault(case_id.rsplit("_", 1)[0], []).append(case_id)
    names = sorted(policies)
    output = {}
    for offset, metric in enumerate(("hit", "mrr")):
        deltas = [
            sum(treatment[case_id][metric] - base[case_id][metric] for case_id in policies[name])
            / len(policies[name])
            for name in names
        ]
        rng = random.Random(20260831 + offset)
        estimates = sorted(
            sum(deltas[rng.randrange(len(deltas))] for _ in deltas) / len(deltas)
            for _ in range(samples)
        )
        output[metric] = {
            "low": estimates[int(samples * 0.025)],
            "high": estimates[int(samples * 0.975) - 1],
            "samples": samples,
            "cluster": "policy",
        }
    return output


def comparison(base_name: str, treatment_name: str, runs: dict, rows_by_run: dict) -> dict:
    return {
        "delta": metric_delta(runs[base_name], runs[treatment_name]),
        "paired": compare(rows_by_run[base_name], rows_by_run[treatment_name]),
        "bootstrap_95_ci": cluster_bootstrap_ci(
            rows_by_run[base_name], rows_by_run[treatment_name]
        ),
    }


def main() -> None:
    docs = LONG.load_docs("noise120")
    cases = [case for case in LONG.load_cases() if case["split"] == "test"]
    gse = SHORT.GSETokenCache()
    embedder = AB.RemoteEmbedder()
    AB.EMBEDDER = embedder
    gse.preload([case["query"] for case in cases])
    reranker = RemoteReranker()
    configs = ((500, 100), (1200, 200))
    all_texts = [
        text
        for size, overlap in configs
        for doc in docs
        for text in AB.chunk_fixed(doc["text"], size, overlap)
    ]
    gse.preload(all_texts)

    runs: dict[str, dict] = {}
    rows_by_run: dict[str, list[dict]] = {}
    candidate_recall: dict[str, dict[str, float]] = {}
    for size, overlap in configs:
        config_name = f"{size}_{overlap}"
        chunks = SHORT.build_chunks(docs, AB.chunk_fixed, size, overlap, gse, embedder)
        baseline_rows = []
        rerank_rows = []
        candidate_hits = {5: [], 10: []}
        for number, case in enumerate(cases, start=1):
            started = time.perf_counter()
            candidates10 = AB.search(
                chunks, case["query"], gse, "hybrid", k=10, cand=50, rrf_k=60
            )
            retrieval_ms = (time.perf_counter() - started) * 1000
            baseline_rows.append(metric_row(case, chunks, candidates10[:5], retrieval_ms))

            relevant_ids = {
                chunk.id for chunk in chunks
                if chunk.doc_id in set(case["gold_docs"]) and case["gold_span"] in chunk.text
            }
            for k in (5, 10):
                candidate_hits[k].append(
                    1.0 if any(chunk.id in relevant_ids for chunk, _ in candidates10[:k]) else 0.0
                )

            started = time.perf_counter()
            reranked = reranker.rerank(case["query"], candidates10, 5)
            rerank_rows.append(
                metric_row(
                    case,
                    chunks,
                    reranked,
                    retrieval_ms + (time.perf_counter() - started) * 1000,
                )
            )
            print(f"[{config_name} {number}/{len(cases)}] {case['id']}", flush=True)

        baseline_name = f"chunk_{config_name}_rrf_top5"
        rerank_name = f"chunk_{config_name}_rerank_top10_to_top5"
        rows_by_run[baseline_name] = baseline_rows
        rows_by_run[rerank_name] = rerank_rows
        runs[baseline_name] = report(baseline_rows)
        runs[rerank_name] = report(rerank_rows)
        candidate_recall[config_name] = {
            str(k): sum(values) / len(values) for k, values in candidate_hits.items()
        }

    base500 = "chunk_500_100_rrf_top5"
    rerank500 = "chunk_500_100_rerank_top10_to_top5"
    base1200 = "chunk_1200_200_rrf_top5"
    rerank1200 = "chunk_1200_200_rerank_top10_to_top5"
    payload = {
        "protocol": {
            "corpus": f"noise120 ({len(docs)} documents)",
            "queries": len(cases),
            "split": "test only; chunking selected on the separate dev split",
            "chunking": ["fixed 500/100", "fixed 1200/200"],
            "retrieval": "Okapi BM25 + Qwen vector + equal RRF(k=60)",
            "reranker": reranker.model,
            "candidate_top_k": 10,
            "final_top_k": 5,
            "candidate_hit_rate": candidate_recall,
            "embedding_api_calls": embedder.api_calls,
            "embedding_api_ms": embedder.api_ms,
            "reranker_api_calls": reranker.api_calls,
            "reranker_api_ms": reranker.api_ms,
            "selection": "1200/200 selected on dev; model and candidate_k=10 fixed before test evaluation; no RRF weight tuning",
        },
        "runs": runs,
        "comparisons": {
            "chunk_only_500_to_1200": comparison(base500, base1200, runs, rows_by_run),
            "reranker_on_500_100": comparison(base500, rerank500, runs, rows_by_run),
            "reranker_on_1200_200": comparison(base1200, rerank1200, runs, rows_by_run),
            "combined_500_plain_to_1200_reranked": comparison(
                base500, rerank1200, runs, rows_by_run
            ),
        },
    }
    OUT.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    embedder.flush()
    print(f"wrote {OUT}; reranker_api_calls={reranker.api_calls}")
    for name, metrics in runs.items():
        print(
            name,
            f"test_hit@5={metrics['hit_rate_at_k']:.4f}",
            f"test_mrr={metrics['mrr']:.4f}",
            f"p95_ms={metrics['search_ms_p95']:.2f}",
        )


if __name__ == "__main__":
    main()
