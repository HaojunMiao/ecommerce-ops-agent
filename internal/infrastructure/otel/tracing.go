// Package otel 统一 Agent 运行的 OpenTelemetry 与 Langfuse 属性。
package otel

import (
	"context"
	"errors"
	"os"
	"strings"

	globalotel "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
)

type RunContext struct{ WorkspaceID, AgentVersionID, ConversationID, UserID string }

// Setup 在配置 OTLP 地址时安装批量导出的 SDK；未配置时保持无操作，
// 因此本地开发不启动 Langfuse/Collector 也能正常运行。
func Setup(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	if strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) == "" &&
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) == "" {
		return func(context.Context) error { return nil }, nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
	))
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	globalotel.SetTracerProvider(provider)
	return func(shutdownCtx context.Context) error {
		return errors.Join(provider.ForceFlush(shutdownCtx), provider.Shutdown(shutdownCtx))
	}, nil
}

// Attributes 使用稳定业务维度把一次 Trace 与工作空间、会话、用户和版本关联起来。
func Attributes(run RunContext) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("kbot.workspace.id", run.WorkspaceID),
		attribute.String("kbot.agent.version.id", run.AgentVersionID),
		attribute.String("kbot.conversation.id", run.ConversationID),
		attribute.String("langfuse.trace.name", "agent.run"),
		attribute.String("langfuse.session.id", run.ConversationID),
		attribute.String("langfuse.user.id", run.UserID),
		attribute.String("langfuse.version", run.AgentVersionID),
	}
}

// StartRun 从传入 ctx 创建根运行 Span，保留取消信号和上游 Trace 关系。
func StartRun(ctx context.Context, run RunContext) (context.Context, func(error)) {
	ctx, span := globalotel.Tracer("github.com/HaojunMiao/ecommerce-ops-agent/runtime").Start(
		ctx, "agent.run", trace.WithAttributes(Attributes(run)...),
	)
	return ctx, func(err error) {
		finishSpan(span, err)
	}
}

func StartOperation(ctx context.Context, name string, attributes ...attribute.KeyValue) (context.Context, func(error)) {
	ctx, span := globalotel.Tracer("github.com/HaojunMiao/ecommerce-ops-agent/runtime").Start(
		ctx, name, trace.WithAttributes(attributes...),
	)
	return ctx, func(err error) {
		finishSpan(span, err)
	}
}

func finishSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "completed")
	}
	span.End()
}
