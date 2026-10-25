package routes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestCompositeRouteErrorGeiliPreservesCauseAndServerAttribution(t *testing.T) {
	for _, tc := range []struct {
		name                string
		err                 error
		status              int
		kind, code, message string
	}{
		{"persistence", errors.New("catalog database offline"), 500, "api_error", "", "internal error"},
		{"deadline", fmt.Errorf("read routing permissions: %w", context.DeadlineExceeded), 503, "api_error", "ROUTING_UNAVAILABLE", "Routing service temporarily unavailable. Please retry later."},
		{"client", infraerrors.BadRequest("MODEL_REQUIRED", "model is required"), 400, "invalid_request_error", "MODEL_REQUIRED", "model is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zap.ErrorLevel)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			ctx := logger.IntoContext(context.Background(), zap.New(core).With(zap.String("request_id", "route-proof")))
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
			writeCompositeRouteError(c, tc.err)
			require.Equal(t, tc.status, recorder.Code)
			require.JSONEq(t, fmt.Sprintf(`{"error":{"type":%q,"code":%q,"message":%q}}`, tc.kind, tc.code, tc.message), recorder.Body.String())
			if tc.status >= 500 {
				require.Len(t, logs.All(), 1)
				require.Equal(t, tc.err.Error(), logs.All()[0].ContextMap()["error"])
				require.Equal(t, "route-proof", logs.All()[0].ContextMap()["request_id"])
			} else {
				require.Zero(t, logs.Len())
			}
		})
	}
}
