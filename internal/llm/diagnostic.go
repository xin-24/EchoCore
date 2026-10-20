package llm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
)

// Diagnostic 只保留固定分类与数字，不保存服务端正文、URL、密钥或思考内容。
// 用量是否已知单独标记，避免把未知用量记录成零。
type Diagnostic struct {
	ToolCount            int
	MaxTools             int
	ToolHistoryCount     int
	MaxToolHistory       int
	Kind                 string
	HTTPStatus           int
	FinishReason         string
	ReasoningPresent     bool
	OutputTokens         int
	UsageKnown           bool
	ReasoningTokens      int
	ReasoningTokensKnown bool
}

type callError struct {
	diagnostic Diagnostic
	cause      error
}

func (e *callError) Error() string {
	d := e.diagnostic.safe()
	return fmt.Sprintf("模型调用失败：kind=%s http_status=%d finish_reason=%s", d.Kind, d.HTTPStatus, d.FinishReason)
}
func (e *callError) Unwrap() error { return e.cause }

func failure(kind string, cause error, d Diagnostic) error {
	d.Kind = kind
	return &callError{diagnostic: d.safe(), cause: cause}
}

// Diagnose 永不透传未知 error 的文本；错误链仅用于分类。
func Diagnose(err error) Diagnostic {
	if err == nil {
		return Diagnostic{}
	}
	var e *callError
	if errors.As(err, &e) {
		return e.diagnostic.safe()
	}
	kind := "unknown"
	var dns *net.DNSError
	var cert *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var record tls.RecordHeaderError
	var network net.Error
	var op *net.OpError
	switch {
	case errors.Is(err, ErrInvalidRequest):
		kind = "invalid_request"
	case errors.Is(err, context.Canceled):
		kind = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		kind = "timeout"
	case errors.As(err, &network) && network.Timeout():
		kind = "timeout"
	case errors.As(err, &dns):
		kind = "dns"
	case errors.As(err, &cert), errors.As(err, &authority), errors.As(err, &hostname), errors.As(err, &record):
		kind = "tls"
	case errors.As(err, &op):
		kind = "connection"
	case errors.Is(err, ErrInvalidResponse):
		kind = "invalid_response"
	case errors.Is(err, ErrProvider):
		kind = "provider_error"
	}
	return Diagnostic{Kind: kind}
}

func transportFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	d := Diagnose(err)
	if d.Kind == "unknown" {
		d.Kind = "network"
	}
	cause := ErrProvider
	if d.Kind == "timeout" {
		cause = context.DeadlineExceeded
	}
	return failure(d.Kind, cause, d)
}

func statusFailure(status int) error {
	kind := "http_error"
	switch {
	case status == 400 || status == 413 || status == 422:
		kind = "invalid_request"
	case status == 401:
		kind = "authentication"
	case status == 402:
		kind = "quota"
	case status == 403:
		kind = "permission"
	case status == 404:
		kind = "not_found"
	case status == 429:
		kind = "rate_limit"
	case status >= 300 && status < 400:
		kind = "redirect"
	case status >= 500:
		kind = "upstream"
	}
	return failure(kind, ErrProvider, Diagnostic{HTTPStatus: status})
}

func (d Diagnostic) safe() Diagnostic {
	switch d.Kind {
	case "", "unknown", "canceled", "timeout", "dns", "tls", "connection", "network", "invalid_response", "provider_error", "http_error", "invalid_request", "authentication", "quota", "permission", "not_found", "rate_limit", "redirect", "upstream", "invalid_json", "invalid_schema", "response_too_large", "empty_content", "output_limit", "content_filtered", "invalid_decision", "too_many_tools", "tool_history_limit", "invalid_tool_history", "invalid_tool_schema":
	default:
		d.Kind = "unknown"
	}
	switch d.FinishReason {
	case "", "stop", "length", "content_filter", "tool_calls", "function_call", "error":
	default:
		d.FinishReason = "other"
	}
	if d.HTTPStatus < 100 || d.HTTPStatus > 599 {
		d.HTTPStatus = 0
	}
	if d.OutputTokens < 0 {
		d.UsageKnown = false
		d.OutputTokens = 0
	}
	if d.ReasoningTokens < 0 {
		d.ReasoningTokensKnown = false
		d.ReasoningTokens = 0
	}
	return d
}

// LogFields 的 prefix 由代码固定传入，用于区分 Jev 和兜底的失败。
func (d Diagnostic) LogFields(prefix string) []any {
	d = d.safe()
	var out []any
	if d.Kind == "too_many_tools" && d.ToolCount > 0 && d.MaxTools > 0 {
		out = append(out, prefix+"tool_count", d.ToolCount, prefix+"max_tools", d.MaxTools)
	}
	if d.Kind == "tool_history_limit" && d.ToolHistoryCount > 0 && d.MaxToolHistory > 0 {
		out = append(out, prefix+"tool_history_count", d.ToolHistoryCount, prefix+"max_tool_history", d.MaxToolHistory)
	}
	if d.Kind != "" {
		out = append(out, prefix+"error_kind", d.Kind)
	}
	if d.HTTPStatus != 0 {
		out = append(out, prefix+"http_status", d.HTTPStatus)
	}
	if d.FinishReason != "" {
		out = append(out, prefix+"finish_reason", d.FinishReason)
	}
	if d.ReasoningPresent {
		out = append(out, prefix+"reasoning_present", true)
	}
	if d.UsageKnown {
		out = append(out, prefix+"completion_tokens", d.OutputTokens)
	}
	if d.ReasoningTokensKnown {
		out = append(out, prefix+"reasoning_tokens", d.ReasoningTokens)
	}
	return out
}
