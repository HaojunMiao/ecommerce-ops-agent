#!/usr/bin/env python3
"""Low-cost extractive answer correctness and citation-faithfulness smoke test.

The script deterministically samples three test queries from each query category
and reuses the stored Hybrid Top-5 output.  It makes at most one
short chat-completion request per case and never uses an LLM-as-a-judge.
"""
from __future__ import annotations

import importlib.util
import json
import os
import random
import sys
import urllib.error
import urllib.request
from collections import defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CORPUS = ROOT / "evals" / "corpus" / "rag-evaluation" / "noise120"
CASES = ROOT / "evals" / "rag_evaluation_cases.jsonl"
RETRIEVAL = ROOT / "evals" / "results" / "rag_evaluation_offline.json"
OUT = ROOT / "evals" / "results" / "rag_evaluation_answer_smoke.json"
SAMPLE_PER_CATEGORY = 2
SEED = 20260831
ABSTAIN = "无法根据给定资料回答"


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


def load_dotenv() -> dict[str, str]:
    values = dict(os.environ)
    path = ROOT / ".env"
    if path.exists():
        for line in path.read_text(encoding="utf-8").splitlines():
            stripped = line.strip()
            if not stripped or stripped.startswith("#") or "=" not in stripped:
                continue
            key, value = stripped.split("=", 1)
            values.setdefault(key.strip(), value.strip())
    return values


def parse_json(text: str) -> dict:
    stripped = text.strip()
    if stripped.startswith("```"):
        stripped = stripped.split("\n", 1)[-1].rsplit("```", 1)[0].strip()
    try:
        return json.loads(stripped)
    except json.JSONDecodeError:
        return {"answer": "", "chunk_id": "", "format_error": stripped[:300]}


def chat(base_url: str, api_key: str, model: str, query: str, passages: list[dict]) -> tuple[dict, dict]:
    context = "\n\n".join(f"[{item['chunk_id']}]\n{item['text']}" for item in passages)
    prompt = f"""请根据资料回答问题。严格遵守：
1. answer必须是资料中能够直接回答问题的最短原文片段，不得改写、解释或补充。
2. chunk_id必须是answer所在资料的方括号编号。
3. 如果资料不足，answer必须填写“{ABSTAIN}”，chunk_id填写空字符串。
4. 只输出JSON：{{"answer":"...","chunk_id":"..."}}

问题：{query}

资料：
{context}"""
    body = json.dumps({
        "model": model,
        "messages": [
            {"role": "system", "content": "你是严格的抽取式问答器，只能逐字引用给定资料。"},
            {"role": "user", "content": prompt},
        ],
        "temperature": 0,
        "max_tokens": 192,
        "response_format": {"type": "json_object"},
    }, ensure_ascii=False).encode("utf-8")
    request = urllib.request.Request(
        base_url.rstrip("/") + "/chat/completions",
        data=body,
        headers={"Authorization": "Bearer " + api_key, "Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            payload = json.load(response)
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", "replace")[:500]
        raise RuntimeError(f"answer evaluation HTTP {exc.code}: {detail}") from exc
    content = payload["choices"][0]["message"]["content"]
    return parse_json(content), payload.get("usage", {})


def main() -> None:
    if OUT.exists() and os.getenv("RAG_ANSWER_EVAL_REFRESH", "false").lower() != "true":
        print(f"reuse {OUT}; set RAG_ANSWER_EVAL_REFRESH=true to rerun")
        return

    env = load_dotenv()
    base_url = env.get("KBOT_ANSWER_EVAL_BASE_URL", env.get("KBOT_EMBEDDER_BASE_URL", "")).strip()
    api_key = env.get("KBOT_ANSWER_EVAL_API_KEY", env.get("KBOT_EMBEDDER_API_KEY", "")).strip()
    model = env.get("KBOT_ANSWER_EVAL_MODEL", "Qwen/Qwen2.5-7B-Instruct").strip()
    if not base_url or not api_key or not model:
        raise RuntimeError("missing answer evaluation base URL, API key or model")

    runner = load_module("rag_answer_smoke_shared", ROOT / "evals" / "run_rag_evaluation.py")
    cases = {
        item["id"]: item
        for item in (json.loads(line) for line in CASES.read_text(encoding="utf-8").splitlines() if line.strip())
    }
    retrieval = json.loads(RETRIEVAL.read_text(encoding="utf-8"))
    run = next(
        item for item in retrieval["runs"]
        if item["group"] == "top_k" and item.get("subset") == "noise120" and item.get("top_k") == 5
    )
    rows = run["cases"]
    test_rows = [row for row in rows if row["split"] == "test"]

    grouped: dict[str, list[dict]] = defaultdict(list)
    for row in test_rows:
        grouped[row["category"]].append(row)
    rng = random.Random(SEED)
    selected = []
    for category in sorted(grouped):
        selected.extend(rng.sample(grouped[category], min(SAMPLE_PER_CATEGORY, len(grouped[category]))))
    selected.sort(key=lambda row: (row["category"], row["id"]))

    chunks = {}
    for path in sorted(CORPUS.glob("*.md")):
        for ordinal, text in enumerate(runner.AB.chunk_fixed(path.read_text(encoding="utf-8"), 1200, 200)):
            chunks[f"{path.stem}#{ordinal}"] = {"doc_id": path.stem, "text": text}

    output_rows = []
    usage = defaultdict(int)
    for index, retrieval_row in enumerate(selected, start=1):
        case = cases[retrieval_row["id"]]
        passages = []
        for hit in retrieval_row["hits"][:5]:
            chunk = chunks[hit["chunk_id"]]
            passages.append({"chunk_id": hit["chunk_id"], "doc_id": chunk["doc_id"], "text": chunk["text"]})
        answer, item_usage = chat(base_url, api_key, model, case["query"], passages)
        for key, value in item_usage.items():
            if isinstance(value, int):
                usage[key] += value

        answer_text = str(answer.get("answer", "")).strip()
        cited_id = str(answer.get("chunk_id", "")).strip()
        passage_by_id = {item["chunk_id"]: item for item in passages}
        cited = passage_by_id.get(cited_id)
        evidence_available = any(
            item["doc_id"] in set(case["gold_docs"]) and case["gold_span"] in item["text"]
            for item in passages
        )
        abstained = answer_text == ABSTAIN
        citation_valid = bool(cited and answer_text and answer_text in cited["text"])
        answer_correct = bool(
            cited
            and cited["doc_id"] in set(case["gold_docs"])
            and case["gold_span"] in answer_text
            and citation_valid
        )
        refusal_correct = bool(not evidence_available and abstained)
        output_rows.append({
            "id": case["id"],
            "category": case["category"],
            "query": case["query"],
            "gold_span": case["gold_span"],
            "evidence_available": evidence_available,
            "answer": answer_text,
            "cited_chunk_id": cited_id,
            "abstained": abstained,
            "citation_valid": citation_valid,
            "answer_correct": answer_correct,
            "refusal_correct": refusal_correct,
        })
        print(f"[{index}/{len(selected)}] {case['id']}", flush=True)

    answerable = [row for row in output_rows if row["evidence_available"]]
    answered = [row for row in output_rows if row["answer"] and not row["abstained"]]
    unanswerable = [row for row in output_rows if not row["evidence_available"]]
    metrics = {
        "sample_size": len(output_rows),
        "evidence_available_rate": sum(row["evidence_available"] for row in output_rows) / len(output_rows),
        "strict_gold_span_with_valid_citation_rate": sum(row["answer_correct"] for row in output_rows) / len(output_rows),
        "strict_answerable_correct_rate": (
            sum(row["answer_correct"] for row in answerable) / len(answerable) if answerable else None
        ),
        "valid_generated_chunk_id_rate": (
            sum(row["citation_valid"] for row in answered) / len(answered) if answered else None
        ),
        "correct_refusal_rate": (
            sum(row["refusal_correct"] for row in unanswerable) / len(unanswerable) if unanswerable else None
        ),
    }
    payload = {
        "protocol": {
            "scope": "low-cost extractive answer smoke test; not LLM-judged RAGAS Faithfulness",
            "model": model,
            "retrieval": "stored offline 1200/200 hybrid equal-RRF top-5",
            "sampling": f"{SAMPLE_PER_CATEGORY} deterministic test cases per category, seed={SEED}",
            "max_requests": len(selected),
            "max_output_tokens_per_request": 192,
            "case_answer_correct_definition": "strict Gold span plus valid generated citation; semantic correctness requires manual review",
        },
        "automatic_strict_metrics": metrics,
        "usage": dict(usage),
        "cases": output_rows,
    }
    OUT.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(metrics, ensure_ascii=False, indent=2))
    print("usage", json.dumps(dict(usage), ensure_ascii=False))
    print(f"wrote {OUT}")


if __name__ == "__main__":
    main()
