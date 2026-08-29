#!/usr/bin/env python3
"""Evaluate the deployed Go/PostgreSQL RAG path through the real HTTP API."""
from __future__ import annotations

import json
import math
import os
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CASES = ROOT / "evals" / "rag_production_cases.jsonl"
OUT = ROOT / "evals" / "results" / "rag_production_results.json"


def request_json(base_url: str, method: str, path: str, body=None, token="", workspace_id=""):
    data = None if body is None else json.dumps(body, ensure_ascii=False).encode("utf-8")
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    if workspace_id:
        headers["X-Workspace-ID"] = workspace_id
    req = urllib.request.Request(base_url.rstrip("/") + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=60) as response:
            return json.load(response)
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", "replace")[:500]
        raise RuntimeError(f"{method} {path}: HTTP {exc.code}: {detail}") from exc


def dcg(relevances: list[float]) -> float:
    return sum(rel / math.log2(rank + 2) for rank, rel in enumerate(relevances))


def main() -> None:
    base_url = os.getenv("KBOT_URL", "http://localhost:8080")
    email = os.getenv("KBOT_EMAIL", "admin@ecommerce-ops.local")
    password = os.getenv("KBOT_PASSWORD", "admin12345")
    workspace_name = os.getenv("CROSSBORDER_WORKSPACE", "跨境电商运营平台")
    kb_name = os.getenv("CROSSBORDER_KB_NAME", "跨境电商规则库")
    top_k = int(os.getenv("RAG_EVAL_TOP_K", "5"))

    login = request_json(base_url, "POST", "/api/v1/auth/login", {"email": email, "password": password})
    token = login["token"]
    workspaces = request_json(base_url, "GET", "/api/v1/workspaces", token=token)
    workspace = next((item for item in workspaces if item["name"] == workspace_name), None)
    if workspace is None:
        raise RuntimeError(f"workspace {workspace_name!r} not found; run crossborder-install first")
    workspace_id = workspace["id"]
    kbs = request_json(base_url, "GET", "/api/v1/kbs", token=token, workspace_id=workspace_id)
    kb = next((item for item in kbs if item["name"] == kb_name), None)
    if kb is None:
        raise RuntimeError(f"knowledge base {kb_name!r} not found; run crossborder-install first")
    if kb.get("status") != "active":
        raise RuntimeError(f"knowledge base is {kb.get('status')!r}; wait for worker ingest to finish")

    documents = request_json(
        base_url, "GET", f"/api/v1/kbs/{kb['id']}/documents", token=token, workspace_id=workspace_id,
    )
    source_to_doc = {
        Path(item["source_uri"]).name: item["id"]
        for item in documents if item.get("status") == "processed"
    }
    cases = [json.loads(line) for line in CASES.read_text(encoding="utf-8").splitlines() if line.strip()]
    report = {
        "scope": "deployed Go/PostgreSQL SearchMode via HTTP API",
        "kb_id": kb["id"],
        "embedding_identity": kb.get("embedding_model", ""),
        "top_k": top_k,
        "modes": {},
    }

    for mode in ("bm25", "vector", "hybrid"):
        rows = []
        for case in cases:
            expected = {source_to_doc[name] for name in case["gold_sources"] if name in source_to_doc}
            if not expected:
                raise RuntimeError(f"case {case['id']} has no ingested gold document: {case['gold_sources']}")
            started = time.perf_counter()
            hits = request_json(
                base_url, "POST", f"/api/v1/kbs/{kb['id']}/search",
                {"query": case["query"], "mode": mode, "top_k": top_k}, token, workspace_id,
            )
            latency_ms = (time.perf_counter() - started) * 1000
            # 主指标按标注的具体规则片段计算。当前演示语料只有两份文档，若只按
            # doc_id 判断相关性，top-5 几乎必然命中文档，会掩盖错误 chunk 排名。
            relevances = [1.0 if case["gold_span"] in hit["text"] else 0.0 for hit in hits[:top_k]]
            ideal = sorted(relevances, reverse=True)
            ideal_dcg = dcg(ideal)
            first_rank = next((i + 1 for i, rel in enumerate(relevances) if rel), 0)
            rows.append({
                "id": case["id"],
                "recall_at_k": 1.0 if any(relevances) else 0.0,
                "mrr": 0.0 if first_rank == 0 else 1.0 / first_rank,
                "ndcg_at_k": 0.0 if ideal_dcg == 0 else dcg(relevances) / ideal_dcg,
                "source_recall_at_k": len({hit["doc_id"] for hit in hits[:top_k]} & expected) / len(expected),
                "latency_ms": round(latency_ms, 2),
                "hits": hits,
            })
        count = len(rows)
        report["modes"][mode] = {
            "recall_at_k": sum(row["recall_at_k"] for row in rows) / count,
            "mrr": sum(row["mrr"] for row in rows) / count,
            "ndcg_at_k": sum(row["ndcg_at_k"] for row in rows) / count,
            "source_recall_at_k": sum(row["source_recall_at_k"] for row in rows) / count,
            "latency_ms_mean": sum(row["latency_ms"] for row in rows) / count,
            "cases": rows,
        }

    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"wrote {OUT}")
    for mode, metrics in report["modes"].items():
        print(mode, {key: round(value, 4) for key, value in metrics.items() if key != "cases"})


if __name__ == "__main__":
    main()
