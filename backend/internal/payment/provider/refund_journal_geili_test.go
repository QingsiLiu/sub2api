//go:build unit

package provider

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/smartwalle/alipay/v3"
	"github.com/stretchr/testify/require"
	"github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic"
)

func TestAlipayRefundJournalKeepsRequestIdentityAndVerifiesQuery(t *testing.T) {
	originalRefund, originalQuery := alipayTradeRefund, alipayTradeRefundQuery
	t.Cleanup(func() { alipayTradeRefund = originalRefund; alipayTradeRefundQuery = originalQuery })
	a := &Alipay{client: &alipay.Client{}}
	var keys []string
	alipayTradeRefund = func(_ context.Context, _ *alipay.Client, param alipay.TradeRefund) (*alipay.TradeRefundRsp, error) {
		keys = append(keys, param.OutRequestNo)
		return &alipay.TradeRefundRsp{TradeNo: "trade-7", FundChange: "Y"}, nil
	}
	for i := 0; i < 2; i++ {
		_, err := a.Refund(context.Background(), payment.RefundRequest{OrderID: "order-7", Amount: "1.00", IdempotencyKey: "geili-refund-7"})
		require.NoError(t, err)
	}
	require.Equal(t, []string{"geili-refund-7", "geili-refund-7"}, keys)
	result := &alipay.TradeFastPayRefundQueryRsp{TradeNo: "trade-7", OutTradeNo: "order-7", OutRequestNo: "geili-refund-7", RefundAmount: "1.00", RefundStatus: "REFUND_SUCCESS"}
	alipayTradeRefundQuery = func(_ context.Context, _ *alipay.Client, p alipay.TradeFastPayRefundQuery) (*alipay.TradeFastPayRefundQueryRsp, error) {
		require.Equal(t, "geili-refund-7", p.OutRequestNo)
		return result, nil
	}
	req := payment.RefundQueryRequest{OrderID: "order-7", Amount: "1.00", IdempotencyKey: "geili-refund-7"}
	response, err := a.QueryRefund(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, payment.ProviderStatusSuccess, response.Status)
	result.RefundStatus = ""
	response, err = a.QueryRefund(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, payment.ProviderStatusPending, response.Status)
	result.RefundAmount = "2.00"
	_, err = a.QueryRefund(context.Background(), req)
	require.Error(t, err)
	result.RefundAmount = "1.00"
	result.OutRequestNo = "someone-elses-refund"
	_, err = a.QueryRefund(context.Background(), req)
	require.Error(t, err)
}

func TestWxpayRefundAbnormalIsNotConfirmedFailure(t *testing.T) {
	require.Equal(t, payment.ProviderStatusPending, wxpayRefundProviderStatus(refunddomestic.STATUS_ABNORMAL))
	require.Equal(t, payment.ProviderStatusPending, wxpayRefundProviderStatus(refunddomestic.STATUS_PROCESSING))
	require.Equal(t, payment.ProviderStatusSuccess, wxpayRefundProviderStatus(refunddomestic.STATUS_SUCCESS))
	require.Equal(t, payment.ProviderStatusFailed, wxpayRefundProviderStatus(refunddomestic.STATUS_CLOSED))
}
