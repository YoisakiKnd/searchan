package platform

import (
	"context"
	"errors"
	"time"

	"searchan/render"
)

// RateLimitError is an explicit platform refusal, not an ambiguous timeout.
type RateLimitError struct{ After time.Duration }

func (e *RateLimitError) Error() string { return "platform rate limit exceeded" }

// RetrySender retries only explicit rate-limit refusals. All transport errors,
// 5xx responses and missing receipts retain the conservative outbox behavior.
type RetrySender struct{ Next Sender }

func retrySend(ctx context.Context, fn func() (Receipt, error)) (Receipt, error) {
	for attempt := 0; ; attempt++ {
		receipt, err := fn()
		var limited *RateLimitError
		if !errors.As(err, &limited) || attempt >= 2 || limited.After <= 0 || limited.After > 5*time.Second {
			return receipt, err
		}
		timer := time.NewTimer(limited.After)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return Receipt{}, err // The previous send was definitely refused.
		}
		if ctx.Err() != nil {
			return Receipt{}, err
		}
	}
}
func (r RetrySender) SendText(ctx context.Context, target Target, text string, seq int) (Receipt, error) {
	return retrySend(ctx, func() (Receipt, error) { return r.Next.SendText(ctx, target, text, seq) })
}
func (r RetrySender) SendImage(ctx context.Context, target Target, image render.Artifact, caption string, seq int) (Receipt, error) {
	return retrySend(ctx, func() (Receipt, error) { return r.Next.SendImage(ctx, target, image, caption, seq) })
}
func (r RetrySender) SupportsReport() bool {
	next, ok := r.Next.(ReportSender)
	return ok && next.SupportsReport()
}
func (r RetrySender) SendReport(ctx context.Context, target Target, report Report, seq int) (Receipt, error) {
	next, ok := r.Next.(ReportSender)
	if !ok || !next.SupportsReport() {
		return Receipt{}, &RejectedError{Code: 400}
	}
	return retrySend(ctx, func() (Receipt, error) { return next.SendReport(ctx, target, report, seq) })
}
