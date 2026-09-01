# E-commerce Operations Agent Platform

一个基于 Go 与 Eino 构建的跨境电商运营 Agent，将大模型能力与订单、库存、调拨等业务系统连接起来。

项目通过可版本化的 Agent 配置、工具调用和可控执行流程，帮助运营人员完成信息查询、异常分析与业务操作，适用于跨境电商运营助手、库存调度、订单处理和企业内部智能工作流等场景。

## 模型配置初始化

开发环境可由 admin autoseed 初始化默认版本；生产或关闭 autoseed 时，在 Workspace 建立后显式执行：

```bash
make bootstrap-model-config \
  MODEL_CONFIG_WORKSPACE='跨境电商运营平台' \
  MODEL_CONFIG_NAME='Doubao'
```

容器编排平台也可直接运行 migrate 镜像内的 `/ecommerce-ops-bootstrap-model-config`，并用 `-workspace-id` 指定 Workspace。命令幂等：配置未变时返回同一版本，Base URL、模型名或运行参数改变时追加新版本。
