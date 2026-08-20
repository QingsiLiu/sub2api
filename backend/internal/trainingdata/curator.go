package trainingdata

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/klauspost/compress/zstd"
)

const MaxDecodedCaptureBodyBytes int64 = 64 * 1024 * 1024

type TrainingMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type TrainingSample struct {
	SampleID             string            `json:"sample_id"`
	Messages             []TrainingMessage `json:"messages"`
	SourceCaptureID      string            `json:"source_capture_id"`
	ClientModel          string            `json:"client_model,omitempty"`
	AssistantSourceModel string            `json:"assistant_source_model,omitempty"`
	QualityFlags         []string          `json:"quality_flags,omitempty"`
	PrivacyFlags         []string          `json:"privacy_flags,omitempty"`
	RightsRef            string            `json:"rights_ref,omitempty"`
	TransformVersion     string            `json:"transform_version"`
	DatasetType          string            `json:"dataset_type"`
	Split                string            `json:"split"`
	ContentSHA256        string            `json:"content_sha256"`
}

var (
	curatorEmailPattern  = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	curatorPhonePattern  = regexp.MustCompile(`(?:\+?\d[\d\s().-]{8,}\d)`)
	curatorSecretPattern = regexp.MustCompile(`(?i)\b(sk|rk|pk|api[_-]?key|token|secret|password)[-_:=\s]+[A-Za-z0-9._~+\-/]{8,}`)
)

// BuildTrainingSample converts a client-facing request/response pair into a
// role-preserving training example. System/developer/tool turns are excluded
// from the deliverable sample but remain recoverable from the raw vault.
func BuildTrainingSample(manifest Manifest, requestBody, responseBody []byte, datasetType string) (TrainingSample, error) {
	datasetType = normalizeDatasetType(datasetType)
	requestMessages, err := extractRequestMessages(manifest.Protocol, requestBody)
	if err != nil {
		return TrainingSample{}, err
	}
	responseText := extractResponseText(manifest.Protocol, responseBody)
	if len(requestMessages) == 0 || !hasMessageRole(requestMessages, "user") || strings.TrimSpace(responseText) == "" {
		return TrainingSample{}, fmt.Errorf("capture %s has no usable user request and assistant response", manifest.CaptureID)
	}
	privacyFlags := []string{}
	for index := range requestMessages {
		cleaned, flags := redactTrainingText(requestMessages[index].Content)
		requestMessages[index].Content = cleaned
		privacyFlags = append(privacyFlags, flags...)
	}
	cleanedResponse, responseFlags := redactTrainingText(responseText)
	privacyFlags = append(privacyFlags, responseFlags...)
	requestMessages = append(requestMessages, TrainingMessage{Role: "assistant", Content: cleanedResponse})
	privacyFlags = uniqueSortedStrings(privacyFlags)
	qualityFlags := []string{}
	if !manifest.CaptureComplete {
		qualityFlags = append(qualityFlags, "capture_incomplete")
	}
	if manifest.ClientResponse.Status >= 400 {
		qualityFlags = append(qualityFlags, "client_error_response")
	}
	if len(privacyFlags) > 0 {
		qualityFlags = append(qualityFlags, "privacy_redacted")
	}
	if datasetType == "code" && !looksLikeCode(requestMessages) {
		qualityFlags = append(qualityFlags, "code_signal_weak")
	}
	canonical, err := json.Marshal(requestMessages)
	if err != nil {
		return TrainingSample{}, err
	}
	digest := sha256.Sum256(canonical)
	sampleID := "sample_" + strings.ReplaceAll(manifest.CaptureID, "-", "")
	return TrainingSample{
		SampleID: sampleID, Messages: requestMessages, SourceCaptureID: manifest.CaptureID,
		ClientModel: manifest.ClientModel, AssistantSourceModel: firstUpstreamModel(manifest),
		QualityFlags: uniqueSortedStrings(qualityFlags), PrivacyFlags: privacyFlags,
		RightsRef: manifest.RightsID, TransformVersion: TransformVersion, DatasetType: datasetType,
		Split: splitForSubject(manifest.UserSubjectRef), ContentSHA256: hex.EncodeToString(digest[:]),
	}, nil
}

func normalizeDatasetType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "code":
		return "code"
	case "eval":
		return "eval"
	default:
		return "chat"
	}
}

func ValidDatasetType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "chat", "code", "eval":
		return true
	default:
		return false
	}
}

func extractRequestMessages(protocol string, body []byte) ([]TrainingMessage, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("request body is not JSON: %w", err)
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	var raw []any
	if strings.Contains(protocol, "chat") || strings.Contains(protocol, "anthropic") {
		raw, _ = root["messages"].([]any)
	} else if strings.Contains(protocol, "responses") {
		switch input := root["input"].(type) {
		case string:
			if strings.TrimSpace(input) != "" {
				return []TrainingMessage{{Role: "user", Content: input}}, nil
			}
		case []any:
			raw = input
		}
	} else if protocol == "gemini" {
		raw, _ = root["contents"].([]any)
	}
	result := make([]TrainingMessage, 0, len(raw))
	for _, item := range raw {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(stringValue(object["role"])))
		if role == "model" {
			role = "assistant"
		}
		if role != "user" && role != "assistant" {
			continue
		}
		content := extractTextContent(object["content"])
		if protocol == "gemini" {
			content = extractTextContent(object["parts"])
		}
		if strings.TrimSpace(content) != "" {
			result = append(result, TrainingMessage{Role: role, Content: content})
		}
	}
	return result, nil
}

func extractResponseText(protocol string, body []byte) string {
	if text := extractJSONResponseText(protocol, body); strings.TrimSpace(text) != "" {
		return text
	}
	return extractSSEText(protocol, body)
}

func extractJSONResponseText(protocol string, body []byte) string {
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return ""
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if strings.Contains(protocol, "anthropic") {
		return extractTextContent(root["content"])
	}
	if protocol == "gemini" {
		if candidates, ok := root["candidates"].([]any); ok && len(candidates) > 0 {
			if candidate, ok := candidates[0].(map[string]any); ok {
				if content, ok := candidate["content"].(map[string]any); ok {
					return extractTextContent(content["parts"])
				}
			}
		}
	}
	if choices, ok := root["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if message, ok := choice["message"].(map[string]any); ok {
				return extractTextContent(message["content"])
			}
			return extractTextContent(choice["text"])
		}
	}
	if outputText, ok := root["output_text"].(string); ok {
		return outputText
	}
	if output, ok := root["output"].([]any); ok {
		return extractTextContent(output)
	}
	return ""
}

func extractSSEText(protocol string, body []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 8*1024*1024)
	var builder strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var object map[string]any
		if json.Unmarshal([]byte(payload), &object) != nil {
			continue
		}
		protocol = strings.ToLower(strings.TrimSpace(protocol))
		if strings.Contains(protocol, "anthropic") {
			if delta, ok := object["delta"].(map[string]any); ok {
				if text, ok := delta["text"].(string); ok {
					builder.WriteString(text)
				}
			}
		} else if strings.Contains(protocol, "responses") {
			if delta, ok := object["delta"].(string); ok {
				builder.WriteString(delta)
			}
		} else if protocol == "gemini" {
			if candidates, ok := object["candidates"].([]any); ok && len(candidates) > 0 {
				if candidate, ok := candidates[0].(map[string]any); ok {
					if content, ok := candidate["content"].(map[string]any); ok {
						builder.WriteString(extractTextContent(content["parts"]))
					}
				}
			}
		} else if choices, ok := object["choices"].([]any); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]any); ok {
				if delta, ok := choice["delta"].(map[string]any); ok {
					if text, ok := delta["content"].(string); ok {
						builder.WriteString(text)
					}
				}
			}
		}
	}
	return builder.String()
}

func extractTextContent(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, extractTextContent(item))
		}
		return JoinNonEmpty(parts, "\n")
	case map[string]any:
		for _, key := range []string{"text", "content", "input_text", "output_text"} {
			if value, ok := typed[key]; ok {
				if text := extractTextContent(value); strings.TrimSpace(text) != "" {
					return text
				}
			}
		}
	}
	return ""
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func redactTrainingText(value string) (string, []string) {
	flags := []string{}
	cleaned := curatorEmailPattern.ReplaceAllStringFunc(value, func(string) string { flags = append(flags, "email"); return "<EMAIL>" })
	cleaned = curatorPhonePattern.ReplaceAllStringFunc(cleaned, func(string) string { flags = append(flags, "phone"); return "<PHONE>" })
	cleaned = pemPrivateKeyPattern.ReplaceAllStringFunc(cleaned, func(string) string {
		flags = append(flags, "private_key")
		return "<REDACTED_PRIVATE_KEY>"
	})
	cleaned = standaloneCredentialPattern.ReplaceAllStringFunc(cleaned, func(string) string {
		flags = append(flags, "credential_pattern")
		return "<REDACTED_CREDENTIAL>"
	})
	cleaned = curatorSecretPattern.ReplaceAllStringFunc(cleaned, func(match string) string {
		flags = append(flags, "credential_pattern")
		parts := strings.FieldsFunc(match, func(r rune) bool { return r == ':' || r == '=' || r == '-' || r == '_' || r == ' ' })
		if len(parts) > 0 {
			return parts[0] + "=<REDACTED>"
		}
		return "<REDACTED>"
	})
	return cleaned, uniqueSortedStrings(flags)
}

func hasMessageRole(messages []TrainingMessage, role string) bool {
	for _, message := range messages {
		if message.Role == role {
			return true
		}
	}
	return false
}

func looksLikeCode(messages []TrainingMessage) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, "```") || strings.Contains(message.Content, "func ") || strings.Contains(message.Content, "def ") || strings.Contains(message.Content, "import ") {
			return true
		}
	}
	return false
}

func splitForSubject(subjectRef string) string {
	digest := sha256.Sum256([]byte(subjectRef))
	value := int(digest[0])
	switch {
	case value < 13:
		return "validation"
	case value < 26:
		return "test"
	default:
		return "train"
	}
}

func firstUpstreamModel(manifest Manifest) string {
	for index := len(manifest.Attempts) - 1; index >= 0; index-- {
		attempt := manifest.Attempts[index]
		if attempt.Complete && strings.TrimSpace(attempt.Error) == "" &&
			attempt.HTTPStatus >= 200 && attempt.HTTPStatus < 300 && strings.TrimSpace(attempt.Model) != "" {
			return strings.TrimSpace(attempt.Model)
		}
	}
	for _, attempt := range manifest.Attempts {
		if strings.TrimSpace(attempt.Model) != "" {
			return strings.TrimSpace(attempt.Model)
		}
	}
	return ""
}

func DecodeCaptureBody(reader io.Reader, key string) ([]byte, error) {
	if strings.HasSuffix(strings.ToLower(key), ".zst") {
		decoder, err := zstd.NewReader(reader)
		if err != nil {
			return nil, err
		}
		defer decoder.Close()
		return readDecodedCaptureBody(decoder)
	}
	if strings.HasSuffix(strings.ToLower(key), ".gz") {
		decoder, err := gzip.NewReader(reader)
		if err != nil {
			return nil, err
		}
		defer decoder.Close()
		return readDecodedCaptureBody(decoder)
	}
	return readDecodedCaptureBody(reader)
}

func readDecodedCaptureBody(reader io.Reader) ([]byte, error) {
	decoded, err := io.ReadAll(io.LimitReader(reader, MaxDecodedCaptureBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(decoded)) > MaxDecodedCaptureBodyBytes {
		return nil, fmt.Errorf("decoded capture body exceeds %d bytes", MaxDecodedCaptureBodyBytes)
	}
	return decoded, nil
}

func JoinNonEmpty(values []string, separator string) string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return strings.Join(result, separator)
}
