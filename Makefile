# E-commerce Operations Agent Platform Makefile
# 常用：make up / make crossborder-install / make test

SHELL := /bin/bash
LITE_COMPOSE := docker compose -p ecommerce-ops-lite -f deploy/docker-compose.yml --env-file .env
LANGFUSE_COMPOSE := docker compose -p ecommerce-ops-agent -f deploy/docker-compose.yml -f deploy/docker-compose.langfuse.yml --env-file .env
CROSSBORDER_COMPOSE := docker compose -p ecommerce-ops-crossborder -f deploy/docker-compose.yml -f projects/crossborder/platform-compose.yml --env-file .env --env-file projects/crossborder/compose.env
PLATFORM_URL ?= http://localhost:8080
CROSSBORDER_ISOLATED_URL ?= http://localhost:8181
CROSSBORDER_ADMIN_EMAIL ?= admin@ecommerce-ops.local
CROSSBORDER_KBOT_PASSWORD ?= admin12345

.PHONY: help bootstrap configure-embedding up down logs ps up-lite down-lite logs-lite ps-lite seed seed-lite migrate migrate-lite \
		bootstrap-model-config bootstrap-model-config-lite crossborder-bootstrap-model-config sqlc-generate openapi test-integration \
		rag-eval rag-eval-production \
		crossborder-build crossborder-test crossborder-up crossborder-install crossborder-install-isolated \
		crossborder-model-smoke crossborder-model-smoke-isolated crossborder-e2e crossborder-e2e-isolated crossborder-logs crossborder-down \
        langfuse-preflight langfuse-up langfuse-demo langfuse-ps langfuse-logs langfuse-down langfuse-reset

help: ## 列出所有 target
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ===== 项目本身 =====

configure-embedding: ## 安全写入硅基流动 Qwen Embedding 配置（API Key 无回显）
	@bash scripts/configure-embedding.sh

bootstrap: ## 检查 Go 版本 / 拉依赖 / 生成 go.sum / 编译验证
	@bash scripts/bootstrap.sh

up: ## 启动平台、业务模拟器与 Langfuse（模型使用 .env 中的真实配置）
	@bash scripts/langfuse-up.sh

down: ## 停止完整环境并保留数据
	$(LANGFUSE_COMPOSE) down

logs: ## 跟随完整环境日志
	$(LANGFUSE_COMPOSE) logs -f --tail=100 app worker crossborder-sim langfuse-web langfuse-worker

ps: ## 查看完整环境容器状态
	$(LANGFUSE_COMPOSE) ps

up-lite: ## 启动轻量开发环境（不含 Langfuse）
	@if [ -n "$$($(LANGFUSE_COMPOSE) ps -q 2>/dev/null)" ]; then \
		echo "完整环境正在运行，请先执行 make down。"; \
		exit 1; \
	fi
	$(LITE_COMPOSE) up --build -d
	@echo "✅ 轻量环境已启动。Admin Console: http://localhost:8080"

down-lite: ## 停止轻量开发环境并保留数据
	$(LITE_COMPOSE) down

logs-lite: ## 跟随轻量开发环境日志
	$(LITE_COMPOSE) logs -f --tail=100 app worker

ps-lite: ## 查看轻量开发环境容器状态
	$(LITE_COMPOSE) ps

test: ## go test -race -count=1 ./...
	go test -race -count=1 ./...
	$(MAKE) crossborder-test

build: ## go build ./...
	go build ./...
	$(MAKE) crossborder-build

crossborder-build: ## 编译独立跨境电商业务模拟服务
	$(MAKE) -C projects/crossborder build

crossborder-test: ## 测试独立跨境电商业务项目
	$(MAKE) -C projects/crossborder test

crossborder-up: ## 启动独立的跨境电商平台环境（端口 8181）
	$(CROSSBORDER_COMPOSE) up --build -d
	@echo "✅ 独立环境已启动；下一步执行 make crossborder-install-isolated"

crossborder-install: ## 向 make up 启动的完整环境安装电商场景（默认端口 8080）
	@KBOT_URL=$(PLATFORM_URL) KBOT_EMAIL=$(CROSSBORDER_ADMIN_EMAIL) KBOT_PASSWORD=$(CROSSBORDER_KBOT_PASSWORD) \
		MODEL_CONFIG_BOOTSTRAP_HINT="make bootstrap-model-config" bash projects/crossborder/scripts/install.sh

crossborder-install-isolated: ## 向 make crossborder-up 启动的独立环境安装电商场景（默认端口 8181）
	@KBOT_URL=$(CROSSBORDER_ISOLATED_URL) KBOT_EMAIL=$(CROSSBORDER_ADMIN_EMAIL) KBOT_PASSWORD=$(CROSSBORDER_KBOT_PASSWORD) \
		MODEL_CONFIG_BOOTSTRAP_HINT="make crossborder-bootstrap-model-config" bash projects/crossborder/scripts/install.sh

crossborder-e2e: ## 在完整环境验证敏感调拨审批、恢复和审计闭环
	@KBOT_URL=$(PLATFORM_URL) KBOT_EMAIL=$(CROSSBORDER_ADMIN_EMAIL) KBOT_PASSWORD=$(CROSSBORDER_KBOT_PASSWORD) bash projects/crossborder/scripts/e2e.sh

crossborder-e2e-isolated: ## 在独立环境验证敏感调拨审批、恢复和审计闭环
	@KBOT_URL=$(CROSSBORDER_ISOLATED_URL) KBOT_EMAIL=$(CROSSBORDER_ADMIN_EMAIL) KBOT_PASSWORD=$(CROSSBORDER_KBOT_PASSWORD) bash projects/crossborder/scripts/e2e.sh

crossborder-model-smoke: ## 在完整环境用真实模型执行只读订单诊断，不触发写操作
	@KBOT_URL=$(PLATFORM_URL) KBOT_EMAIL=$(CROSSBORDER_ADMIN_EMAIL) KBOT_PASSWORD=$(CROSSBORDER_KBOT_PASSWORD) bash projects/crossborder/scripts/model-smoke.sh

crossborder-model-smoke-isolated: ## 在独立环境用真实模型执行只读订单诊断，不触发写操作
	@KBOT_URL=$(CROSSBORDER_ISOLATED_URL) KBOT_EMAIL=$(CROSSBORDER_ADMIN_EMAIL) KBOT_PASSWORD=$(CROSSBORDER_KBOT_PASSWORD) bash projects/crossborder/scripts/model-smoke.sh

crossborder-logs: ## 跟随跨境电商环境日志
	$(CROSSBORDER_COMPOSE) logs -f --tail=100 app worker crossborder-sim

crossborder-down: ## 停止跨境电商环境并保留数据
	$(CROSSBORDER_COMPOSE) down

seed: ## 在完整环境初始化业务 Workspace
	@SEED_EMAIL=$(CROSSBORDER_ADMIN_EMAIL) bash scripts/seed.sh --auto-open

seed-lite: ## 在轻量开发环境初始化业务 Workspace
	@bash scripts/seed.sh --auto-open

migrate: ## 在完整环境执行数据库迁移
	$(LANGFUSE_COMPOSE) run --rm --build migrate -up

migrate-lite: ## 在轻量开发环境执行数据库迁移
	$(LITE_COMPOSE) run --rm --build migrate -up

MODEL_CONFIG_WORKSPACE ?= 跨境电商运营平台
MODEL_CONFIG_NAME ?= 默认模型配置
MODEL_CONFIG_ARGS = -workspace-name "$(MODEL_CONFIG_WORKSPACE)" -name "$(MODEL_CONFIG_NAME)"

bootstrap-model-config: ## 在 make up 的完整环境初始化/更新模型配置版本
	$(LANGFUSE_COMPOSE) run --rm --build --entrypoint /ecommerce-ops-bootstrap-model-config migrate \
		$(MODEL_CONFIG_ARGS)

bootstrap-model-config-lite: ## 在 make up-lite 的轻量环境初始化/更新模型配置版本
	$(LITE_COMPOSE) run --rm --build --entrypoint /ecommerce-ops-bootstrap-model-config migrate \
		$(MODEL_CONFIG_ARGS)

crossborder-bootstrap-model-config: ## 在 make crossborder-up 的独立环境初始化/更新模型配置版本
	$(CROSSBORDER_COMPOSE) run --rm --build --entrypoint /ecommerce-ops-bootstrap-model-config migrate \
		$(MODEL_CONFIG_ARGS)

sqlc-generate: ## 生成 sqlc Go 代码(改 SQL 后跑;生成产物已进 git,学员一般无需跑)
	@if command -v sqlc >/dev/null 2>&1; then \
		sqlc generate; \
	elif command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then \
		docker run --rm -v "$(CURDIR):/src" -w /src sqlc/sqlc:1.27.0 generate; \
	else \
		echo "需要 sqlc v1.27.0 二进制或可用的 Docker daemon"; \
		exit 1; \
	fi

openapi: ## 生成 Swagger 2.0 spec（swaggo/swag）
	@swag init --quiet -g main.go -d cmd/server,internal/api/v1 -o pkg/sdk/openapi \
		--packageName openapi --parseInternal --parseDependencyLevel 1 --parseDepth 2

test-integration: ## dockertest 真 PG/Redis 集成测试(含各 store contract test + e2e;需 Docker)
	@# 复用常驻 PG 可加速:KBOT_TEST_DATABASE_URL=postgres://kbot:kbot@localhost:55432/kbot?sslmode=disable make test-integration
	@# -p 1 串行包:契约测试会 TRUNCATE 共享 testpg,串行避免与 tests/integration e2e 并发互踩。
	go test -race -tags=integration -count=1 -p 1 ./...

rag-eval: ## 算法消融：比较切片、分词、召回与 RRF 参数（不代表生产链路）
	python3 evals/run_rag_eval.py

rag-eval-production: ## 生产链路：通过 HTTP 评测 Go/GSE/PostgreSQL/真实向量模型
	python3 evals/run_rag_production_eval.py

# ===== Langfuse 可观测环境 =====

langfuse-preflight: ## 检查 Langfuse 环境依赖、端口和 Compose 配置
	@bash scripts/langfuse-preflight.sh

langfuse-up: up ## 兼容入口：等同于 make up

langfuse-ps: ps ## 兼容入口：等同于 make ps

langfuse-logs: logs ## 兼容入口：等同于 make logs

langfuse-down: down ## 兼容入口：等同于 make down

langfuse-reset: ## 删除 Langfuse 环境及其数据卷
	@echo "即将删除 ecommerce-ops-agent Compose 项目的容器和数据卷。"
	$(LANGFUSE_COMPOSE) down -v --remove-orphans
