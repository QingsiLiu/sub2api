package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEncryptedRouteSettingsAdminGeiliRoundTripAndOmittedPreservation(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{service.SettingKeySiteName: "unchanged"})
	policy := map[string]any{"mode": "protect", "protected_user_ids": []int64{3885}, "reasoning_recovery_enabled": true}
	rec := doUpdateSettings(t, h, map[string]any{"openai_encrypted_route": policy}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	raw := repo.values[service.SettingKeyOpenAIEncryptedRouteGeili]
	var v service.OpenAIEncryptedRouteSettingsGeili
	require.NoError(t, json.Unmarshal([]byte(raw), &v))
	require.Equal(t, "protect", v.Mode)
	rec = doUpdateSettings(t, h, map[string]any{"site_name": "renamed"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, raw, repo.values[service.SettingKeyOpenAIEncryptedRouteGeili])
	require.NotContains(t, repo.lastUpdates, service.SettingKeyOpenAIEncryptedRouteGeili)
	rec = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	var body struct {
		Data dto.SystemSettings `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.Data.OpenAIEncryptedRoute)
	require.Equal(t, v, *body.Data.OpenAIEncryptedRoute)
}
func TestEncryptedRouteSettingsAdminGeiliRejectsInvalidBeforeWrite(t *testing.T) {
	for name, payload := range map[string]any{"null": nil, "unknownmode": map[string]any{"mode": "bad"}, "negativeuser": map[string]any{"mode": "protect", "protected_user_ids": []int64{-1}}, "duplicateuser": map[string]any{"mode": "protect", "protected_user_ids": []int64{3885, 3885}}} {
		t.Run(name, func(t *testing.T) {
			h, repo := newStepUpSwitchTestHandler(t, map[string]string{service.SettingKeySiteName: "unchanged"})
			rec := doUpdateSettings(t, h, map[string]any{"site_name": "must not write", "openai_encrypted_route": payload}, nil)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Equal(t, "unchanged", repo.values[service.SettingKeySiteName])
			require.NotContains(t, repo.values, service.SettingKeyOpenAIEncryptedRouteGeili)
		})
	}
}
