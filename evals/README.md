# RAG 评测目录

本目录只保留一套固定评测流程：

| 文件 | 作用 |
|---|---|
| `build_rag_evaluation.py` | 生成 190 篇语料、40 条 dev 查询、100 条 test 查询及 Gold 标注 |
| `run_rag_evaluation.py` | 运行切片、召回器和 Top-K 离线消融 |
| `run_rag_reranker_evaluation.py` | 在500/100与1200/200下比较等权RRF Top-5和Top-10→Top-5重排 |
| `run_rag_answer_smoke_evaluation.py` | 12 条分层样本的低成本抽取式正确性与引用忠实度检查 |
| `run_rag_system_evaluation.py` | 通过 HTTP 运行真实 Go/PostgreSQL 链路评测 |
| `_rag_dataset_base.py` | 数据生成公共模板，不直接执行 |
| `_rag_offline_core.py` | 分词、切片、Embedding、BM25、Vector、RRF 公共实现，不直接执行 |
| `rag-evaluation.compose.yml` | 真实链路评测的 Docker 挂载配置 |
| `results/` | 固定实验结果 |

完整实验设计、指标、结果和结论见 `docs/rag-evaluation-report.md`。入口命令统一为 `make rag-eval-*`；无需直接运行下划线开头的内部模块。
