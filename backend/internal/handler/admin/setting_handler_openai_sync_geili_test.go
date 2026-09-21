package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingsOpenAISyncCatalogRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &settingHandlerRepoStub{values: map[string]string{}}
	svc := service.NewSettingService(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	h := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)

	initial := doUpdateSettings(t, h, map[string]any{
		"openai_sync_model_ids": []string{"gpt-6-astra", "gpt-reserve"},
	}, nil)
	require.Equal(t, http.StatusOK, initial.Code)
	require.JSONEq(t, `["gpt-6-astra","gpt-reserve"]`, repo.values[service.SettingKeyOpenAISyncModelIDs])

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, rec.Code)
	var envelope struct {
		Data dto.SystemSettings `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	require.Equal(t, []string{"gpt-6-astra", "gpt-reserve"}, envelope.Data.OpenAISyncModelIDs)
	require.Contains(t, envelope.Data.OpenAISyncModelCandidates, "gpt-reserve")

}

func TestSettingsOpenAISyncCatalogRejectsInvalidPayload(t *testing.T) {
	for name, input := range map[string]any{
		"empty": []string{}, "null": nil, "blank": []string{"gpt-6-astra", " "},
		"duplicate": []string{"gpt-reserve", " gpt-reserve "},
		"unknown":   []string{"unknown-model"}, "other_provider": []string{"claude-sonnet-4-6"},
		"wildcard": []string{"gpt-*"}, "wrong_type": "gpt-reserve",
	} {
		t.Run(name, func(t *testing.T) {
			h, repo := newStepUpSwitchTestHandler(t, map[string]string{service.SettingKeySiteName: "unchanged"})
			rec := doUpdateSettings(t, h, map[string]any{"openai_sync_model_ids": input, "site_name": "must not write"}, nil)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Equal(t, "unchanged", repo.values[service.SettingKeySiteName])
			require.NotContains(t, repo.values, service.SettingKeyOpenAISyncModelIDs)
		})
	}
}

func TestSettingsOpenAISyncCatalogDefaultAndPartialUpdate(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{service.SettingKeySiteName: "unchanged"})
	read := func() dto.SystemSettings {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
		h.GetSettings(c)
		require.Equal(t, http.StatusOK, rec.Code)
		var envelope struct {
			Data dto.SystemSettings `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
		return envelope.Data
	}
	require.Equal(t, service.DefaultOpenAISyncModelIDs(), read().OpenAISyncModelIDs)
	rec := doUpdateSettings(t, h, map[string]any{"site_name": "renamed"}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, repo.lastUpdates, service.SettingKeyOpenAISyncModelIDs)
	rec = doUpdateSettings(t, h, map[string]any{"openai_sync_model_ids": []string{" gpt-reserve ", "gpt-5.5"}}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "renamed", repo.values[service.SettingKeySiteName])
	rec = doUpdateSettings(t, h, map[string]any{"site_name": "renamed again"}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, repo.lastUpdates, service.SettingKeyOpenAISyncModelIDs)
	require.Equal(t, []string{"gpt-reserve", "gpt-5.5"}, read().OpenAISyncModelIDs)
}
