package service

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// geiliPluginResponseNormalizedKeys 只列出需要把响应还原成普通 OpenAI 形态的插件。
// Excel Transport 会带回 *-excel 模型名和写缓存用量（启用前 OAuth 号从无写缓存）；
// 用户和下游 Sub2API 必须看不到，写缓存按普通输入计费。
var geiliPluginResponseNormalizedKeys = map[string]struct{}{
	"local.sub2api.excel-transport": {},
}

const geiliPluginResponseBufferLimit = 64 << 20

var (
	geiliPluginUsagePaths = []string{"usage", "response.usage", "data.usage", "data.response.usage"}
	// 嵌套字段存在时归零：核心和下游都优先读嵌套字段，0 会阻止它们回退到顶层别名。
	geiliPluginCacheWriteNestedFields = []string{
		"input_tokens_details.cache_write_tokens",
		"prompt_tokens_details.cache_write_tokens",
		"input_tokens_details.cache_creation_tokens",
		"prompt_tokens_details.cache_creation_tokens",
	}
	geiliPluginCacheWriteTopLevelFields = []string{
		"cache_write_tokens",
		"cache_creation_input_tokens",
		"cache_write_input_tokens",
		"cache_creation_tokens",
	}
)

func geiliPluginNormalizesResponse(installation *PluginInstallation) bool {
	if installation == nil {
		return false
	}
	_, ok := geiliPluginResponseNormalizedKeys[strings.TrimSpace(installation.PluginKey)]
	return ok
}

// normalizeGeiliPluginOpenAIResponse 把命中插件的响应改成与普通账号一致：
// 模型名还原为实际发出的模型，写缓存 token 并回普通输入（input_tokens 本身已包含它们）。
// 这样用量记录、用户端展示和下游 Sub2API 看到的都是普通响应，计费按普通输入价走。
func normalizeGeiliPluginOpenAIResponse(installation *PluginInstallation, request *http.Request, response *http.Response) *http.Response {
	if response == nil || response.Body == nil || !geiliPluginNormalizesResponse(installation) {
		return response
	}
	if request == nil || request.Method != http.MethodPost || response.StatusCode < 200 || response.StatusCode > 299 {
		return response
	}
	if encoding := strings.TrimSpace(response.Header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return response
	}
	normalizer := &geiliPluginResponseNormalizer{
		pluginKey: installation.PluginKey,
		sentModel: geiliPluginRequestModel(request),
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/event-stream") {
		response.Body = &geiliPluginSSEBody{source: response.Body, reader: bufio.NewReader(response.Body), normalizer: normalizer}
	} else {
		response.Body = &geiliPluginBufferedBody{source: response.Body, normalizer: normalizer}
	}
	response.ContentLength = -1
	response.Header.Del("Content-Length")
	return response
}

func geiliPluginRequestModel(request *http.Request) string {
	if request.GetBody == nil {
		return ""
	}
	body, err := request.GetBody()
	if err != nil {
		return ""
	}
	defer func() { _ = body.Close() }()
	raw, err := io.ReadAll(body)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(gjson.GetBytes(raw, "model").String())
}

type geiliPluginResponseNormalizer struct {
	pluginKey         string
	sentModel         string
	rawModel          string
	cacheWriteRemoved int64
	logged            bool
}

// normalizeJSON 只改协议里的 model 与 usage 字段，正文和工具载荷保持原样。
func (n *geiliPluginResponseNormalizer) normalizeJSON(payload string) string {
	updated := payload
	if n.sentModel != "" {
		for _, path := range []string{"model", "response.model"} {
			value := gjson.Get(updated, path)
			if value.Type != gjson.String || value.String() == n.sentModel {
				continue
			}
			if next, err := sjson.Set(updated, path, n.sentModel); err == nil {
				n.rawModel = value.String()
				updated = next
			}
		}
	}
	for _, usagePath := range geiliPluginUsagePaths {
		if !gjson.Get(updated, usagePath).IsObject() {
			continue
		}
		for _, field := range geiliPluginCacheWriteNestedFields {
			path := usagePath + "." + field
			value := gjson.Get(updated, path)
			if !value.Exists() || value.Int() == 0 {
				continue
			}
			if next, err := sjson.Set(updated, path, 0); err == nil {
				n.recordCacheWrite(value.Int())
				updated = next
			}
		}
		for _, field := range geiliPluginCacheWriteTopLevelFields {
			path := usagePath + "." + field
			value := gjson.Get(updated, path)
			if !value.Exists() {
				continue
			}
			if next, err := sjson.Delete(updated, path); err == nil {
				n.recordCacheWrite(value.Int())
				updated = next
			}
		}
	}
	return updated
}

func (n *geiliPluginResponseNormalizer) recordCacheWrite(tokens int64) {
	if tokens > n.cacheWriteRemoved {
		n.cacheWriteRemoved = tokens
	}
}

func (n *geiliPluginResponseNormalizer) normalizeSSELine(line string) string {
	body := strings.TrimRight(line, "\r\n")
	ending := line[len(body):]
	data, ok := extractOpenAISSEDataLine(body)
	if !ok || !strings.Contains(data, "model") && !strings.Contains(data, "cache_") {
		return line
	}
	if !gjson.Valid(data) {
		return line
	}
	updated := n.normalizeJSON(data)
	if updated == data {
		return line
	}
	return "data: " + updated + ending
}

// logOnce 保留原始值供管理员排查；对外响应和用量记录只看到还原后的普通形态。
func (n *geiliPluginResponseNormalizer) logOnce() {
	if n.logged || (n.rawModel == "" && n.cacheWriteRemoved == 0) {
		return
	}
	n.logged = true
	slog.Info("plugin_openai_response_normalized",
		"plugin_key", n.pluginKey,
		"sent_model", n.sentModel,
		"raw_model", n.rawModel,
		"cache_write_tokens", n.cacheWriteRemoved,
	)
}

type geiliPluginSSEBody struct {
	source     io.ReadCloser
	reader     *bufio.Reader
	normalizer *geiliPluginResponseNormalizer
	pending    []byte
	err        error
}

func (b *geiliPluginSSEBody) Read(p []byte) (int, error) {
	for len(b.pending) == 0 {
		if b.err != nil {
			b.normalizer.logOnce()
			return 0, b.err
		}
		line, err := b.reader.ReadString('\n')
		if line != "" {
			b.pending = []byte(b.normalizer.normalizeSSELine(line))
		}
		b.err = err
	}
	n := copy(p, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}

func (b *geiliPluginSSEBody) Close() error {
	b.normalizer.logOnce()
	return b.source.Close()
}

// geiliPluginBufferedBody 处理非流式 JSON 以及无 event-stream 头的 SSE 文本。
// 超过上限的响应原样透传，避免为了改写字段把大体积图片响应整体放进内存。
type geiliPluginBufferedBody struct {
	source     io.ReadCloser
	normalizer *geiliPluginResponseNormalizer
	reader     io.Reader
	readErr    error
}

func (b *geiliPluginBufferedBody) Read(p []byte) (int, error) {
	if b.reader == nil {
		b.prepare()
	}
	if b.reader == nil {
		return 0, b.readErr
	}
	return b.reader.Read(p)
}

func (b *geiliPluginBufferedBody) prepare() {
	raw, err := io.ReadAll(io.LimitReader(b.source, geiliPluginResponseBufferLimit+1))
	if err != nil && !errors.Is(err, io.EOF) {
		b.reader = io.MultiReader(bytes.NewReader(raw), geiliPluginErrReader{err: err})
		return
	}
	if len(raw) > geiliPluginResponseBufferLimit {
		b.reader = io.MultiReader(bytes.NewReader(raw), b.source)
		return
	}
	b.reader = bytes.NewReader(b.normalizer.normalizeBuffered(raw))
	b.normalizer.logOnce()
}

func (b *geiliPluginBufferedBody) Close() error {
	return b.source.Close()
}

func (n *geiliPluginResponseNormalizer) normalizeBuffered(raw []byte) []byte {
	if gjson.ValidBytes(raw) {
		return []byte(n.normalizeJSON(string(raw)))
	}
	text := string(raw)
	if !strings.Contains(text, "data:") {
		return raw
	}
	var builder strings.Builder
	builder.Grow(len(text))
	for len(text) > 0 {
		index := strings.IndexByte(text, '\n')
		line := text
		if index >= 0 {
			line = text[:index+1]
		}
		builder.WriteString(n.normalizeSSELine(line))
		text = text[len(line):]
	}
	return []byte(builder.String())
}

type geiliPluginErrReader struct{ err error }

func (r geiliPluginErrReader) Read([]byte) (int, error) { return 0, r.err }
