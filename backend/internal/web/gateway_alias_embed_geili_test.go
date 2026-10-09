//go:build embed

package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The real embedded middleware used to return HTML (or an empty 200) before
// the registered gateway alias could authenticate or forward the Python call.
func TestGeiliEmbeddedRootChatAliasReachesAuthenticationAndGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ServeEmbeddedFrontend())
	var calls int
	router.POST("/chat/completions", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer synthetic-key" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"type": "authentication_error"}})
			return
		}
		calls++
		c.JSON(http.StatusOK, gin.H{"object": "chat.completion", "choices": []gin.H{{"finish_reason": "stop"}}})
	})
	for _, authorized := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(`{"model":"gpt-6-astra","messages":[{"role":"user","content":"synthetic"}]}`))
		request.Header.Set("Content-Type", "application/json")
		if authorized {
			request.Header.Set("Authorization", "Bearer synthetic-key")
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if authorized {
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Body.String(), `"finish_reason":"stop"`)
		} else {
			require.Equal(t, http.StatusUnauthorized, recorder.Code)
			require.Contains(t, recorder.Body.String(), "authentication_error")
		}
		require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")
	}
	require.Equal(t, 1, calls)
}
