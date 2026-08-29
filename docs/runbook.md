# 本地运行

## 配置

复制 `.env.example` 为 `.env`，至少填写：

- `KBOT_LLM_BASE_URL`
- `KBOT_LLM_API_KEY`
- `KBOT_LLM_MODEL`
- `KBOT_EMBEDDER_BASE_URL`
- `KBOT_EMBEDDER_API_KEY`（`KBOT_EMBEDDER=openai` 时必填）
- `KBOT_EMBEDDER_MODEL`
- `KBOT_JWT_SECRET_KEY`
- `KBOT_CREDENTIAL_ENCRYPTION_KEY`

聊天模型与 Embedding 独立配置，可以分别使用不同供应商。当前真实文本向量配置为：

```dotenv
KBOT_EMBEDDER=openai
KBOT_EMBEDDER_BASE_URL=https://api.siliconflow.cn/v1
KBOT_EMBEDDER_API_KEY=仅写入本地.env或SecretManager
KBOT_EMBEDDER_MODEL=Qwen/Qwen3-Embedding-4B
KBOT_EMBEDDER_DIM=2048
```

项目显式请求 2048 维向量，数据库使用 `halfvec(2048) + HNSW cosine`；server 查询与 worker 入库必须使用完全相同的模型和维度。模型、接口地址或维度变化会进入文档指纹并自动触发 Connector 文档重新向量化。旧上传文档没有可重读的 Connector 源时，需要重新上传。

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
make rag-eval
make rag-eval-production
go run ./evals/run_agent_eval.go
```

`rag-eval` 是 Python 算法消融实验，用于快速比较切片、分词和 RRF 参数；它使用真实向量模型，但关键词评分不是生产 PostgreSQL 链路，因此不能把混合检索指标直接当作线上效果。

`rag-eval-production` 要求平台与电商演示数据已经启动并完成入库。它通过真实 HTTP API 调用 Go 服务，覆盖生产使用的 GSE 分词、PostgreSQL `ts_rank_cd`、真实向量模型和 RRF 融合。两类评测均输出 Recall@K、MRR、NDCG 等指标到 `evals/results/`；Agent 评测覆盖工具选择、参数、审批合规、禁止工具、任务完成和幂等性。
