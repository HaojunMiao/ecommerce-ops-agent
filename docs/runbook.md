# 本地运行

## 配置

复制 `.env.example` 为 `.env`，至少填写：

- `KBOT_LLM_BASE_URL`
- `KBOT_LLM_API_KEY`
- `KBOT_LLM_MODEL`
- `KBOT_EMBEDDER_BASE_URL`
- `KBOT_EMBEDDER_API_KEY`（`KBOT_EMBEDDER=openai` 时必填）
- `KBOT_EMBEDDER_MODEL`
- `KBOT_RERANKER_ENABLED`（开启模型重排时设为 `true`）
- `KBOT_RERANKER_MODEL`（当前为 `Qwen/Qwen3-Reranker-4B`）
- `KBOT_JWT_SECRET_KEY`
- `KBOT_CREDENTIAL_ENCRYPTION_KEY`

聊天模型与 Embedding 独立配置，可以分别使用不同供应商。当前真实文本向量配置为：

```dotenv
KBOT_EMBEDDER=openai
KBOT_EMBEDDER_BASE_URL=https://api.siliconflow.cn/v1
KBOT_EMBEDDER_API_KEY=仅写入本地.env或SecretManager
KBOT_EMBEDDER_MODEL=Qwen/Qwen3-Embedding-4B
KBOT_EMBEDDER_DIM=2048
KBOT_RERANKER_ENABLED=true
KBOT_RERANKER_BASE_URL=https://api.siliconflow.cn/v1
KBOT_RERANKER_MODEL=Qwen/Qwen3-Reranker-4B
KBOT_RERANKER_CANDIDATE_K=10
```

`KBOT_RERANKER_API_KEY` 可独立配置；留空时复用 `KBOT_EMBEDDER_API_KEY`。开启后仅改变查询时的排序：等权 RRF 先返回候选，再由 Reranker 输出调用方要求的 Top-K；无需重新向量化或重灌知识库。Reranker 接口临时失败时回退到原等权 RRF 顺序。

项目显式请求 2048 维向量，数据库使用 `halfvec(2048) + HNSW cosine`；server 查询与 worker 入库必须使用完全相同的模型和维度。模型、接口地址或维度变化会进入文档指纹并自动触发 Connector 文档重新向量化。旧上传文档没有可重读的 Connector 源时，需要重新上传。

关键词检索使用 `pg_search 0.25.0` 的 BM25 索引。数据库镜像仍以原先固定的 PostgreSQL 16 + pgvector 镜像为基础，只额外安装经过 SHA256 校验的 pg_search 包；`shared_preload_libraries=pg_search` 由 Compose 显式传入，因此已有数据卷重启后也能加载扩展。迁移 `000036` 会直接为现有 `kb_chunks.search_text` 建索引，不需要重新切片或向量化。旧 `tsvector/GIN` 暂时保留为回滚安全网，但运行时查询已不再使用。

不要把 API Key 写进命令参数或提交到仓库。可使用无回显配置命令：

```bash
make configure-embedding
```

## 启动

```bash
make up
make crossborder-install
```

平台地址为 `http://localhost:8080`，Langfuse 地址默认为 `http://localhost:3000`。只需要平台、PostgreSQL 和 Redis 时可使用 `make up-lite`。

模型超时、重试和价格可通过 `KBOT_LLM_TIMEOUT_MS`、`KBOT_LLM_MAX_RETRIES` 与三项 `KBOT_LLM_*_PRICE_PER_MILLION` 配置。它们会与模型地址、名称一起固化为模型配置版本；修改后重启服务会追加新版本，不会改写已有会话。

`KBOT_*` 环境变量前缀、数据库默认名称及 `x-kbot-approval` 是为已有本地数据和审批 Schema 保留的兼容标识；它们不代表项目仍依赖课程品牌。

## 常用检查

```bash
make ps
make logs
make test
make crossborder-model-smoke
make crossborder-e2e
```

电商演示数据只有一份，位于 `projects/crossborder/`，由 `projects/crossborder/scripts/install.sh` 幂等安装。

`crossborder-model-smoke` 会向 `.env` 中的真实模型发送只读诊断任务，并拒绝任何敏感写工具；`crossborder-e2e` 则会批准并执行本地模拟库存调拨，只应在明确需要验证写链路时运行。

## 离线评测

```bash
make rag-eval-build
make rag-eval-offline
make rag-eval-reranker
make rag-eval-answer-smoke
make rag-eval-up
make rag-eval-system
make rag-eval-down
go run ./evals/run_agent_eval.go
```

`rag-eval-offline` 是 Python 算法消融实验，用于比较切片、召回器、Top-K 与 Reranker；它使用真实向量模型和离线 Okapi BM25。生产链路同样采用 BM25，但由 PostgreSQL `pg_search` 执行，因此离线结果用于算法选型，不能替代真实 HTTP/数据库链路验证。

`rag-eval-system` 要求先用 `rag-eval-up` 启动隔离环境并完成入库。它通过真实 HTTP API 调用 Go 服务，覆盖生产使用的 GSE 分词、PostgreSQL `pg_search` BM25、真实向量模型和 RRF 融合。检索评测输出 Hit@K、Precision@K、MRR、NDCG 等指标到 `evals/results/`；Agent 评测覆盖工具选择、参数、审批合规、禁止工具和任务完成。

固定数据集包含 190 篇合成文档和 140 条独立查询：40 条 dev 查询用于选参，100 条 test 查询用于最终报告；覆盖 BM25、向量、混合检索、切片边界、Top-K 和 Reranker 消融。

`rag-eval-up` 使用独立端口启动真实链路；结束后用 `rag-eval-down` 停止容器并保留数据卷。完整方法、指标解释与最终结论见 `docs/rag-evaluation-report.md`。
