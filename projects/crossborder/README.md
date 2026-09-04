# 跨境电商运营与供应链协同 Agent

该目录提供跨境电商业务模拟服务，以及唯一一套演示数据初始化资源：工具契约、技能与知识库。它通过 HTTP 工具接入平台，不导入主服务的 Go 包。

## 本地运行

```bash
make test
make run
curl http://localhost:8091/healthz
```

初始数据包含以下互不混淆的业务剧本：

| 剧本 | 事实数据 | 推荐演示 Prompt | 预期行为 |
| --- | --- | --- | --- |
| 紧急履约 | 订单 `TTS-20260801-1001` 的履约仓是洛杉矶仓，8 小时后到 `ship_by`；该仓 `SKU-BLACK-M-01` 可用库存为 0，旧金山仓库存为 18，已知 `SFO→LAX` 调拨线路预计 6 小时 | `/order_exception_triage 仅分析订单 TTS-20260801-1001 的履约风险并给出方案，不执行写操作` | 查询订单、库存、调拨线路和发货物流；说明调拨可在截止前到仓，但不执行 |
| 调拨审批 | 与紧急履约使用同一组事实 | `/order_exception_triage 核实后，从 WH-US-SFO 向 WH-US-LAX 调拨 1 件 SKU-BLACK-M-01` | 调用敏感工具、产生审批；批准后创建状态为 `in_transit` 且带预计到仓时间的调拨单 |
| 取消退款 | 订单 `TTS-20260801-1002` 待发货、买家已发起取消请求，实付 59.90 USD | `/order_exception_triage 买家要求取消订单 TTS-20260801-1002，核实后为全额退款发起审批` | 查询订单后调用退款工具并等待审批；批准后订单变为 `cancelled` |
| 切换履约仓 | 订单 `TTS-20260801-1003` 的 LAX 履约仓缺货，6 小时后到 `ship_by`；BOS 仓库存为 12，但 `BOS→LAX` 调拨需要 12 小时；BOS 有 SLA 合格的 FedEx 2Day 渠道 | `/order_exception_triage 处理订单 TTS-20260801-1003；如果调拨赶不上 ship_by，请评估并切换到可直接履约的仓库` | 排除来不及的调拨，验证 BOS 库存和物流后调用敏感履约仓变更工具；审批通过后生成 `FW-0001` |
| 结算申诉 | 账单 `STMT-2026-31` 应结 118.47 USD、实结 106.95 USD，正差异 11.52 USD | `/settlement_reconciliation 核验账单 STMT-2026-31，并为符合规则的差异创建申诉` | 检索结算规则、查询账单、计算 11.52 USD；创建申诉前等待审批 |

`get_shipping_options` 表示当前或指定候选履约仓向消费者发货的物流选项；仓间调拨时效由 `get_inventory` 返回的 `transfer_lanes` 提供，二者不再混用。调拨创建后库存记为在途（`inbound`），不会被当作已经到仓的可用库存。

敏感工具的 `dry_run` 仅供安装脚本试调使用，不出现在模型可见的 JSON Schema 中，避免模型把“正式提交审批”错误规划成试运行。

AgentVersion 的 `max_steps` 为 16：足以完成 Skill 加载、规则/订单/库存/物流四类只读取证、一次敏感工具中断和恢复后的最终回答，同时仍对 ReAct 循环设置明确上限。

工具注册清单位于 `config/tools.json`。敏感工具在 JSON Schema 中声明兼容字段 `x-kbot-approval`，由平台生成领域化审批卡片。

## 端到端演示环境

```bash
make crossborder-up
make crossborder-install-isolated
make crossborder-e2e-isolated
```

`crossborder-install-isolated` 幂等创建 Workspace、知识库、工具、技能和 System PromptVersion，并创建直接固定该 PromptVersion、ModelConfigVersion 与生成参数的单一电商运营 Agent。若 Workspace 中尚无 Doubao 模型配置，安装脚本会明确失败并提示先执行 `make crossborder-bootstrap-model-config`。

`crossborder-e2e-isolated` 验证订单诊断、敏感履约仓变更、固定审批卡片、断点恢复和审计链；它会向 `.env` 配置的外部模型发送演示订单指令，请仅在已授权的模型端点上执行。

Compose 使用项目名 `ecommerce-ops-crossborder`，数据库、Redis、Worker 和业务模拟器均使用独立实例。若开发时复用外部 PostgreSQL/Redis，请为该环境分配独立 Redis DB，避免审批恢复任务被其他 Worker 消费。

默认入口为 `http://localhost:8181`，端口配置位于 `compose.env`。
