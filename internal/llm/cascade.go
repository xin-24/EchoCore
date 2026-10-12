package llm

import (
	"context"
	"errors"
	"time"
)

// CascadeClient 仅替换判断职责；生成仍委托原客户端。构造后不可变。
type CascadeClient struct {
	Base              Client
	Primary           Decider
	Threshold         float64
	Timeout           time.Duration
	UnavailableReason string
}

func (c *CascadeClient) Generate(ctx context.Context, r GenerateRequest) (GenerateResponse, error) {
	return c.Base.Generate(ctx, r)
}

func (c *CascadeClient) Decide(ctx context.Context, r DecideRequest) (DecideResponse, error) {
	if err := ctx.Err(); err != nil {
		return DecideResponse{}, err
	}
	reason := c.UnavailableReason
	var confidence *float64
	var primaryUsage *Usage
	var primaryDiagnostic Diagnostic
	if c.Primary != nil {
		child, cancel := context.WithTimeout(ctx, c.Timeout)
		result, err := c.Primary.Decide(child, r)
		childErr := child.Err()
		cancel()
		primaryUsage = result.Usage
		primaryDiagnostic = result.Diagnostic
		if err != nil {
			primaryDiagnostic = Diagnose(err)
		}
		if childErr != nil {
			primaryDiagnostic = Diagnose(childErr)
		}
		if ValidConfidence(result.Confidence) {
			confidence = result.Confidence
		}
		if ctx.Err() != nil {
			return DecideResponse{}, ctx.Err()
		}
		switch {
		case errors.Is(childErr, context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
			reason = "jev_timeout"
		case errors.Is(err, ErrInvalidResponse):
			reason = "jev_invalid_response"
		case err != nil:
			reason = "jev_error"
		case !ValidConfidence(result.Confidence):
			reason = "jev_invalid_confidence"
		case result.Decision == DecisionUnclear:
			reason = "jev_unclear"
		case result.Decision != DecisionReply && result.Decision != DecisionIgnore:
			reason = "jev_invalid_choice"
		case *result.Confidence < c.Threshold:
			reason = "jev_low_confidence"
			confidence = result.Confidence
		default:
			result.Source = "jev"
			return result, nil
		}
	}
	if reason == "" {
		reason = "jev_disabled"
	}
	if ctx.Err() != nil {
		return DecideResponse{}, ctx.Err()
	}
	result, err := c.Base.Decide(ctx, r)
	if ctx.Err() != nil {
		return DecideResponse{}, ctx.Err()
	}
	if err != nil {
		return DecideResponse{Decision: DecisionIgnore, Source: "router_llm", Reason: "fallback_error", FallbackReason: reason, Confidence: confidence, PrimaryUsage: primaryUsage, Usage: result.Usage, Diagnostic: Diagnose(err), PrimaryDiagnostic: primaryDiagnostic}, nil
	}
	result.Source = "router_llm"
	result.FallbackReason = reason
	result.Confidence = confidence
	result.PrimaryUsage = primaryUsage
	result.PrimaryDiagnostic = primaryDiagnostic
	if result.Decision != DecisionReply && result.Decision != DecisionIgnore {
		result.Decision = DecisionIgnore
		result.Reason = "invalid_decision"
	}
	return result, nil
}
