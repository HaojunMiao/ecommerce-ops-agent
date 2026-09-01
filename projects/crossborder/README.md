# 跨境电商运营与供应链协同 Agent

该目录提供跨境电商业务模拟服务，以及唯一一套演示数据初始化资源：工具契约、技能与知识库。它通过 HTTP 工具接入平台，不导入主服务的 Go 包。

## 本地运行

```bash
make test
make run
curl http://localhost:8091/healthz
```

初始数据包含：

- 待发货订单 `TTS-20260801-1001`；
- 深圳仓缺货、洛杉矶仓有货的 SKU `SKU-BLACK-M-01`；
- 存在结算差异的账单 `STMT-2026-31`。

工具注册清单位于 `config/tools.json`。敏感工具在 JSON Schema 中声明兼容字段 `x-kbot-approval`，由平台生成领域化审批卡片。

## 端到端演示环境

```bash
make crossborder-up
make crossborder-install-isolated
make crossborder-e2e-isolated
```

`crossborder-install-isolated` 幂等创建 Workspace、知识库、工具、技能和 System PromptVersion，并创建直接固定该 PromptVersion、ModelConfigVersion 与生成参数的单一电商运营 Agent。若 Workspace 中尚无默认模型配置，安装脚本会明确失败并提示先执行 `make crossborder-bootstrap-model-config`。

`crossborder-e2e-isolated` 验证订单诊断、敏感库存调拨、固定审批卡片、断点恢复和审计链；它会向 `.env` 配置的外部模型发送演示订单指令，请仅在已授权的模型端点上执行。

Compose 使用项目名 `ecommerce-ops-crossborder`，数据库、Redis、Worker 和业务模拟器均使用独立实例。若开发时复用外部 PostgreSQL/Redis，请为该环境分配独立 Redis DB，避免审批恢复任务被其他 Worker 消费。

默认入口为 `http://localhost:8181`，端口配置位于 `compose.env`。
