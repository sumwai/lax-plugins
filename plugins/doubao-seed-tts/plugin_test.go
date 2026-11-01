package doubao_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"lax/internal/plugin"
)

const (
	doubaoPluginDir    = "."
	doubaoDialectID    = "com.lax.doubao-seed/seed-tts@1"
	doubaoASRDialectID = "com.lax.doubao-seed/seed-asr@1"
)

// openDoubaoPlugin 加载当前插件目录并登记方言。用例直接跑交付目录，
// 避免测试里的副本与真正安装的包各说各话。
func openDoubaoPlugin(t *testing.T) plugin.Dialect {
	t.Helper()
	return openDoubaoDialect(t, doubaoDialectID)
}

func openDoubaoASRPlugin(t *testing.T) plugin.Dialect {
	t.Helper()
	return openDoubaoDialect(t, doubaoASRDialectID)
}

func openDoubaoDialect(t *testing.T, id string) plugin.Dialect {
	t.Helper()
	loaded, err := plugin.LoadDir(doubaoPluginDir)
	if err != nil {
		t.Fatalf("加载豆包插件：%v", err)
	}
	reg := plugin.DefaultRegistry()
	if err := loaded.Register(reg, plugin.SandboxOptions{Trust: plugin.TrustSandboxed}); err != nil {
		t.Fatalf("登记豆包方言：%v", err)
	}
	t.Cleanup(func() { _ = loaded.Close() })
	dialect, ok := reg.Dialect(id)
	if !ok {
		t.Fatalf("注册表里没有方言 %s", id)
	}
	return dialect
}

// requireHookError 与 internal/plugin 的测试 helper 同义：插件目录不 import
// 测试包，只在这里保留这一份最小断言。
func requireHookError(t *testing.T, err error, code, contains string) *plugin.HookError {
	t.Helper()
	var hookErr *plugin.HookError
	if !errors.As(err, &hookErr) {
		t.Fatalf("期望 *plugin.HookError，得到 %T：%v", err, err)
	}
	if hookErr.Code != code {
		t.Fatalf("错误码是 %q，期望 %q（文案：%s）", hookErr.Code, code, hookErr.Message)
	}
	if contains != "" && !strings.Contains(hookErr.Message, contains) {
		t.Fatalf("错误文案 %q 里没有 %q", hookErr.Message, contains)
	}
	return hookErr
}

func doubaoFrame(t *testing.T, value map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("编码豆包响应帧：%v", err)
	}
	return string(encoded) + "\n"
}

func TestDoubaoSeedPrepare(t *testing.T) {
	dialect := openDoubaoPlugin(t)
	speed := 1.5
	plan, err := dialect.Prepare(plugin.Input{
		Operation:    plugin.OpSpeechSynthesize,
		Wire:         plugin.WireSpeech,
		Model:        "seed-tts-2.0",
		Text:         "你好，这是种子语音测试。",
		Format:       "mp3",
		Instructions: "你可以用特别开心的语气说话吗？",
		Speed:        &speed,
	})
	if err != nil {
		t.Fatalf("Prepare：%v", err)
	}

	for header, want := range map[string]string{
		"x-api-resource-id":                     "seed-tts-2.0",
		"x-control-require-usage-tokens-return": "text_words",
	} {
		if got := plan.Headers[header]; got != want {
			t.Fatalf("请求头 %s = %q，期望 %q", header, got, want)
		}
	}
	if plan.Headers["x-api-request-id"] == "" {
		t.Fatal("缺少每次请求生成的 x-api-request-id")
	}
	if _, exists := plan.Headers["x-api-key"]; exists {
		t.Fatal("插件不能自己设置账号凭据头 x-api-key")
	}

	reqParams, ok := plan.JSON["req_params"].(map[string]any)
	if !ok {
		t.Fatalf("req_params 不是对象：%v", plan.JSON["req_params"])
	}
	if reqParams["text"] != "你好，这是种子语音测试。" {
		t.Fatalf("text = %v", reqParams["text"])
	}
	if reqParams["speaker"] != "zh_female_xiaohe_uranus_bigtts" {
		t.Fatalf("默认音色 = %v", reqParams["speaker"])
	}
	audio, ok := reqParams["audio_params"].(map[string]any)
	if !ok {
		t.Fatalf("audio_params 不是对象：%v", reqParams["audio_params"])
	}
	if audio["format"] != "mp3" || audio["sample_rate"] != float64(24000) || audio["speech_rate"] != float64(50) {
		t.Fatalf("audio_params = %v，期望 mp3 / 24000 / 50", audio)
	}
	additions, ok := reqParams["additions"].(string)
	if !ok {
		t.Fatalf("additions = %v，期望 JSON 字符串", reqParams["additions"])
	}
	var style map[string]any
	if err := json.Unmarshal([]byte(additions), &style); err != nil {
		t.Fatalf("additions 不是合法 JSON：%v", err)
	}
	contexts, ok := style["context_texts"].([]any)
	if !ok || len(contexts) != 1 || contexts[0] != "你可以用特别开心的语气说话吗？" {
		t.Fatalf("context_texts = %v", style["context_texts"])
	}
}

func TestDoubaoSeedOmitsEmptyInstructions(t *testing.T) {
	dialect := openDoubaoPlugin(t)
	plan, err := dialect.Prepare(plugin.Input{
		Operation: plugin.OpSpeechSynthesize,
		Wire:      plugin.WireSpeech,
		Model:     "seed-tts-2.0",
		Text:      "没有语气指令",
		Format:    "mp3",
	})
	if err != nil {
		t.Fatalf("Prepare：%v", err)
	}
	reqParams := plan.JSON["req_params"].(map[string]any)
	if _, exists := reqParams["additions"]; exists {
		t.Fatalf("空 instructions 不该产生 additions：%v", reqParams["additions"])
	}
}

func TestDoubaoSeedDecodeJoinsBase64FragmentsAsBytes(t *testing.T) {
	dialect := openDoubaoPlugin(t)
	first := []byte{0x49}
	second := []byte{0x44, 0x33, 0x04, 0x00, 0xff}
	body := doubaoFrame(t, map[string]any{"code": 0, "data": base64.StdEncoding.EncodeToString(first)}) +
		doubaoFrame(t, map[string]any{"code": 0, "data": base64.StdEncoding.EncodeToString(second)}) +
		doubaoFrame(t, map[string]any{"code": 20000000, "message": "OK", "usage": map[string]any{"text_words": 2}})

	result, err := dialect.Decode(plugin.Input{Format: "mp3", Chars: 10}, plugin.Response{
		Status:      200,
		ContentType: "application/x-ndjson",
		Body:        []byte(body),
	})
	if err != nil {
		t.Fatalf("Decode：%v", err)
	}
	want := append(append([]byte{}, first...), second...)
	if !bytes.Equal(result.Media, want) {
		t.Fatalf("音频 = %x，期望 %x", result.Media, want)
	}
	if result.MIME != "audio/mpeg" {
		t.Fatalf("MIME = %q，期望 audio/mpeg", result.MIME)
	}
	if result.Usage.TTSChars != 2 || result.Usage.Source != "upstream" {
		t.Fatalf("用量 = %+v，期望以上游 text_words 为准", result.Usage)
	}
}

func TestDoubaoSeedDecodeErrors(t *testing.T) {
	dialect := openDoubaoPlugin(t)
	tests := []struct {
		name     string
		body     string
		contains string
	}{
		{
			name:     "上游错误码",
			body:     doubaoFrame(t, map[string]any{"code": 45000030, "message": "requested resource not granted"}),
			contains: "code=45000030",
		},
		{
			name:     "缺少结束帧",
			body:     doubaoFrame(t, map[string]any{"code": 0, "data": base64.StdEncoding.EncodeToString([]byte("ID3"))}),
			contains: "结束帧",
		},
		{
			name:     "没有音频",
			body:     doubaoFrame(t, map[string]any{"code": 20000000, "message": "OK"}),
			contains: "没有任何音频",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := dialect.Decode(plugin.Input{Format: "mp3"}, plugin.Response{
				Status:      200,
				ContentType: "application/x-ndjson",
				Body:        []byte(tt.body),
			})
			if err == nil || !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("错误 = %v，期望包含 %q", err, tt.contains)
			}
		})
	}
}

func TestDoubaoSeedASRPrepare(t *testing.T) {
	dialect := openDoubaoASRPlugin(t)
	audio := []byte("RIFF....WAVEfmt ")
	plan, err := dialect.Prepare(plugin.Input{
		Operation:      plugin.OpSpeechTranscribe,
		Wire:           plugin.WireTranscriptions,
		Model:          "bigmodel",
		Format:         "json",
		Language:       "zh",
		Audio:          audio,
		AudioContainer: "wav",
	})
	if err != nil {
		t.Fatalf("Prepare：%v", err)
	}
	if plan.Headers["x-api-resource-id"] != "volc.seedasr.auc" {
		t.Fatalf("资源头 = %q", plan.Headers["x-api-resource-id"])
	}
	if plan.Headers["x-api-request-id"] == "" {
		t.Fatal("缺少每次请求生成的 x-api-request-id")
	}

	audioParams, ok := plan.JSON["audio"].(map[string]any)
	if !ok {
		t.Fatalf("audio 不是对象：%v", plan.JSON["audio"])
	}
	if audioParams["format"] != "wav" || audioParams["language"] != "zh-CN" {
		t.Fatalf("audio = %v，期望 wav / zh-CN", audioParams)
	}
	if audioParams["data"] != base64.StdEncoding.EncodeToString(audio) {
		t.Fatalf("audio.data 不是音频的 base64")
	}
	request, ok := plan.JSON["request"].(map[string]any)
	if !ok {
		t.Fatalf("request 不是对象：%v", plan.JSON["request"])
	}
	if request["model_name"] != "bigmodel" || request["enable_itn"] != true ||
		request["enable_punc"] != true || request["show_utterances"] != true {
		t.Fatalf("request = %v", request)
	}
}

func TestDoubaoSeedASRDecodeFormats(t *testing.T) {
	dialect := openDoubaoASRPlugin(t)
	body := []byte(`{"audio_info":{"duration":2447},"result":{"text":"今天天气真好，我们出去走走吧。","utterances":[{"start_time":120,"end_time":2280,"text":"今天天气真好，我们出去走走吧。"}]}}`)

	t.Run("json", func(t *testing.T) {
		result, err := dialect.Decode(plugin.Input{
			Operation: plugin.OpSpeechTranscribe,
			Format:    "json",
		}, plugin.Response{Status: 200, ContentType: "application/json", Body: body})
		if err != nil {
			t.Fatalf("Decode：%v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(result.JSON, &payload); err != nil {
			t.Fatalf("结果不是 JSON：%v", err)
		}
		if payload["text"] != "今天天气真好，我们出去走走吧。" {
			t.Fatalf("text = %v", payload["text"])
		}
		if result.Usage.AudioSeconds != 2 || result.Usage.Source != "upstream" {
			t.Fatalf("用量 = %+v", result.Usage)
		}
	})

	t.Run("text", func(t *testing.T) {
		result, err := dialect.Decode(plugin.Input{
			Operation: plugin.OpSpeechTranscribe,
			Format:    "text",
		}, plugin.Response{Status: 200, ContentType: "application/json", Body: body})
		if err != nil {
			t.Fatalf("Decode：%v", err)
		}
		if result.Kind != plugin.ResultText || result.Text != "今天天气真好，我们出去走走吧。" {
			t.Fatalf("结果 = %+v", result)
		}
	})

	t.Run("verbose_json", func(t *testing.T) {
		result, err := dialect.Decode(plugin.Input{
			Operation: plugin.OpSpeechTranscribe,
			Format:    "verbose_json",
			Language:  "zh",
		}, plugin.Response{Status: 200, ContentType: "application/json", Body: body})
		if err != nil {
			t.Fatalf("Decode：%v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(result.JSON, &payload); err != nil {
			t.Fatalf("结果不是 JSON：%v", err)
		}
		if payload["task"] != "transcribe" || payload["language"] != "zh" || payload["duration"] != 2.447 {
			t.Fatalf("verbose_json = %v", payload)
		}
		segments, ok := payload["segments"].([]any)
		if !ok || len(segments) != 1 {
			t.Fatalf("segments = %v", payload["segments"])
		}
		segment := segments[0].(map[string]any)
		if segment["start"] != 0.12 || segment["end"] != 2.28 {
			t.Fatalf("segment = %v", segment)
		}
	})
}

func TestDoubaoSeedASRErrors(t *testing.T) {
	dialect := openDoubaoASRPlugin(t)
	tests := []struct {
		name     string
		status   int
		body     string
		contains string
	}{
		{
			name:     "HTTP 错误",
			status:   403,
			body:     `{"header":{"code":45000030,"message":"requested resource not granted"}}`,
			contains: "code=45000030",
		},
		{
			name:     "响应头错误码",
			status:   200,
			body:     `{"header":{"code":55000000,"message":"bad audio"}}`,
			contains: "55000000",
		},
		{
			name:     "没有 result",
			status:   200,
			body:     `{}`,
			contains: "没有返回 result",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := dialect.Decode(plugin.Input{
				Operation: plugin.OpSpeechTranscribe,
				Format:    "json",
			}, plugin.Response{Status: tt.status, ContentType: "application/json", Body: []byte(tt.body)})
			if err == nil || !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("错误 = %v，期望包含 %q", err, tt.contains)
			}
		})
	}
}

func TestDoubaoSeedASRRejectsMissingAudio(t *testing.T) {
	dialect := openDoubaoASRPlugin(t)
	_, err := dialect.Prepare(plugin.Input{
		Operation:      plugin.OpSpeechTranscribe,
		Wire:           plugin.WireTranscriptions,
		Format:         "json",
		AudioContainer: "wav",
	})
	requireHookError(t, err, plugin.ErrorInvalidInput, "8 MiB")
}
