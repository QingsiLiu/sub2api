package service

import (
	"context"
	"io"
)

const nativePaidSupplierDebtMaxBytesGeili = 512 << 10

// Capture alongside the native reader, without changing its bytes or errors.
// A complete supplier error may precede a truncated/cancelled HTTP body.
type nativePaidSupplierDebtCaptureGeili struct {
	io.Reader
	body []byte
}

func (c *nativePaidSupplierDebtCaptureGeili) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	remaining := nativePaidSupplierDebtMaxBytesGeili - len(c.body)
	if remaining > 0 {
		c.body = append(c.body, p[:min(n, remaining)]...)
	}
	return n, err
}

// Native create paths retain their own protocol/error handling. Observe only
// confirmed supplier debt before those handlers can return or apply quota rules.
// stopSupplierBalanceGeili persists a fixed reason with a bounded detached
// context, so a client cancellation cannot discard an already seen debt fact.
func observeNativePaidSupplierDebtGeili(ctx context.Context, limits *RateLimitService, account *Account, status int, body []byte) bool {
	if len(body) > nativePaidSupplierDebtMaxBytesGeili {
		body = body[:nativePaidSupplierDebtMaxBytesGeili]
	}
	return limits != nil && limits.stopSupplierBalanceGeili(ctx, account, status, body)
}
