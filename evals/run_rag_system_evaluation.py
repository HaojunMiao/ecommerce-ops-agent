#!/usr/bin/env python3
"""Evaluate the fixed corpus through the real Go/PostgreSQL RAG API."""
from __future__ import annotations

import json
import math
import os
import statistics
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_CASES_PATH = ROOT / "evals" / "rag_evaluation_cases.jsonl"
DEFAULT_OUT = ROOT / "evals" / "results" / "rag_evaluation_system.json"


def request_json(base_url: str, method: str, path: str, body=None, token="", workspace_id=""):
    data = None if body is None else json.dumps(body, ensure_ascii=False).encode("utf-8")
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    if workspace_id:
        headers["X-Workspace-ID"] = workspace_id
    request = urllib.request.Request(base_url.rstrip("/") + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=120) as response:
            return json.load(response)
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", "replace")[:1000]
        raise RuntimeError(f"{method} {path}: HTTP {exc.code}: {detail}") from exc


def percentile(values: list[float], p: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    pos = (len(ordered) - 1) * p
    lo, hi = math.floor(pos), math.ceil(pos)
    if lo == hi:
        return ordered[lo]
    return ordered[lo] * (hi - pos) + ordered[hi] * (pos - lo)


def dcg(values: list[float]) -> float:
    return sum(value / math.log2(index + 2) for index, value in enumerate(values))


def evaluate(base_url, token, workspace_id, kb_id, cases, source_to_doc, mode, top_k):
    rows = []
    latencies = []
    for case in cases:
        started = time.perf_counter()
        hits = request_json(
            base_url,
            "POST",
            f"/api/v1/kbs/{kb_id}/search",
            {"query": case["query"], "mode": "bm25" if mode == "pg_fts" else mode, "top_k": top_k},
            token,
            workspace_id,
        ) or []
        elapsed = (time.perf_counter() - started) * 1000
        latencies.append(elapsed)
        gold_doc = source_to_doc.get(case["gold_docs"][0] + ".md", "")
        relevant = [
            1.0 if hit["doc_id"] == gold_doc and case["gold_span"] in hit["text"] else 0.0
            for hit in hits[:top_k]
        ]
        first = next((i + 1 for i, value in enumerate(relevant) if value), 0)
        ideal = sorted(relevant, reverse=True)
        rows.append({
            "id": case["id"],
            "span_hit": 1.0 if first else 0.0,
            "precision": sum(relevant) / len(hits[:top_k]) if hits[:top_k] else 0.0,
            "mrr": 0.0 if not first else 1.0 / first,
            "ndcg": dcg(relevant) / dcg(ideal) if any(ideal) else 0.0,
            "first_rank": first,
            "context_chars": sum(len(hit["text"]) for hit in hits[:top_k]),
            "latency_ms": round(elapsed, 2),
            "hits": [
                {
                    "doc_id": hit.get("doc_id"),
                    "chunk_id": hit.get("chunk_id"),
                    "score": hit.get("score"),
                }
                for hit in hits
            ],
        })
    count = len(rows)
    result = {
        "hit_rate_at_1": sum(1.0 for row in rows if row["first_rank"] == 1) / count,
        "hit_rate_at_3": sum(1.0 for row in rows if 0 < row["first_rank"] <= 3) / count,
        "hit_rate_at_k": sum(row["span_hit"] for row in rows) / count,
        "precision_at_k": sum(row["precision"] for row in rows) / count,
        "mrr": sum(row["mrr"] for row in rows) / count,
        "ndcg_at_k": sum(row["ndcg"] for row in rows) / count,
        "avg_context_chars": sum(row["context_chars"] for row in rows) / count,
        "latency_ms_mean": statistics.mean(latencies),
        "latency_ms_p50": percentile(latencies, 0.50),
        "latency_ms_p95": percentile(latencies, 0.95),
        "cases": rows,
    }
    if any("split" in case for case in cases):
        case_by_id = {case["id"]: case for case in cases}
        result["by_split"] = {}
        for split in ("dev", "test"):
            selected = [row for row in rows if case_by_id[row["id"]].get("split") == split]
            if not selected:
                continue
            result["by_split"][split] = {
                "queries": len(selected),
                "hit_rate_at_1": sum(row["first_rank"] == 1 for row in selected) / len(selected),
                "hit_rate_at_3": sum(0 < row["first_rank"] <= 3 for row in selected) / len(selected),
                "hit_rate_at_k": sum(row["span_hit"] for row in selected) / len(selected),
                "precision_at_k": sum(row["precision"] for row in selected) / len(selected),
                "mrr": sum(row["mrr"] for row in selected) / len(selected),
                "ndcg_at_k": sum(row["ndcg"] for row in selected) / len(selected),
                "avg_context_chars": sum(row["context_chars"] for row in selected) / len(selected),
            }
        result["by_category"] = {}
        for category in sorted({case.get("category", "unknown") for case in cases}):
            selected = [row for row in rows if case_by_id[row["id"]].get("category", "unknown") == category]
            result["by_category"][category] = {
                "queries": len(selected),
                "hit_rate_at_1": sum(row["first_rank"] == 1 for row in selected) / len(selected),
                "hit_rate_at_3": sum(0 < row["first_rank"] <= 3 for row in selected) / len(selected),
                "hit_rate_at_k": sum(row["span_hit"] for row in selected) / len(selected),
                "precision_at_k": sum(row["precision"] for row in selected) / len(selected),
                "mrr": sum(row["mrr"] for row in selected) / len(selected),
            }
    return result


def ensure_workspace(base_url, token, name):
    workspaces = request_json(base_url, "GET", "/api/v1/workspaces", token=token)
    existing = next((item for item in workspaces if item["name"] == name), None)
    if existing:
        return existing["id"]
    created = request_json(
        base_url,
        "POST",
        "/api/v1/workspaces",
        {"name": name, "description": "合成RAG评测专用Workspace"},
        token,
    )
    return created["id"]


def ensure_kb(base_url, token, workspace_id, name):
    kbs = request_json(base_url, "GET", "/api/v1/kbs", token=token, workspace_id=workspace_id)
    existing = next((item for item in kbs if item["name"] == name), None)
    if existing:
        return existing["id"]
    created = request_json(base_url, "POST", "/api/v1/kbs", {"name": name}, token, workspace_id)
    return created["id"]


def wait_ready(base_url, token, workspace_id, kb_id, expected_docs, timeout_s=1200):
    deadline = time.time() + timeout_s
    last = ""
    while time.time() < deadline:
        docs = request_json(
            base_url, "GET", f"/api/v1/kbs/{kb_id}/documents", token=token, workspace_id=workspace_id
        )
        counts = {}
        for doc in docs:
            counts[doc.get("status", "unknown")] = counts.get(doc.get("status", "unknown"), 0) + 1
        last = f"documents={len(docs)}/{expected_docs} status={counts}"
        if len(docs) == expected_docs and counts.get("processed", 0) == expected_docs:
            return docs
        print("waiting:", last, flush=True)
        time.sleep(5)
    raise TimeoutError("benchmark KB did not become ready: " + last)


def main() -> None:
    cases_path = Path(os.getenv("RAG_EVAL_CASES", str(DEFAULT_CASES_PATH)))
    out_path = Path(os.getenv("RAG_EVAL_OUTPUT", str(DEFAULT_OUT)))
    corpus_dir = Path(os.getenv("RAG_EVAL_CORPUS_DIR", str(ROOT / "evals" / "corpus" / "rag-evaluation")))
    container_root = os.getenv("RAG_EVAL_CONTAINER_ROOT", "/scenarios/rag-evaluation")
    kb_prefix = os.getenv("RAG_EVAL_KB_PREFIX", "RAG评测")
    full_matrix = os.getenv("RAG_EVAL_FULL_MATRIX", "false").lower() == "true"
    if not cases_path.exists():
        subprocess.run([sys.executable, "evals/build_rag_evaluation.py"], cwd=ROOT, check=True)
    base_url = os.getenv("KBOT_URL", "http://localhost:8080")
    email = os.getenv("KBOT_EMAIL", "admin@ecommerce-ops.local")
    password = os.getenv("KBOT_PASSWORD", "admin12345")
    workspace_name = os.getenv("RAG_EVAL_WORKSPACE", "RAG检索评测")
    subsets = [
        item.strip()
        for item in os.getenv("RAG_EVAL_SUBSETS", "core,noise120").split(",")
        if item.strip()
    ]
    cases = [json.loads(line) for line in cases_path.read_text(encoding="utf-8").splitlines() if line.strip()]

    login = request_json(base_url, "POST", "/api/v1/auth/login", {"email": email, "password": password})
    token = login["token"]
    workspace_id = ensure_workspace(base_url, token, workspace_name)
    report = {
        "protocol": {
            "scope": "deployed Go/GSE/PostgreSQL/pgvector/Qwen path via HTTP",
            "synthetic_corpus": True,
            "queries": len(cases),
            "subsets": subsets,
            "top_k": 5,
            "latency": "首个 subset 的首次 vector pass 包含冷 query embedding；后续请求复用进程内缓存",
        },
        "subsets": {},
    }

    for subset in subsets:
        source_dir = corpus_dir / subset
        expected_docs = len(list(source_dir.glob("*.md")))
        kb_id = ensure_kb(base_url, token, workspace_id, f"{kb_prefix}-{subset}")
        request_json(
            base_url,
            "POST",
            f"/api/v1/kbs/{kb_id}/connectors/markdown/sync",
            {"root_path": f"{container_root}/{subset}"},
            token,
            workspace_id,
        )
        docs = wait_ready(base_url, token, workspace_id, kb_id, expected_docs)
        source_to_doc = {Path(item["source_uri"]).name: item["id"] for item in docs}

        # Cold pass measures the external query-embedding path and fills the app cache.
        cold = evaluate(base_url, token, workspace_id, kb_id, cases, source_to_doc, "vector", 1)
        result = {
            "kb_id": kb_id,
            "documents": expected_docs,
            "query_embedding_pass_latency": {
                "cache_state": "cold" if subset == subsets[0] else "warm_from_previous_subset",
                "mean": cold["latency_ms_mean"],
                "p50": cold["latency_ms_p50"],
                "p95": cold["latency_ms_p95"],
            },
            "modes": {},
        }
        for mode in ("pg_fts", "vector", "hybrid"):
            result["modes"][mode] = evaluate(
                base_url, token, workspace_id, kb_id, cases, source_to_doc, mode, 5
            )
        if subset == subsets[-1] and full_matrix:
            result["top_k"] = {
                str(top_k): evaluate(base_url, token, workspace_id, kb_id, cases, source_to_doc, "hybrid", top_k)
                for top_k in (1, 3, 5, 8, 10)
            }
            result["top_k_modes"] = {
                mode: {
                    str(top_k): evaluate(base_url, token, workspace_id, kb_id, cases, source_to_doc, mode, top_k)
                    for top_k in (1, 3, 5, 8, 10)
                }
                for mode in ("pg_fts", "vector", "hybrid")
            }
        report["subsets"][subset] = result

    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"wrote {out_path}")
    for subset, result in report["subsets"].items():
        for mode, metrics in result["modes"].items():
            print(
                subset, mode,
                f"hit@{metrics['top_k']}={metrics['hit_rate_at_k']:.4f}",
                f"mrr={metrics['mrr']:.4f}",
                f"p95={metrics['latency_ms_p95']:.2f}ms",
            )


if __name__ == "__main__":
    main()
