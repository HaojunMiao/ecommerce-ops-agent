// Package config 从环境变量加载 kbot 配置。生产密钥只从环境读，绝不写进代码。
package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	// HTTP 服务
	Addr               string   // 监听地址
	CORSAllowedOrigins []string // 浏览器跨域来源白名单

	// 数据库
	DatabaseURL string // Postgres(pgvector) 连接串
	RedisURL    string // Redis 连接串

	ToolAllowedHosts        []string
	ToolAllowPrivateNetwork bool
	KBMarkdownAllowedRoots  []string

	// LLM
	LLMBaseURL     string // OpenAI 兼容网关
	DoubaoAPIKey   string // 豆包模型密钥；ModelConfigVersion 通过 DOUBAO_API_KEY 引用
	LLMModel       string // 主对话模型（需支持工具调用）
	DeepSeekAPIKey string // 可选的 DeepSeek 凭据，供独立 ModelConfigVersion 引用
	// 下列非密钥参数会固化进不可变模型配置版本，配置变化时创建新版本。
	LLMTimeoutMS                  int
	LLMMaxRetries                 int
	LLMInputPricePerMillion       float64
	LLMOutputPricePerMillion      float64
	LLMCachedInputPricePerMillion float64

	// KB 向量化。
	EmbedderKind    string // local | openai
	EmbedderBaseURL string // 独立的 OpenAI 兼容 Embeddings API 地址
	EmbedderAPIKey  string // 独立的 Embedding 密钥，不与聊天模型凭据耦合
	EmbedderDim     int    // 必须与 kb_chunks.embedding halfvec(N) 一致
	EmbedderModel   string // OpenAI 兼容 embedding 模型名

	// KB 检索重排。API Key 未单独配置时复用 Embedding Key。
	RerankerEnabled    bool
	RerankerBaseURL    string
	RerankerAPIKey     string
	RerankerModel      string
	RerankerCandidateK int

	// JWT 认证
	JWTSecretKey            string // JWT 签名密钥
	CredentialEncryptionKey string // Tool 凭据的应用层加密密钥

	// OTEL / Langfuse（本地可观测环境）
	OTLPEndpoint       string  // OTLP traces 完整 URL，如 http://langfuse-web:3000/api/public/otel/v1/traces
	OTLPHeaders        string  // 逗号分隔的 HTTP headers；本地 Langfuse 用 Basic Auth
	OTELSampleRatio    float64 // 0..1，本地演示默认全采样
	OTELCaptureContent bool    // 是否记录模型输入输出；生产默认关闭，本地演示显式开启
	ServiceVersion     string  // OTel service.version / Langfuse release
	LangfuseUIURL      string  // 浏览器可访问的 UI 地址，与容器内 OTLP 地址分开
	LangfuseProjectID  string  // Headless init 创建的项目 ID，用于拼 Trace 深链

	// 环境标识
	Environment string // dev/staging/prod

	// 首启自动 seed admin（仅 dev；prod 应置 false）
	AutoseedAdmin         bool   // 启动时无 admin 用户则自动建一个
	AutoseedAdminEmail    string // 自动 seed 的 admin 邮箱
	AutoseedAdminPassword string // 自动 seed 的 admin 密码
}

func Load() Config {
	cfg := Config{
		Addr:                    getenv("KBOT_ADDR", ":8080"),
		CORSAllowedOrigins:      splitList(getenv("KBOT_CORS_ALLOWED_ORIGINS", "http://localhost:8080,http://localhost:5173")),
		DatabaseURL:             getenv("KBOT_DATABASE_URL", "postgres://kbot:kbot@localhost:5432/kbot?sslmode=disable"),
		RedisURL:                getenv("KBOT_REDIS_URL", "redis://localhost:6379/0"),
		ToolAllowedHosts:        splitList(getenv("KBOT_TOOL_ALLOWED_HOSTS", "crossborder-sim")),
		ToolAllowPrivateNetwork: strings.EqualFold(getenv("KBOT_TOOL_ALLOW_PRIVATE_NETWORK", "false"), "true"),
		KBMarkdownAllowedRoots:  splitList(getenv("KBOT_KB_MARKDOWN_ALLOWED_ROOTS", "projects")),
		LLMBaseURL:              getenv("KBOT_LLM_BASE_URL", "https://ark.cn-beijing.volces.com/api/v3"),
		DoubaoAPIKey:            os.Getenv("DOUBAO_API_KEY"), // 可选：未配置时仅可使用其他已注册的模型凭据
		LLMModel:                getenv("KBOT_LLM_MODEL", "doubao-seed-2-1-pro-260628"),
		DeepSeekAPIKey:          os.Getenv("DEEPSEEK_API_KEY"),
		LLMTimeoutMS:            getenvInt("KBOT_LLM_TIMEOUT_MS", 120000),
		LLMMaxRetries:           getenvInt("KBOT_LLM_MAX_RETRIES", 1),
		LLMInputPricePerMillion: getenvNumber("KBOT_LLM_INPUT_PRICE_PER_MILLION", 0),
		LLMOutputPricePerMillion: getenvNumber(
			"KBOT_LLM_OUTPUT_PRICE_PER_MILLION", 0,
		),
		LLMCachedInputPricePerMillion: getenvNumber(
			"KBOT_LLM_CACHED_INPUT_PRICE_PER_MILLION", 0,
		),
		EmbedderKind:       getenv("KBOT_EMBEDDER", "local"),
		EmbedderBaseURL:    getenv("KBOT_EMBEDDER_BASE_URL", "https://api.siliconflow.cn/v1"),
		EmbedderAPIKey:     os.Getenv("KBOT_EMBEDDER_API_KEY"),
		EmbedderDim:        getenvInt("KBOT_EMBEDDER_DIM", 2048),
		EmbedderModel:      getenv("KBOT_EMBEDDER_MODEL", "Qwen/Qwen3-Embedding-4B"),
		RerankerEnabled:    strings.EqualFold(getenv("KBOT_RERANKER_ENABLED", "false"), "true"),
		RerankerBaseURL:    getenv("KBOT_RERANKER_BASE_URL", "https://api.siliconflow.cn/v1"),
		RerankerAPIKey:     os.Getenv("KBOT_RERANKER_API_KEY"),
		RerankerModel:      getenv("KBOT_RERANKER_MODEL", "Qwen/Qwen3-Reranker-4B"),
		RerankerCandidateK: getenvInt("KBOT_RERANKER_CANDIDATE_K", 10),

		JWTSecretKey:       os.Getenv("KBOT_JWT_SECRET_KEY"), // 必填
		OTLPEndpoint:       getenv("KBOT_OTLP_ENDPOINT", ""), // 可选，为空则禁用
		OTLPHeaders:        getenv("KBOT_OTLP_HEADERS", ""),
		OTELSampleRatio:    getenvFloat("KBOT_OTEL_SAMPLE_RATIO", 1),
		OTELCaptureContent: strings.EqualFold(getenv("KBOT_OTEL_CAPTURE_CONTENT", "false"), "true"),
		ServiceVersion:     getenv("KBOT_SERVICE_VERSION", "dev"),
		LangfuseUIURL:      getenv("KBOT_LANGFUSE_UI_URL", ""),
		LangfuseProjectID:  getenv("KBOT_LANGFUSE_PROJECT_ID", ""),
		Environment:        getenv("KBOT_ENVIRONMENT", "dev"),

		AutoseedAdmin:         getenv("KBOT_AUTOSEED_ADMIN", "false") == "true",
		AutoseedAdminEmail:    getenv("KBOT_AUTOSEED_ADMIN_EMAIL", "admin@example.com"),
		AutoseedAdminPassword: getenv("KBOT_AUTOSEED_ADMIN_PASSWORD", "admin12345"),
	}
	cfg.CredentialEncryptionKey = os.Getenv("KBOT_CREDENTIAL_ENCRYPTION_KEY")
	if cfg.RerankerAPIKey == "" {
		cfg.RerankerAPIKey = cfg.EmbedderAPIKey
	}
	return cfg
}

// MustValidate 校验必填项；缺失就在启动那一刻失败（快速失败，胜过运行时才崩）。
func (c Config) MustValidate() {
	if err := c.Validate(); err != nil {
		log.Fatal(err)
	}
}

// Validate 返回可测试的配置错误；prod 环境额外拒绝演示默认密钥和高风险调试开关。
func (c Config) Validate() error {
	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "KBOT_DATABASE_URL")
	}
	if c.JWTSecretKey == "" {
		missing = append(missing, "KBOT_JWT_SECRET_KEY")
	}
	if c.CredentialEncryptionKey == "" {
		missing = append(missing, "KBOT_CREDENTIAL_ENCRYPTION_KEY")
	}
	if len(missing) > 0 {
		return fmt.Errorf("缺少必填配置：%v", missing)
	}

	// 验证 JWT 密钥长度（至少32字节）
	if len(c.JWTSecretKey) < 32 {
		return fmt.Errorf("KBOT_JWT_SECRET_KEY 长度至少需要32字符")
	}
	if len(c.CredentialEncryptionKey) < 32 {
		return fmt.Errorf("KBOT_CREDENTIAL_ENCRYPTION_KEY 长度至少需要32字符")
	}
	if c.LLMTimeoutMS <= 0 {
		return fmt.Errorf("KBOT_LLM_TIMEOUT_MS 必须大于0")
	}
	if c.LLMMaxRetries < 0 {
		return fmt.Errorf("KBOT_LLM_MAX_RETRIES 不能为负数")
	}
	if c.LLMInputPricePerMillion < 0 || c.LLMOutputPricePerMillion < 0 || c.LLMCachedInputPricePerMillion < 0 {
		return fmt.Errorf("模型价格配置不能为负数")
	}
	if c.EmbedderDim <= 0 {
		return fmt.Errorf("KBOT_EMBEDDER_DIM 必须大于0")
	}
	if c.EmbedderKind != "" && c.EmbedderKind != "local" && c.EmbedderKind != "openai" {
		return fmt.Errorf("KBOT_EMBEDDER 必须是 local 或 openai")
	}
	if c.EmbedderKind == "openai" {
		var missingEmbedder []string
		if strings.TrimSpace(c.EmbedderBaseURL) == "" {
			missingEmbedder = append(missingEmbedder, "KBOT_EMBEDDER_BASE_URL")
		}
		if strings.TrimSpace(c.EmbedderAPIKey) == "" {
			missingEmbedder = append(missingEmbedder, "KBOT_EMBEDDER_API_KEY")
		}
		if strings.TrimSpace(c.EmbedderModel) == "" {
			missingEmbedder = append(missingEmbedder, "KBOT_EMBEDDER_MODEL")
		}
		if len(missingEmbedder) > 0 {
			return fmt.Errorf("openai Embedding 缺少配置：%v", missingEmbedder)
		}
	}
	if c.RerankerEnabled {
		var missingReranker []string
		if strings.TrimSpace(c.RerankerBaseURL) == "" {
			missingReranker = append(missingReranker, "KBOT_RERANKER_BASE_URL")
		}
		if strings.TrimSpace(c.RerankerAPIKey) == "" {
			missingReranker = append(missingReranker, "KBOT_RERANKER_API_KEY/KBOT_EMBEDDER_API_KEY")
		}
		if strings.TrimSpace(c.RerankerModel) == "" {
			missingReranker = append(missingReranker, "KBOT_RERANKER_MODEL")
		}
		if len(missingReranker) > 0 {
			return fmt.Errorf("Reranker 缺少配置：%v", missingReranker)
		}
		if c.RerankerCandidateK <= 0 {
			return fmt.Errorf("KBOT_RERANKER_CANDIDATE_K 必须大于0")
		}
	}
	if !strings.EqualFold(c.Environment, "prod") {
		return nil
	}
	weakSecrets := map[string]string{
		"KBOT_JWT_SECRET_KEY":            c.JWTSecretKey,
		"KBOT_CREDENTIAL_ENCRYPTION_KEY": c.CredentialEncryptionKey,
	}
	knownDefaults := []string{
		"dev-secret-key-32-chars-minimum",
		"dev-credential-key-minimum-32-chars",
	}
	for key, value := range weakSecrets {
		for _, known := range knownDefaults {
			if value == known {
				return fmt.Errorf("prod 环境禁止使用演示默认密钥：%s", key)
			}
		}
	}
	if c.JWTSecretKey == c.CredentialEncryptionKey {
		return fmt.Errorf("prod 环境要求 JWT 与凭据加密使用不同密钥")
	}
	if c.AutoseedAdmin {
		return fmt.Errorf("prod 环境禁止自动初始化演示账号或资产")
	}
	if c.OTELCaptureContent {
		return fmt.Errorf("prod 环境默认禁止采集完整模型输入输出")
	}
	if c.ToolAllowPrivateNetwork {
		return fmt.Errorf("prod 环境禁止全局放行 Tool 私网访问，请使用显式 host allowlist")
	}
	for _, origin := range c.CORSAllowedOrigins {
		if origin == "*" || strings.Contains(origin, "localhost") || strings.Contains(origin, "127.0.0.1") {
			return fmt.Errorf("prod 环境 CORS 来源必须是明确的生产域名：%s", origin)
		}
	}
	return nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getenvFloat(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			if n < 0 {
				return 0
			}
			if n > 1 {
				return 1
			}
			return n
		}
	}
	return def
}

func getenvNumber(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return def
}

func splitList(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// JWTKeyBytes 返回JWT密钥的字节数组
func (c Config) JWTKeyBytes() []byte {
	return []byte(c.JWTSecretKey)
}
