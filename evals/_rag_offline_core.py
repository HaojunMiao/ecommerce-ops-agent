#!/usr/bin/env python3
"""Shared tokenizer, embedding and retrieval helpers for RAG evaluation.

This is an algorithm-quality benchmark.  It reuses the production GSE tokenizer
through a tiny Go helper, but BM25 and fusion run in Python so BM25 can be tested
without modifying the deployed PostgreSQL instance.
"""
from __future__ import annotations

import json
import math
import os
import re
import subprocess
import time
import urllib.error
import urllib.request
from collections import defaultdict
from dataclasses import dataclass
from hashlib import sha256
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CACHE_DIR = ROOT / "evals" / "cache"


def load_dotenv(path: Path) -> None:
    if not path.exists():
        return
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        key, value = key.strip(), value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        os.environ.setdefault(key, value)


class RemoteEmbedder:
    """Minimal OpenAI-compatible embedding client with a model-scoped cache."""

    def __init__(self) -> None:
        load_dotenv(ROOT / ".env")
        kind = os.getenv("KBOT_EMBEDDER", "").strip()
        self.base_url = os.getenv("KBOT_EMBEDDER_BASE_URL", "").strip().rstrip("/")
        self.api_key = os.getenv("KBOT_EMBEDDER_API_KEY", "").strip()
        self.model = os.getenv("KBOT_EMBEDDER_MODEL", "").strip()
        self.dim = int(os.getenv("KBOT_EMBEDDER_DIM", "0"))
        self.batch_size = max(1, int(os.getenv("KBOT_EMBEDDER_BATCH_SIZE", "64")))
        if kind != "openai":
            raise RuntimeError("RAG 评测要求 KBOT_EMBEDDER=openai，禁止使用本地哈希向量")
        if not self.base_url or not self.api_key or not self.model or self.dim <= 0:
            raise RuntimeError("RAG 评测缺少 KBOT_EMBEDDER_BASE_URL/API_KEY/MODEL/DIM")
        identity = f"{self.base_url}\0{self.model}\0{self.dim}"
        self.cache_path = CACHE_DIR / (sha256(identity.encode()).hexdigest()[:16] + ".json")
        self.cache: dict[str, list[float]] = {}
        if self.cache_path.exists():
            value = json.loads(self.cache_path.read_text(encoding="utf-8"))
            if isinstance(value, dict):
                self.cache = value
        self.api_calls = 0
        self.api_ms = 0.0

    @staticmethod
    def _key(text: str) -> str:
        return sha256(text.encode("utf-8")).hexdigest()

    def _request(self, texts: list[str]) -> list[list[float]]:
        body = json.dumps({
            "model": self.model,
            "input": texts,
            "dimensions": self.dim,
            "encoding_format": "float",
        }, ensure_ascii=False).encode("utf-8")
        request = urllib.request.Request(
            self.base_url + "/embeddings",
            data=body,
            headers={
                "Authorization": "Bearer " + self.api_key,
                "Content-Type": "application/json",
                "User-Agent": "ecommerce-ops-agent-rag-eval/1.0",
            },
            method="POST",
        )
        last_error: Exception | None = None
        for attempt in range(3):
            started = time.perf_counter()
            try:
                with urllib.request.urlopen(request, timeout=60) as response:
                    payload = json.load(response)
                self.api_calls += 1
                self.api_ms += (time.perf_counter() - started) * 1000
                data = sorted(payload.get("data", []), key=lambda item: item.get("index", 0))
                vectors = [item.get("embedding", []) for item in data]
                if len(vectors) != len(texts) or any(len(vector) != self.dim for vector in vectors):
                    raise RuntimeError("向量接口返回的数量或维度与配置不一致")
                return vectors
            except urllib.error.HTTPError as exc:
                last_error = RuntimeError(
                    f"向量接口 HTTP {exc.code}: {exc.read().decode('utf-8', 'replace')[:300]}"
                )
                if exc.code not in (429, 500, 502, 503, 504):
                    break
            except (urllib.error.URLError, TimeoutError) as exc:
                last_error = exc
            if attempt < 2:
                time.sleep(2**attempt)
        raise RuntimeError(f"向量接口调用失败: {last_error}") from last_error

    def embed_many(self, texts: list[str]) -> list[list[float]]:
        missing = {self._key(text): text for text in texts if self._key(text) not in self.cache}
        items = list(missing.items())
        for start in range(0, len(items), self.batch_size):
            batch = items[start:start + self.batch_size]
            vectors = self._request([text for _, text in batch])
            for (key, _), vector in zip(batch, vectors):
                self.cache[key] = vector
        return [self.cache[self._key(text)] for text in texts]

    def flush(self) -> None:
        CACHE_DIR.mkdir(parents=True, exist_ok=True)
        self.cache_path.write_text(json.dumps(self.cache, separators=(",", ":")), encoding="utf-8")

    def description(self) -> str:
        return f"{self.model} via {self.base_url}, dim={self.dim}"


EMBEDDER: RemoteEmbedder | None = None


def embed_many(texts: list[str]) -> list[list[float]]:
    if EMBEDDER is None:
        raise RuntimeError("embedder is not initialized")
    return EMBEDDER.embed_many(texts)


def chunk_fixed(text: str, size: int, overlap: int) -> list[str]:
    """Character-budget chunker matching production's UTF-8 rune semantics."""
    if size <= 0:
        size = 500
    if overlap < 0 or overlap >= size:
        overlap = size // 5
    paragraphs = [part.strip() for part in text.strip().split("\n\n") if part.strip()]
    if not paragraphs:
        return []
    chunks: list[str] = []
    current = ""
    for paragraph in paragraphs:
        if current and len(current) + len(paragraph) + 1 > size:
            chunks.append(current)
            current = current[-overlap:] if overlap else ""
        current = f"{current}\n{paragraph}" if current else paragraph
        while len(current) > size:
            cut = current[:size]
            chunks.append(cut)
            remainder = current[len(cut):]
            next_value = (cut[-overlap:] if overlap else "") + remainder
            if next_value == current:
                break
            current = next_value
    if current:
        chunks.append(current)
    return chunks


HEADING_RE = re.compile(r"(?m)^(#{1,3})\s+.+$")


def chunk_heading(text: str, size: int, overlap: int) -> list[str]:
    sections: list[str] = []
    buffer: list[str] = []
    for line in text.splitlines(keepends=True):
        if HEADING_RE.match(line) and buffer:
            sections.append("".join(buffer).strip())
            buffer = [line]
        else:
            buffer.append(line)
    if buffer:
        sections.append("".join(buffer).strip())
    output: list[str] = []
    for section in sections:
        if section:
            output.extend([section] if len(section) <= size else chunk_fixed(section, size, overlap))
    return output or chunk_fixed(text, size, overlap)


def cosine(left: list[float], right: list[float]) -> float:
    dot = sum(x * y for x, y in zip(left, right))
    left_norm = math.sqrt(sum(x * x for x in left))
    right_norm = math.sqrt(sum(x * x for x in right))
    return 0.0 if left_norm == 0 or right_norm == 0 else dot / (left_norm * right_norm)


@dataclass
class Chunk:
    id: str
    doc_id: str
    text: str
    embedding: list[float]
    terms: dict[str, int]
    length: int


def term_freq(tokens: list[str]) -> dict[str, int]:
    counts: dict[str, int] = defaultdict(int)
    for token in tokens:
        counts[token] += 1
    return dict(counts)


def bm25_search(chunks: list[Chunk], query: str, tokenize, limit: int):
    k1, b = 1.5, 0.75
    if not chunks:
        return []
    average_length = sum(chunk.length for chunk in chunks) / len(chunks)
    document_frequency: dict[str, int] = defaultdict(int)
    for chunk in chunks:
        for term in chunk.terms:
            document_frequency[term] += 1
    scored = []
    for chunk in chunks:
        score = 0.0
        for term in term_freq(tokenize(query)):
            frequency = float(chunk.terms.get(term, 0))
            if not frequency:
                continue
            df = document_frequency[term]
            inverse_frequency = math.log(1 + (len(chunks) - df + 0.5) / (df + 0.5))
            denominator = frequency + k1 * (1 - b + b * chunk.length / average_length)
            score += inverse_frequency * (frequency * (k1 + 1)) / denominator
        if score > 0:
            scored.append((chunk, score))
    return sorted(scored, key=lambda item: item[1], reverse=True)[:limit]


def vector_search(chunks: list[Chunk], query_vector: list[float], limit: int):
    return sorted(
        ((chunk, cosine(query_vector, chunk.embedding)) for chunk in chunks),
        key=lambda item: item[1], reverse=True,
    )[:limit]


def rrf(rankings: list[list[tuple[Chunk, float]]], k: int, limit: int):
    scores: dict[str, float] = defaultdict(float)
    chunks: dict[str, Chunk] = {}
    for ranking in rankings:
        for rank, (chunk, _) in enumerate(ranking, start=1):
            scores[chunk.id] += 1.0 / (k + rank)
            chunks[chunk.id] = chunk
    return sorted(
        ((chunks[chunk_id], score) for chunk_id, score in scores.items()),
        key=lambda item: item[1], reverse=True,
    )[:limit]


def search(chunks, query: str, tokenize, mode: str, k: int = 5, cand: int = 20, rrf_k: int = 60):
    if mode == "bm25":
        return bm25_search(chunks, query, tokenize, k)
    query_vector = embed_many([query])[0]
    if mode == "vector":
        return vector_search(chunks, query_vector, k)
    return rrf(
        [vector_search(chunks, query_vector, cand), bm25_search(chunks, query, tokenize, cand)],
        rrf_k, k,
    )


def dcg(relevance: list[float]) -> float:
    return sum(value / math.log2(index + 2) for index, value in enumerate(relevance))


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
            pending.append((f"{doc['id']}#{index}", doc["id"], text, term_freq(tokens)))
    vectors = embedder.embed_many([item[2] for item in pending])
    return [
        Chunk(
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
        hits = search(chunks, case["query"], tokenize, mode, k=top_k, cand=cand, rrf_k=rrf_k)
        latencies.append((time.perf_counter() - started) * 1000)
        relevant = [1.0 if case["gold_span"] in chunk.text else 0.0 for chunk, _ in hits[:top_k]]
        first = next((index + 1 for index, value in enumerate(relevant) if value), 0)
        ideal = sorted(relevant, reverse=True)
        ndcg = dcg(relevant) / dcg(ideal) if any(ideal) else 0.0
        context_chars = sum(len(chunk.text) for chunk, _ in hits[:top_k])
        rows.append({
            "id": case["id"],
            "category": case["category"],
            "span_hit": 1.0 if first else 0.0,
            "mrr": 0.0 if not first else 1.0 / first,
            "ndcg": ndcg,
            "first_rank": first,
            "context_chars": context_chars,
            "hits": [{"chunk_id": c.id, "doc_id": c.doc_id, "score": round(score, 8)} for c, score in hits],
        })
    count = len(rows)
    return {
        "hit_rate_at_k": sum(row["span_hit"] for row in rows) / count,
        "mrr": sum(row["mrr"] for row in rows) / count,
        "ndcg_at_k": sum(row["ndcg"] for row in rows) / count,
        "avg_context_chars": sum(row["context_chars"] for row in rows) / count,
        "search_ms_p50": percentile(latencies, 0.50),
        "search_ms_p95": percentile(latencies, 0.95),
        "cases": rows,
    }


def summarized(run: dict) -> dict:
    return {key: value for key, value in run.items() if key != "cases"}
