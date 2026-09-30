package handler

import "github.com/Wei-Shaw/sub2api/internal/service"

// inflightSkipsSubscriptionSettlement 订阅结算的 Key（含显式 billing_source=subscription 且所选分组不是订阅型）
// 不从余额扣费，不能为它登记余额在途预留，否则余额不足的订阅用户会被误拦。
func inflightSkipsSubscriptionSettlement(apiKey *service.APIKey, subscription *service.UserSubscription) bool {
	return subscription != nil && apiKey != nil && apiKey.UsesSubscriptionBilling()
}
