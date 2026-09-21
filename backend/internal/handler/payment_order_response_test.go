//go:build unit

package handler

import (
	"encoding/json"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

func TestSanitizePaymentOrderForResponseIncludesProviderTradeNumber(t *testing.T) {
	order := &dbent.PaymentOrder{
		OutTradeNo:     "sub2_internal_order",
		PaymentTradeNo: "2026091823001481451440748515",
	}

	result := sanitizePaymentOrderForResponse(order)
	if result == nil {
		t.Fatal("sanitizePaymentOrderForResponse returned nil")
	}
	if result.PaymentTradeNo != order.PaymentTradeNo {
		t.Fatalf("payment_trade_no = %q, want %q", result.PaymentTradeNo, order.PaymentTradeNo)
	}
}

func TestPaymentOrderResultSerializesProviderTradeNumber(t *testing.T) {
	for _, tradeNo := range []string{"", "2026091823001481451440748515"} {
		t.Run(tradeNo, func(t *testing.T) {
			result := sanitizePaymentOrderForResponse(&dbent.PaymentOrder{PaymentTradeNo: tradeNo})
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatal(err)
			}
			value, present := payload["payment_trade_no"]
			if tradeNo == "" {
				if present {
					t.Fatal("empty provider trade number must be omitted")
				}
			} else if value != tradeNo {
				t.Fatalf("payment_trade_no = %v, want %q", value, tradeNo)
			}
		})
	}
}

func TestPublicOrderVerificationPreservesPaidSubscriptionReviewState(t *testing.T) {
	now := time.Now()
	result := buildPublicOrderVerifyResult(&dbent.PaymentOrder{OutTradeNo: "synthetic", OrderType: "subscription", Status: "FAILED", PaidAt: &now})
	if result.OrderType != "subscription" || !result.Paid || result.PaidAt == nil {
		t.Fatalf("paid review order lost payment state: %+v", result)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"user_id", "user_email", "amount", "plan_id", "failed_reason"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("anonymous verification exposed %s", key)
		}
	}
}
