// Package platform 提供平台服务整合
package platform

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	pgstore "github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres/sqlc"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/agent"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/approval"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/audit"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/iam"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/kb"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/modelconfig"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/prompt"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/skill"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/tool"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/cache"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/guard"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/promptcache"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/retriever"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/tooling"
)

// Service 平台服务整合
type Service struct {
	IAM         *iam.Service
	Prompt      *prompt.Service
	ModelConfig *modelconfig.Service
	Agent       *agent.Service
	Tool        *tool.Service
	KB          *kb.Service
	Skill       *skill.Service
	Audit       *audit.Service

	Approvals approval.Store

	// Runtime 依赖（数据面）
	Registry    *tooling.Registry
	Retriever   retriever.Searcher
	PromptCache *promptcache.Cache
	Guard       *guard.Guard
	EmbedCache  *cache.EmbeddingCache
}

// KBSearchSDKName 是 KB 检索 internal_sdk 工具的注册名。
// Tool Registry 里 source_type=internal_sdk 且 endpoint_config.sdk_name 为此值的工具
// 会被路由到 KB 检索执行器。
const KBSearchSDKName = "search_knowledge_base"

// pgvectorDim 是当前知识库向量列的统一维度。
// 数据库使用 halfvec，以支持超过 vector HNSW 2000 维上限的向量模型。
const pgvectorDim = 2048

// rateLimitPerMin 是每个身份、每个 hook 的每分钟请求上限。
const rateLimitPerMin = 1000

// NewService 创建平台服务。
//   - pub:Prompt 失效广播器(无 Redis 传 nil)。
//   - embedder:KB 向量化嵌入器(nil 时默认 LocalEmbedder(256),仅内存路径用)。
//   - kbEnqueuer:KB ingest 异步投递器(nil 时 Sync 走进程内同步 ingest)。
func NewService(db *pgxpool.Pool, rds *redis.Client, jwtKey []byte, pub prompt.Publisher, embedder retriever.Embedder, kbEnqueuer kb.TaskEnqueuer, credentialKeys ...[]byte) *Service {
	// server/worker 使用 PostgreSQL，无 db 的单元测试使用内存存储。
	var (
		iamStore    iam.Store
		promptStore prompt.Store
		agentStore  agent.Store
		toolStore   tool.Store
		kbStore     kb.Store
		skillStore  skill.Store
		auditStore  audit.Store
		apprStore   approval.Store
		modelStore  modelconfig.Store
	)
	if db != nil {
		q := pgstore.New(db)
		iamStore = iam.NewPostgresStore(db, q)
		promptStore = prompt.NewPostgresStore(db, q)
		agentStore = agent.NewPostgresStore(db, q)
		toolStore = tool.NewPostgresStore(q)
		kbStore = kb.NewPostgresStore(q)
		skillStore = skill.NewPostgresStore(q)
		auditStore = audit.NewPostgresStore(q)
		apprStore = approval.NewPostgresStore(q)
		modelStore = modelconfig.NewPostgresStore(db)
	} else {
		iamStore = NewMemoryIAMStore()
		promptStore = NewMemoryPromptStore()
		agentStore = NewMemoryAgentStore()
		toolStore = NewMemoryToolStore()
		kbStore = NewMemoryKBStore()
		skillStore = NewMemorySkillStore()
		auditStore = audit.NewMemoryStore()
		apprStore = approval.NewMemoryStore()
		modelStore = modelconfig.NewMemoryStore()
	}

	// Runtime 数据面：检索器（带嵌入缓存）+ 工具注册表 + Prompt 客户端缓存。
	if embedder == nil {
		embedder = retriever.NewLocalEmbedder(256) // 内存/测试默认
	}
	embedCache := cache.NewEmbeddingCache(embedder)
	// db != nil → pgvector(chunk 落 kb_chunks,跨进程共享 + 重启持久);否则进程内内存索引。
	var kbSearcher retriever.Searcher
	if db != nil {
		if embedCache.Dim() != pgvectorDim {
			log.Fatalf("KBOT_EMBEDDER_DIM=%d 与 kb_chunks.embedding halfvec(%d) 不一致;改维度需迁移并重灌全部 KB", embedCache.Dim(), pgvectorDim)
		}
		kbSearcher = retriever.NewPgvectorRetriever(db, embedCache)
	} else {
		kbSearcher = retriever.New(embedCache)
	}
	kbService := kb.NewService(kbStore, kbSearcher, kbEnqueuer)
	pcache := promptcache.NewCache()

	auditService := audit.NewService(auditStore)
	// Redis 提供跨进程限流，单元测试使用内存实现。
	rateLimiter := guard.NewLimiter(rds, rateLimitPerMin, time.Minute)
	guardEngine := guard.New(newAuditRecorder(auditService)).
		Add(guard.NewInjectionRule()).
		Add(guard.NewPIIRule()).
		Add(guard.NewRateLimitRule(guard.HookOnInput, "input", rateLimiter)).
		Add(guard.NewRateLimitRule(guard.HookOnLLMCall, "llm", rateLimiter)).
		Add(guard.NewRateLimitRule(guard.HookOnToolCall, "tool", rateLimiter))

	// 服务层
	iamService := iam.NewService(iamStore, jwtKey)
	credentialKey := jwtKey
	if len(credentialKeys) > 0 && len(credentialKeys[0]) > 0 {
		credentialKey = credentialKeys[0]
	}
	credentialCipher, err := tool.NewCipher(credentialKey)
	if err != nil {
		log.Fatalf("initialize credential cipher: %v", err)
	}
	modelService := modelconfig.NewService(modelStore)
	promptService := prompt.NewService(promptStore, pcache, pub).WithModelConfigs(modelService)
	toolService := tool.NewService(toolStore)
	toolService.ConfigureSecurity(credentialCipher, nil)
	skillService := skill.NewService(skillStore, toolService).WithKBChecker(kbService)
	agentService := agent.NewService(agentStore, promptService, skillService, toolService).WithKBResolver(kbService)
	registry := tooling.NewRegistry(toolService)
	// 把 KB 检索注册为 internal_sdk 工具的执行器。
	registry.RegisterSDK(KBSearchSDKName,
		"在企业知识库中按语义+关键词混合检索相关片段；回答事实性问题前应先调用它。",
		kbSearchParamsSchema,
		newKBSearchExecutor(kbService))

	return &Service{
		IAM:         iamService,
		Prompt:      promptService,
		ModelConfig: modelService,
		Agent:       agentService,
		Tool:        toolService,
		KB:          kbService,
		Skill:       skillService,
		Audit:       auditService,
		Approvals:   apprStore,
		Registry:    registry,
		Retriever:   kbSearcher,
		PromptCache: pcache,
		Guard:       guardEngine,
		EmbedCache:  embedCache,
	}
}

// auditRecorder 把 audit.Service 适配成 guard.Recorder（拦截事件入审计 + 注入日志）。
type auditRecorder struct{ a *audit.Service }

func newAuditRecorder(a *audit.Service) guard.Recorder { return &auditRecorder{a: a} }

func (r *auditRecorder) Record(ctx context.Context, ev guard.Event) {
	action := "guard_" + string(ev.Hook)
	if !ev.Allowed {
		action = "guard_block"
	}
	r.a.Record(ctx, "system", action+":"+ev.Rule, "guard", ev.Reason)
}

// kbSearchParamsSchema 是 KB 检索工具暴露给模型的参数 schema。
const kbSearchParamsSchema = `{
  "type": "object",
  "properties": {
    "kb_id": {"type": "string", "description": "要检索的知识库 ID"},
    "query": {"type": "string", "description": "检索问题或关键词"},
    "top_k": {"type": "integer", "description": "返回片段数，默认 5"}
  },
  "required": ["kb_id", "query"]
}`

// newKBSearchExecutor 构造 KB 检索的 internal_sdk 执行器。
func newKBSearchExecutor(kbService *kb.Service) tooling.ExecutorFunc {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			KbID  string `json:"kb_id"`
			Query string `json:"query"`
			TopK  int    `json:"top_k"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		if in.TopK == 0 {
			in.TopK = 5
		}
		passages, err := kbService.Search(ctx, in.KbID, in.Query, in.TopK)
		if err != nil {
			return "", err
		}
		out, err := json.Marshal(passages)
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
}
