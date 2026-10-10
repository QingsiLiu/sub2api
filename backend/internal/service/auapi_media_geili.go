package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// Prices are explicit AUAPI retail snapshots, keyed by model and exact spec.
// Unknown specs never inherit a cheaper/default tariff.
func auapiConfiguredMediaPrice(account *Account, request auapiImageRequest, tier string) (float64, error) {
	var prices map[string]map[string]float64
	if err := json.Unmarshal([]byte(account.GetCredential("auapi_media_prices")), &prices); err != nil {
		return 0, errors.New("configure AUAPI model/spec retail prices")
	}
	spec := strings.ToLower(tier)
	if request.Kind == "video" {
		if request.Parameters.GenerateAudio == nil {
			return 0, errors.New("generate_audio must be explicit")
		}
		spec += ":no_video_input:audio_" + strconv.FormatBool(*request.Parameters.GenerateAudio)
	} else if request.Parameters.Quality != "" && request.Parameters.Quality != "auto" {
		spec += ":" + request.Parameters.Quality
	}
	price, ok := prices[request.Model][spec]
	if !ok || price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return 0, errors.New("AUAPI model/spec price is not configured")
	}
	if request.Kind == "video" {
		price *= float64(request.Parameters.DurationSeconds)
	}
	return price, nil
}

func supportedAUAPIMediaModel(kind, model string) bool {
	if kind == "video" {
		return model == "wan3.0-video" || model == "seedance-2.5" || model == "dreamina-seedance-2-5-260628" || model == "MiniMax-H3" || model == "kling-3.0"
	}
	return model == "gpt-image-2" || model == "gpt-image-2.5" || model == "gemini-3-pro-image-preview"
}

func buildAUAPIVideoPayload(body []byte) (auapiImageRequest, string, error) {
	var request auapiImageRequest
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return request, "", errors.New("invalid video JSON request")
	}
	allowed := map[string]bool{"model": true, "prompt": true, "resolution": true, "duration": true, "seconds": true, "aspect_ratio": true, "generate_audio": true}
	for field := range fields {
		if !allowed[field] {
			return request, "", fmt.Errorf("AUAPI video parameter %s is not supported", field)
		}
	}
	var in struct {
		Model      string `json:"model"`
		Prompt     string `json:"prompt"`
		Resolution string `json:"resolution"`
		Duration   int    `json:"duration"`
		Seconds    int    `json:"seconds"`
		Ratio      string `json:"aspect_ratio"`
		Audio      *bool  `json:"generate_audio"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return request, "", errors.New("invalid video parameter type")
	}
	if strings.TrimSpace(in.Model) == "" || strings.TrimSpace(in.Prompt) == "" {
		return request, "", errors.New("model and prompt are required")
	}
	if in.Duration != 0 && in.Seconds != 0 {
		return request, "", errors.New("specify duration or seconds, not both")
	}
	if in.Duration == 0 {
		in.Duration = in.Seconds
	}
	if in.Duration < 1 || in.Duration > 15 {
		return request, "", errors.New("explicit duration between 1 and 15 seconds is required")
	}
	switch strings.ToLower(in.Resolution) {
	case "480p", "720p", "768p", "1080p", "2k", "4k":
	default:
		return request, "", errors.New("explicit supported resolution is required")
	}
	if in.Audio == nil {
		return request, "", errors.New("generate_audio must be explicit")
	}
	switch in.Ratio {
	case "", "16:9", "9:16", "1:1":
	default:
		return request, "", errors.New("unsupported aspect_ratio")
	}
	request.Kind, request.Action, request.Model, request.Input.Prompt = "video", "generate", in.Model, in.Prompt
	request.Parameters.N, request.Parameters.Resolution, request.Parameters.Ratio = 1, strings.ToLower(in.Resolution), in.Ratio
	request.Parameters.DurationSeconds, request.Parameters.GenerateAudio = in.Duration, in.Audio
	return request, strings.ToLower(in.Resolution), nil
}

func auapiTaskMediaKind(t *AUAPIImageTaskRecord) string {
	if t.Kind == "video" {
		return "video"
	}
	return "image"
}
func auapiTaskImageCount(t *AUAPIImageTaskRecord) int {
	if t.Kind == "video" {
		return 0
	}
	return t.SuccessCount
}
func detectedAUAPIVideoContentType(data []byte) string {
	ct := strings.Split(http.DetectContentType(data), ";")[0]
	if ct == "video/mp4" || ct == "video/webm" {
		return ct
	}
	return ""
}
func auapiMediaStorageKey(u *ImageResultUploader, taskID string, index int, ct string) string {
	if ct == "video/mp4" {
		return u.prefix + taskID + "-" + strconv.Itoa(index) + ".mp4"
	}
	if ct == "video/webm" {
		return u.prefix + taskID + "-" + strconv.Itoa(index) + ".webm"
	}
	return u.buildKey(taskID, index, ct)
}
