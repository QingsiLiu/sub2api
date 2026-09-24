package service

import (
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// NormalizeModelPlazaConfig validates and canonicalizes the presentation-only
// model selection stored on a group.
func NormalizeModelPlazaConfig(cfg GroupModelPlazaConfig) (GroupModelPlazaConfig, error) {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = GroupModelPlazaModeAll
	}
	if mode != GroupModelPlazaModeAll && mode != GroupModelPlazaModeSelected {
		return GroupModelPlazaConfig{}, infraerrors.New(http.StatusBadRequest, "INVALID_MODEL_PLAZA_CONFIG", "model plaza mode must be all or selected")
	}

	seen := make(map[string]struct{}, len(cfg.Models))
	models := make([]string, 0, len(cfg.Models))
	for _, model := range cfg.Models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		models = append(models, model)
	}
	if mode == GroupModelPlazaModeSelected && len(models) == 0 {
		return GroupModelPlazaConfig{}, infraerrors.New(http.StatusBadRequest, "INVALID_MODEL_PLAZA_CONFIG", "selected model plaza mode requires at least one model")
	}
	if mode == GroupModelPlazaModeAll {
		models = nil
	}
	return GroupModelPlazaConfig{Mode: mode, Models: models}, nil
}

// FilterModelPlazaModels filters a discovered model list according to
// presentation settings while preserving discovery order.
func FilterModelPlazaModels(c GroupModelPlazaConfig, source []string) []string {
	if strings.ToLower(strings.TrimSpace(c.Mode)) != GroupModelPlazaModeSelected {
		return source
	}
	allowed := make(map[string]struct{}, len(c.Models))
	for _, model := range c.Models {
		allowed[strings.ToLower(strings.TrimSpace(model))] = struct{}{}
	}
	out := make([]string, 0, len(source))
	for _, model := range source {
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(model))]; ok {
			out = append(out, model)
		}
	}
	return out
}
