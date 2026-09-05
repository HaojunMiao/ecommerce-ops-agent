# E-commerce Operations Agent Platform

基于 Go、Eino、PostgreSQL 和 Redis 构建的通用 Agent 构建与运行平台。通过配置不同的 System Prompt、Skill、知识库与 Tool，可接入订单、库存、物流、财务等业务系统；仓库内提供一套跨境电商运营场景用于完整演示。

## 核心能力

- **版本化 Agent 运行时**：AgentVersion 固定 Prompt、模型配置、Skill、Tool 与知识库版本；按环境发布，新旧会话互不影响并支持回滚。
- **RAG 混合检索**：知识库增量摄取，pgvector 向量召回与 PostgreSQL BM25 双路检索，使用 RRF 融合；Reranker 可配置启停。
- **人在环审批与恢复**：敏感工具调用通过 Eino Checkpoint 中断，人工审批后由 Asynq Worker 异步续跑，并以双租约、执行令牌和幂等键保证正确性。
- **工具安全边界**：AgentVersion/Skill 双层授权、JSON Schema 参数校验、网络访问策略、SSRF 防护与 ReAct 步数限制。
- **评测与可观测**：提供固定 RAG/Agent 评测集、OpenTelemetry Trace、Langfuse、Prometheus 指标和审计记录。

## 目录概览

```text
cmd/                  Server、Worker、迁移等程序入口
internal/platform/    Agent、Prompt、Tool、Skill、KB 等控制面
internal/runtime/     Eino Agent、RAG、工具执行、审批恢复与 Guard
internal/api/         REST 与 SSE 接口
migrations/           PostgreSQL 数据库迁移
web/admin/            React 管理与会话前端
projects/crossborder/ 跨境电商业务模拟器及演示资源
evals/                RAG 与 Agent 评测代码、语料和结果
deploy/               Docker Compose 与镜像配置
```

## 快速启动

需要 Docker、Docker Compose 和 Make。复制配置文件并填写聊天模型及 Embedding 凭据：

```bash
cp .env.example .env
make up
make crossborder-install
```

启动后访问：

- Admin Console：<http://localhost:8080>
- Langfuse：<http://localhost:3000>
- 跨境电商模拟器：<http://localhost:8091>

不需要 Langfuse 时可使用 `make up-lite`。停止环境使用 `make down` 或 `make down-lite`，默认保留 PostgreSQL 等数据卷。

## 模型配置

开发环境可通过自动初始化创建默认 Doubao 配置；需要显式创建或追加新版本时执行：

```bash
make bootstrap-model-config \
  MODEL_CONFIG_WORKSPACE='跨境电商运营平台' \
  MODEL_CONFIG_NAME='Doubao'
```

该命令幂等：配置未变化时复用原版本，模型地址、名称或运行参数变化时创建不可变的新版本。

## 验证

```bash
make test                       # 单元测试与跨境项目测试
make test-integration           # PostgreSQL/Redis 集成测试
make crossborder-model-smoke    # 真实模型只读诊断
make crossborder-e2e            # 敏感操作审批与恢复闭环
make rag-eval-offline           # RAG 离线消融评测
```

## 文档

- [系统架构](docs/architecture.md)
- [运行手册](docs/runbook.md)
- [RAG 评测报告](docs/rag-evaluation-report.md)
- [跨境电商演示场景](projects/crossborder/README.md)

本地 `.env` 含密钥且已被 Git 忽略，请勿提交；真实模型冒烟与 E2E 会调用配置的外部模型服务。
