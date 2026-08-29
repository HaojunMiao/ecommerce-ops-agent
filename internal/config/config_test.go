package config

import (
	"reflect"
	"testing"
)

func TestSplitList(t *testing.T) {
	want := []string{"https://admin.example.com", "http://localhost:5173"}
	got := splitList(" https://admin.example.com, ,http://localhost:5173 ")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitList() = %#v, want %#v", got, want)
	}
}

func TestValidateRejectsDemoDefaultsInProduction(t *testing.T) {
	cfg := Config{
		DatabaseURL: "postgres://db", JWTSecretKey: "dev-secret-key-32-chars-minimum",
		CredentialEncryptionKey: "dev-credential-key-minimum-32-chars",
		Environment:             "prod", CORSAllowedOrigins: []string{"https://admin.example.com"},
		LLMTimeoutMS: 120000, EmbedderDim: 2048,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected production default secret rejection")
	}
}

func TestValidateAcceptsHardenedProductionConfig(t *testing.T) {
	cfg := Config{
		DatabaseURL: "postgres://db", JWTSecretKey: "jwt-production-secret-0000000000000001",
		CredentialEncryptionKey: "credential-production-secret-000000001",
		Environment:             "prod", CORSAllowedOrigins: []string{"https://admin.example.com"},
		LLMTimeoutMS: 120000, EmbedderDim: 2048,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestLoadModelVersionParameters(t *testing.T) {
	t.Setenv("KBOT_LLM_TIMEOUT_MS", "90000")
	t.Setenv("KBOT_LLM_MAX_RETRIES", "3")
	t.Setenv("KBOT_LLM_INPUT_PRICE_PER_MILLION", "1.25")
	t.Setenv("KBOT_LLM_OUTPUT_PRICE_PER_MILLION", "2.5")
	t.Setenv("KBOT_LLM_CACHED_INPUT_PRICE_PER_MILLION", "0.25")

	cfg := Load()
	if cfg.LLMTimeoutMS != 90000 || cfg.LLMMaxRetries != 3 {
		t.Fatalf("runtime limits = (%d, %d), want (90000, 3)", cfg.LLMTimeoutMS, cfg.LLMMaxRetries)
	}
	if cfg.LLMInputPricePerMillion != 1.25 || cfg.LLMOutputPricePerMillion != 2.5 || cfg.LLMCachedInputPricePerMillion != 0.25 {
		t.Fatalf("model prices not loaded: %+v", cfg)
	}
}

func TestValidateRejectsInvalidModelRuntimeParameters(t *testing.T) {
	cfg := Config{
		DatabaseURL: "postgres://db", JWTSecretKey: "jwt-development-secret-000000000000001",
		CredentialEncryptionKey: "credential-development-secret-00000001",
		Environment:             "dev", LLMTimeoutMS: 0, EmbedderDim: 2048,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("zero model timeout must be rejected")
	}
	cfg.LLMTimeoutMS = 120000
	cfg.LLMMaxRetries = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("negative model retries must be rejected")
	}
}

func TestLoadUsesIndependentEmbeddingConfiguration(t *testing.T) {
	t.Setenv("KBOT_LLM_BASE_URL", "https://chat.example/v1")
	t.Setenv("KBOT_LLM_API_KEY", "chat-secret")
	t.Setenv("KBOT_EMBEDDER", "openai")
	t.Setenv("KBOT_EMBEDDER_BASE_URL", "https://embedding.example/v1")
	t.Setenv("KBOT_EMBEDDER_API_KEY", "embedding-secret")
	t.Setenv("KBOT_EMBEDDER_MODEL", "example/embedding-model")
	t.Setenv("KBOT_EMBEDDER_DIM", "2048")

	cfg := Load()
	if cfg.EmbedderBaseURL != "https://embedding.example/v1" || cfg.EmbedderAPIKey != "embedding-secret" {
		t.Fatalf("embedding config was not loaded independently: %+v", cfg)
	}
	if cfg.LLMBaseURL != "https://chat.example/v1" || cfg.LLMAPIKey != "chat-secret" {
		t.Fatal("embedding config unexpectedly changed chat config")
	}
}

func TestValidateRequiresOpenAIEmbeddingCredentials(t *testing.T) {
	cfg := Config{
		DatabaseURL: "postgres://db", JWTSecretKey: "jwt-development-secret-000000000000001",
		CredentialEncryptionKey: "credential-development-secret-00000001",
		Environment:             "dev", LLMTimeoutMS: 120000,
		EmbedderKind: "openai", EmbedderBaseURL: "https://embedding.example/v1",
		EmbedderModel: "example/embedding-model", EmbedderDim: 2048,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("missing embedding API key must be rejected")
	}
	cfg.EmbedderAPIKey = "secret"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid independent embedding config rejected: %v", err)
	}
}

func TestLoadPreservesInvalidNegativePriceForValidation(t *testing.T) {
	t.Setenv("KBOT_LLM_INPUT_PRICE_PER_MILLION", "-1")
	cfg := Load()
	if cfg.LLMInputPricePerMillion != -1 {
		t.Fatalf("input price = %v, want -1 so validation can reject it", cfg.LLMInputPricePerMillion)
	}
}
