#!/usr/bin/env python3
"""Offline RAG algorithm ablation using the same real embedding model as runtime.

This script intentionally owns experimental Python implementations of chunking,
tokenization, Okapi BM25 and RRF so different strategies can be compared cheaply.
It is not a production-path benchmark: production uses GSE + PostgreSQL
ts_rank_cd + 50 candidates per branch. Use evals/run_rag_production_eval.py for
metrics produced by the actual Go/PostgreSQL SearchMode path.

Embeddings are fetched from the OpenAI-compatible endpoint configured in .env
and cached by model identity so repeated experiments do not repeatedly incur cost.
"""
from __future__ import annotations

import json
import math
import os
import re
import time
import urllib.error
import urllib.request
from collections import defaultdict
from dataclasses import dataclass
from hashlib import sha256
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
OUT = ROOT / "evals" / "results" / "rag_results.json"
CASES = ROOT / "evals" / "rag_cases.jsonl"
CACHE_DIR = ROOT / "evals" / "cache"


def load_dotenv(path: Path) -> None:
    """Load simple KEY=value entries without executing shell syntax."""
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
    """Minimal OpenAI-compatible embedding client with persistent cache."""

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
        cache_name = sha256(identity.encode()).hexdigest()[:16] + ".json"
        self.cache_path = CACHE_DIR / cache_name
        self.cache: dict[str, list[float]] = {}
        if self.cache_path.exists():
            raw = json.loads(self.cache_path.read_text(encoding="utf-8"))
            if isinstance(raw, dict):
                self.cache = raw
        self.api_calls = 0
        self.api_ms = 0.0

    @staticmethod
    def _key(text: str) -> str:
        return sha256(text.encode("utf-8")).hexdigest()

    def _request(self, texts: list[str]) -> list[list[float]]:
        payload = json.dumps(
            {
                "model": self.model,
                "input": texts,
                "dimensions": self.dim,
                "encoding_format": "float",
            },
            ensure_ascii=False,
        ).encode("utf-8")
        request = urllib.request.Request(
            self.base_url + "/embeddings",
            data=payload,
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
                    body = json.load(response)
                self.api_calls += 1
                self.api_ms += (time.perf_counter() - started) * 1000
                data = sorted(body.get("data", []), key=lambda item: item.get("index", 0))
                vectors = [item.get("embedding", []) for item in data]
                if len(vectors) != len(texts):
                    raise RuntimeError(f"向量接口返回 {len(vectors)} 条，预期 {len(texts)} 条")
                for vector in vectors:
                    if len(vector) != self.dim:
                        raise RuntimeError(f"向量接口返回 {len(vector)} 维，配置为 {self.dim} 维")
                return vectors
            except urllib.error.HTTPError as exc:
                last_error = RuntimeError(f"向量接口 HTTP {exc.code}: {exc.read().decode('utf-8', 'replace')[:300]}")
                if exc.code not in (429, 500, 502, 503, 504):
                    break
            except (urllib.error.URLError, TimeoutError) as exc:
                last_error = exc
            if attempt < 2:
                time.sleep(2**attempt)
        raise RuntimeError(f"向量接口调用失败: {last_error}") from last_error

    def embed_many(self, texts: list[str]) -> list[list[float]]:
        missing: dict[str, str] = {}
        for text in texts:
            key = self._key(text)
            if key not in self.cache:
                missing[key] = text
        items = list(missing.items())
        for start in range(0, len(items), self.batch_size):
            batch = items[start : start + self.batch_size]
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

# --- corpus: long enough that 150 vs 1200 actually splits differently --------

DOCS = [
    {
        "id": "fulfillment_sla",
        "title": "发货时效与 SLA",
        "text": """# 发货时效与 SLA 规则

## ship_by 截止

待发货订单必须在 ship_by 之前完成包裹创建和承运商交接。超过 ship_by 未交接视为履约超时，平台可能扣除物流绩效分并触发延迟发货罚金。US 站点标准订单的默认生产加拣货窗口为 8 小时，高峰期可申请延长到 12 小时，但必须在订单备注写入超时原因码 SLA-DELAY-12H。

## 渠道过滤

候选物流渠道必须返回 sla_eligible=true 才允许下单。sla_eligible=false 的渠道即使单价更低也不得选用。若全部渠道均不满足时效，应转入跨仓调拨评估，而不是强行选择超时渠道。偏远邮编（阿拉斯加、夏威夷、PO Box）需要走偏远附加费规则，不能套用本土 48 州的标准时效表。

## 证据要求

向运营提交超时方案时必须同时给出：订单号、当前履约仓、ship_by 时间、库存可用量和至少一条 sla_eligible=true 的渠道。缺少任一项时不得建议自动发货。
""",
    },
    {
        "id": "transfer_policy",
        "title": "跨仓调拨",
        "text": """# 跨仓调拨作业规范

## 触发条件

履约仓库存不足且海外仓或其它区域仓对该 SKU 的可用量大于需求数量时，允许创建跨仓调拨。调拨不是默认动作：若本仓 4 小时内有在途补货且仍能赶在 ship_by 前发出，应优先等待补货。

## 路径与数量

US 站点演示订单优先评估洛杉矶仓 WH-US-LAX 调往深圳仓 WH-CN-SZ 的反向并不成立；正确方向是把有货仓调向履约仓。调拨数量必须等于缺口，禁止超额调拨占用在途库存。同一 SKU 同一订单只允许一张有效调拨单。

## 写操作约束

create_inventory_transfer 必须携带稳定的 idempotency_key，格式建议 transfer:{order_id}:{sku}。该接口为敏感写操作，必须暂停等待人工审批后才真正扣减库存。审批驳回后不得重放同一键之外的第二条调拨。干跑 dry_run=true 只返回可行性，不生成调拨单。
""",
    },
    {
        "id": "refund_policy",
        "title": "取消与退款",
        "text": """# 取消与退款规则

## 可退窗口

待发货且存在买家取消请求时，运营可以批准退款。已经部分发货的订单不得整单退款，只能对未发货行做部分退。退款金额不得超过订单实付金额，也不得把运费优惠券重复补给买家。

## 金额计算

退款基数是实付金额，不是吊牌价。若订单使用了平台优惠，退款=实付-已发货行分摊。演示订单 TTS-20260801-1001 实付 129.99 USD，若整单未发货且买家取消，最大可退 129.99 USD。

## 审批与幂等

approve_refund 需要人工审批与幂等键 refund:{order_id}:{amount}。模型给出退款结论后不得立即打款。重复提交同一幂等键必须返回首次退款单，禁止生成第二笔。
""",
    },
    {
        "id": "settlement_policy",
        "title": "结算对账",
        "text": """# 跨境平台结算对账规则 v1

## 差异定义

对账差异定义为应结金额减去实结金额，即 expected_amount - paid_amount。仅对正差异创建申诉。零差异或负差异（平台多付）走财务手工复核，不走自动申诉工具。

## 申诉字段

申诉必须绑定账单号、差异金额、费用原因和计算证据。演示账单 STMT-2026-31 应结 118.47、实结 106.95，差异 11.52 USD，原因可写平台运费重复扣减。缺少账单号的申诉一律拒绝。

## 重复提交

重复提交由 idempotency_key 拦截，建议 reconciliation:{statement_id}。create_reconciliation_case 必须等待财务人员审批。状态不是 difference_detected 时禁止建单。
""",
    },
    {
        "id": "inventory_hold",
        "title": "库存预占与可用量",
        "text": """# 库存预占规则

## 可用量口径

get_inventory 返回的 Available 已扣除预占和冻结，不得再把在途采购算进可发。Available=0 时本仓不能创建包裹。演示 SKU-BLACK-M-01 在 WH-CN-SZ 可用 0，在 WH-US-LAX 可用 18。

## 预占周期

创建调拨单后立即预占来源仓可用量，预占保留 24 小时；超时未审批则释放。退款审批通过后必须释放对应 SKU 的预占，避免库存黑洞。

## 禁止事项

不得编造库存数字。不得把安全库存 SafetyStock 当成可发量。多仓查询结果必须按仓库拆开汇报，禁止把 18 件加总后告诉模型「总库存 18 所以深圳也能发」。
""",
    },
    {
        "id": "remote_surcharge",
        "title": "偏远地址与附加费",
        "text": """# 偏远地址与附加费

## 识别

邮编属于阿拉斯加、夏威夷、关岛或 PO Box 时，视为偏远地址。偏远地址不使用本土 48 州的 7 日达时效，默认走 12-18 日渠道，并加收偏远附加费。

## 费用

偏远附加费按重量计：0-0.5kg 收取 6.5 USD，0.5-2kg 收取 11.0 USD，超过 2kg 每公斤再加 2.8 USD。附加费不计入退款基数，买家取消未发货订单时附加费未发生则不必退。

## 渠道

4PX US Priority 在偏远地址上 sla_eligible 通常为 false。应改选 4PX Remote Saver。若运营强行用 Priority 渠道导致超时，责任记在人工覆盖而非模型建议。
""",
    },
    {
        "id": "audit_trail",
        "title": "审计留痕",
        "text": """# 审计留痕要求

## 必须记录

库存调拨、退款和结算申诉必须记录订单或账单标识、操作者、审批记录、工具参数和执行结果。缺少审批 ID 的写操作视为不合规。

## 会话一致性

同一会话内使用的提示词、工具版本和知识库版本必须可复现。禁止在排查故障时用新版本配置重放历史会话。审计日志至少保留 180 天。

## 抽检

每周抽检 20 条敏感写操作：核对幂等键是否稳定、是否发生重复执行、审批记录是否与 approval_id 一致。抽检失败需在 24 小时内复盘。
""",
    },
    {
        "id": "weight_shipping",
        "title": "重量段运费",
        "text": """# 重量段运费表（US 本土）

## 标准件

0-0.5kg：8.90 USD，时效 7 日，渠道 4PX US Priority，sla_eligible=true。
0.5-1.0kg：12.80 USD，时效 7 日，渠道 4PX US Priority，sla_eligible=true。
1.0-2.0kg：16.40 USD，时效 8 日，仅当 ship_by 剩余大于 9 日才标 sla_eligible=true。

## 超重

超过 2.0kg 改走卡派，报价需实时查询，禁止使用上表一口价。卡派默认 sla_eligible=false，除非承诺 5 日达产品代码 GROUND-5。

## 与退款关系

已产生的运费在发货后不可随商品退款全额退回，未发货取消则运费未产生。重量以出库称重为准，不以商品页申报重量为准。
""",
    },
]

QUERIES = [json.loads(line) for line in CASES.read_text(encoding="utf-8").splitlines() if line.strip()]

# --- chunking (faithful to chunk.go, including byte vs rune mix) ------------

def bytelen(s: str) -> int:
    return len(s.encode("utf-8"))


def tail_runes(s: str, n: int) -> str:
    if n <= 0:
        return ""
    r = list(s)
    return s if len(r) <= n else "".join(r[-n:])


def cut_runes(s: str, n: int) -> str:
    r = list(s)
    return s if len(r) <= n else "".join(r[:n])


def split_paragraphs(text: str) -> list[str]:
    out = [p.strip() for p in text.split("\n\n") if p.strip()]
    return out or [text]


def nrunes(s: str) -> int:
    return len(s)


def chunk_fixed(text: str, size: int, overlap: int) -> list[str]:
    """Character-budget chunker matching production's UTF-8 rune semantics."""
    if size <= 0:
        size = 500
    if overlap < 0 or overlap >= size:
        overlap = size // 5
    text = text.strip()
    if not text:
        return []
    chunks: list[str] = []
    cur = ""
    for p in split_paragraphs(text):
        if cur and nrunes(cur) + nrunes(p) + 1 > size:
            chunks.append(cur)
            cur = tail_runes(cur, overlap)
        cur = f"{cur}\n{p}" if cur else p
        while nrunes(cur) > size:
            cut = cut_runes(cur, size)
            if not cut:
                break
            chunks.append(cut)
            rest = cur[len(cut):]
            nxt = tail_runes(cut, overlap) + rest
            if nxt == cur:
                break
            cur = nxt
    if cur:
        chunks.append(cur)
    return chunks


HEADING_RE = re.compile(r"(?m)^(#{1,3})\s+.+$")


def chunk_heading(text: str, size: int, overlap: int) -> list[str]:
    """Split on markdown headings, then fall back to fixed packing."""
    lines = text.splitlines(keepends=True)
    sections: list[str] = []
    buf: list[str] = []
    for line in lines:
        if HEADING_RE.match(line) and buf:
            sections.append("".join(buf).strip())
            buf = [line]
        else:
            buf.append(line)
    if buf:
        sections.append("".join(buf).strip())
    out: list[str] = []
    for sec in sections:
        if not sec:
            continue
        if nrunes(sec) <= size:
            out.append(sec)
        else:
            out.extend(chunk_fixed(sec, size, overlap))
    return out or chunk_fixed(text, size, overlap)


# --- tokenize / bm25 / vector / rrf (retriever/*.go) -----------------------

def tokenize_char(s: str) -> list[str]:
    """Experimental ASCII-word + CJK-unigram tokenizer (not production GSE)."""
    s = s.lower()
    tokens: list[str] = []
    cur: list[str] = []

    def flush() -> None:
        if cur:
            tokens.append("".join(cur))
            cur.clear()

    for r in s:
        if ("a" <= r <= "z") or ("0" <= r <= "9"):
            cur.append(r)
        elif ord(r) > 127:
            flush()
            tokens.append(r)
        else:
            flush()
    flush()
    return tokens


def tokenize_bigram(s: str) -> list[str]:
    """ASCII words + CJK bigrams (common cheap upgrade)."""
    s = s.lower()
    tokens: list[str] = []
    cur: list[str] = []
    cjk: list[str] = []

    def flush_ascii() -> None:
        if cur:
            tokens.append("".join(cur))
            cur.clear()

    def flush_cjk() -> None:
        if len(cjk) == 1:
            tokens.append(cjk[0])
        else:
            tokens.extend(cjk[i] + cjk[i + 1] for i in range(len(cjk) - 1))
        cjk.clear()

    for r in s:
        if ("a" <= r <= "z") or ("0" <= r <= "9"):
            flush_cjk()
            cur.append(r)
        elif ord(r) > 127:
            flush_ascii()
            cjk.append(r)
        else:
            flush_ascii()
            flush_cjk()
    flush_ascii()
    flush_cjk()
    return tokens


def tokenize_whitespace(s: str) -> list[str]:
    """PG simple-ish: split on whitespace/punct, keep CJK runs intact."""
    return [t.lower() for t in re.findall(r"[A-Za-z0-9_.:{}-]+|[\u4e00-\u9fff]+", s) if t]


TOKENIZERS = {
    "char": tokenize_char,
    "bigram": tokenize_bigram,
    "whitespace": tokenize_whitespace,
}


def cosine(a: list[float], b: list[float]) -> float:
    dot = sum(x * y for x, y in zip(a, b))
    norm_a = math.sqrt(sum(x * x for x in a))
    norm_b = math.sqrt(sum(x * x for x in b))
    if norm_a == 0 or norm_b == 0:
        return 0.0
    return dot / (norm_a * norm_b)


@dataclass
class Chunk:
    id: str
    doc_id: str
    text: str
    embedding: list[float]
    terms: dict[str, int]
    length: int


def term_freq(tokens: list[str]) -> dict[str, int]:
    tf: dict[str, int] = defaultdict(int)
    for t in tokens:
        tf[t] += 1
    return dict(tf)


def build_index(docs, chunker, size, overlap, tokenize) -> list[Chunk]:
    pending: list[tuple[str, str, str, dict[str, int]]] = []
    for doc in docs:
        parts = chunker(doc["text"], size, overlap)
        for i, text in enumerate(parts):
            tf = term_freq(tokenize(text))
            pending.append((f"{doc['id']}#{i}", doc["id"], text, tf))
    vectors = embed_many([text for _, _, text, _ in pending])
    chunks: list[Chunk] = []
    for (chunk_id, doc_id, text, tf), vector in zip(pending, vectors):
        chunks.append(
            Chunk(
                id=chunk_id,
                doc_id=doc_id,
                text=text,
                embedding=vector,
                terms=tf,
                length=sum(tf.values()),
            )
        )
    return chunks


def bm25_search(chunks: list[Chunk], query: str, tokenize, n: int) -> list[tuple[Chunk, float]]:
    k1, b = 1.5, 0.75
    N = len(chunks)
    if N == 0:
        return []
    avgdl = sum(c.length for c in chunks) / N
    df: dict[str, int] = defaultdict(int)
    for c in chunks:
        for t in c.terms:
            df[t] += 1
    q_terms = term_freq(tokenize(query))
    scored = []
    for c in chunks:
        score = 0.0
        for term in q_terms:
            tf = float(c.terms.get(term, 0))
            if tf == 0:
                continue
            idf = math.log(1 + (N - df[term] + 0.5) / (df[term] + 0.5))
            denom = tf + k1 * (1 - b + b * c.length / avgdl)
            score += idf * (tf * (k1 + 1)) / denom
        if score > 0:
            scored.append((c, score))
    scored.sort(key=lambda x: x[1], reverse=True)
    return scored[:n]


def vector_search(chunks: list[Chunk], qv: list[float], n: int) -> list[tuple[Chunk, float]]:
    scored = [(c, cosine(qv, c.embedding)) for c in chunks]
    scored.sort(key=lambda x: x[1], reverse=True)
    return scored[:n]


def rrf(lists: list[list[tuple[Chunk, float]]], k: int, n: int) -> list[tuple[Chunk, float]]:
    acc: dict[str, float] = defaultdict(float)
    by_id: dict[str, Chunk] = {}
    for ranked in lists:
        for rank, (c, _) in enumerate(ranked, start=1):
            acc[c.id] += 1.0 / (k + rank)
            by_id[c.id] = c
    merged = [(by_id[i], s) for i, s in acc.items()]
    merged.sort(key=lambda x: x[1], reverse=True)
    return merged[:n]


def search(
    chunks,
    query,
    tokenize,
    mode: str,
    k: int = 5,
    cand: int = 20,
    rrf_k: int = 60,
):
    if mode == "bm25":
        return bm25_search(chunks, query, tokenize, k)
    qv = embed_many([query])[0]
    if mode == "vector":
        return vector_search(chunks, qv, k)
    return rrf(
        [vector_search(chunks, qv, cand), bm25_search(chunks, query, tokenize, cand)],
        rrf_k,
        k,
    )


def dcg(rels: list[float]) -> float:
    return sum(rel / math.log2(i + 2) for i, rel in enumerate(rels))


def metrics_for(hits: list[tuple[Chunk, float]], gold_docs: list[str], gold_span: str, k: int = 5):
    gold = set(gold_docs)
    rels = [1.0 if c.doc_id in gold else 0.0 for c, _ in hits[:k]]
    doc_hit = 1.0 if any(rels) else 0.0
    retrieved_gold = {c.doc_id for c, _ in hits[:k] if c.doc_id in gold}
    recall = len(retrieved_gold) / len(gold) if gold else 0.0
    prec = sum(rels) / k
    mrr = 0.0
    for i, r in enumerate(rels):
        if r:
            mrr = 1.0 / (i + 1)
            break
    ideal = sorted(rels, reverse=True)
    ndcg = (dcg(rels) / dcg(ideal)) if any(ideal) else 0.0
    span_hit = 1.0 if any(gold_span in c.text for c, _ in hits[:k]) else 0.0
    span_mrr = 0.0
    for i, (c, _) in enumerate(hits[:k]):
        if gold_span in c.text:
            span_mrr = 1.0 / (i + 1)
            break
    ctx_chars = sum(len(c.text) for c, _ in hits[:k])
    return {
        "doc_hit@5": doc_hit,
        "doc_recall@5": recall,
        "precision@5": prec,
        "mrr": mrr,
        "ndcg@5": ndcg,
        "span_hit@5": span_hit,
        "span_mrr": span_mrr,
        "context_chars": ctx_chars,
    }


def mean(rows: list[dict], key: str) -> float:
    return sum(r[key] for r in rows) / len(rows) if rows else 0.0


def eval_config(name, chunker, size, overlap, tok_name, mode, rrf_k=60) -> dict:
    print(name, flush=True)
    tokenize = TOKENIZERS[tok_name]
    t0 = time.perf_counter()
    chunks = build_index(DOCS, chunker, size, overlap, tokenize)
    index_ms = (time.perf_counter() - t0) * 1000
    per_q = []
    search_ms = []
    for q in QUERIES:
        t1 = time.perf_counter()
        hits = search(chunks, q["query"], tokenize, mode, rrf_k=rrf_k)
        search_ms.append((time.perf_counter() - t1) * 1000)
        m = metrics_for(hits, q["gold_docs"], q["gold_span"])
        m["id"] = q["id"]
        per_q.append(m)
    avg_len = sum(len(c.text) for c in chunks) / len(chunks) if chunks else 0
    return {
        "name": name,
        "chunker": chunker.__name__,
        "size": size,
        "overlap": overlap,
        "tokenizer": tok_name,
        "mode": mode,
        "rrf_k": rrf_k if mode == "hybrid" else None,
        "n_chunks": len(chunks),
        "avg_chunk_chars": round(avg_len, 1),
        "index_ms": round(index_ms, 2),
        "search_ms_p50": round(sorted(search_ms)[len(search_ms) // 2], 3),
        "search_ms_mean": round(sum(search_ms) / len(search_ms), 3),
        "doc_hit@5": round(mean(per_q, "doc_hit@5"), 4),
        "doc_recall@5": round(mean(per_q, "doc_recall@5"), 4),
        "precision@5": round(mean(per_q, "precision@5"), 4),
        "mrr": round(mean(per_q, "mrr"), 4),
        "ndcg@5": round(mean(per_q, "ndcg@5"), 4),
        "span_hit@5": round(mean(per_q, "span_hit@5"), 4),
        "span_mrr": round(mean(per_q, "span_mrr"), 4),
        "avg_context_chars": round(mean(per_q, "context_chars"), 1),
        "per_query": per_q,
    }


def main() -> None:
    global EMBEDDER
    EMBEDDER = RemoteEmbedder()
    # 查询向量预热，避免第一个实验独自承担远程 API 延迟；指标主要比较检索质量。
    EMBEDDER.embed_many([q["query"] for q in QUERIES])
    runs: list[dict] = []

    # A. chunk size × overlap; other experimental variables remain fixed.
    for size in (150, 300, 500, 800, 1200):
        for overlap in (0, 50, 100, 200):
            if overlap >= size:
                continue
            runs.append(
                eval_config(
                    f"fixed/size={size}/overlap={overlap}",
                    chunk_fixed, size, overlap, "char", "hybrid",
                )
            )

    # B. heading vs fixed at current defaults
    runs.append(eval_config("heading/size=500/overlap=100", chunk_heading, 500, 100, "char", "hybrid"))

    # C. tokenizer × retriever at current defaults
    for tok in TOKENIZERS:
        for mode in ("bm25", "vector", "hybrid"):
            runs.append(
                eval_config(
                    f"fixed/tok={tok}/mode={mode}",
                    chunk_fixed, 500, 100, tok, mode,
                )
            )

    # D. RRF 融合参数。其余配置保持一致，避免把切片和分词差异混进结果。
    for rrf_k in (10, 30, 60, 100):
        runs.append(
            eval_config(
                f"fixed/rrf_k={rrf_k}",
                chunk_fixed, 500, 100, "char", "hybrid", rrf_k,
            )
        )

    summary = [{k: v for k, v in r.items() if k != "per_query"} for r in runs]
    payload = {
        "protocol": {
            "corpus_docs": len(DOCS),
            "queries": len(QUERIES),
            "embedder": EMBEDDER.description(),
            "bm25": "Okapi k1=1.5 b=0.75",
            "rrf_k": "10/30/60/100 sweep; baseline=60",
            "topk": 5,
            "gold": "document id + unique span containment",
            "note": "vector path uses the configured production embedding model; timing includes local cache and is not a provider latency benchmark",
            "scope": "Python algorithm ablation only; lexical and fusion metrics do not represent the Go/PostgreSQL production path",
            "embedding_api_calls": EMBEDDER.api_calls,
            "embedding_api_ms": round(EMBEDDER.api_ms, 2),
        },
        "baseline": "fixed/size=500/overlap=100 + char + hybrid",
        "summary": summary,
        "runs": runs,
    }
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    EMBEDDER.flush()
    print(f"wrote {OUT} ({len(runs)} configs)")
    # print compact table
    keys = ["name", "n_chunks", "span_hit@5", "span_mrr", "doc_hit@5", "ndcg@5", "avg_context_chars", "search_ms_mean"]
    print("\t".join(keys))
    for s in summary:
        print("\t".join(str(s[k]) for k in keys))


if __name__ == "__main__":
    main()
