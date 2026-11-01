# Doubao Seed Voice plugin

Wires two Doubao speech products into lax:

| Client model | Operation | Upstream model | Inbound endpoint |
| --- | --- | --- | --- |
| `doubao-seed-tts` | `speech.synthesize@1` | `seed-tts-2.0` | `POST /v1/audio/speech` |
| `doubao-seed-asr` | `speech.transcribe@1` | `bigmodel` | `POST /v1/audio/transcriptions` |

The plugin only translates request and response shapes. lax still owns account
selection, credentials, routing, timeouts and usage accounting. The package ID
and directory name remain `com.lax.doubao-seed` / `doubao-seed-tts` so existing
installs keep working; the package itself covers both voice directions.

## Install

```bash
lax plugin install https://github.com/sumwai/lax-plugins/tree/master/plugins/doubao-seed-tts \
    --trust sandboxed --yes
printf '%s' "$DOUBAO_API_KEY" \
    | lax account add doubao --provider doubao-seed --models seed-tts-2.0,bigmodel --key -
```

也可以先克隆仓库，把 `./plugins/doubao-seed-tts` 作为本地目录传给 `lax plugin install`。

`--key -` reads the key from standard input, so it does not enter shell history.
Both upstream model names must be allowed on the account: `seed-tts-2.0` for TTS
and `bigmodel` for ASR.

```bash
lax model speak --model doubao-seed-tts --voice zh_female_xiaohe_uranus_bigtts \
    --format mp3 --text '你好' --out hello.mp3
lax model transcribe hello.mp3 --model doubao-seed-asr --language zh \
    --format verbose_json
```

## Text to speech

The upstream endpoint is the HTTP unidirectional stream:

```text
POST https://openspeech.bytedance.com/api/v3/tts/unidirectional
X-Api-Key: <account key>
X-Api-Resource-Id: seed-tts-2.0
```

Request body shape:

```json
{
  "user": {"uid": "lax"},
  "req_params": {
    "text": "你好",
    "speaker": "zh_female_xiaohe_uranus_bigtts",
    "audio_params": {"format": "mp3", "sample_rate": 24000},
    "additions": "{\"context_texts\":[\"请用轻快、开心的语气\"]}"
  }
}
```

The response is newline-delimited JSON. Each `data` field is an independently
base64-encoded audio fragment; the terminal frame has `code: 20000000`. The
plugin decodes every fragment to bytes and re-encodes the concatenated stream,
because concatenating the base64 strings would insert extra zero bits at every
fragment boundary.

Mapping:

| OpenAI field | Doubao field |
| --- | --- |
| `input` | `req_params.text` |
| `voice` (default `zh_female_xiaohe_uranus_bigtts`) | `req_params.speaker` |
| `response_format` `mp3` / `wav` / `pcm` | `req_params.audio_params.format` |
| `speed` multiplier | `req_params.audio_params.speech_rate`, as a percentage offset clamped to `-50..100` |
| `instructions` | `req_params.additions`, a JSON string containing `context_texts` |

`instructions` is the tone control: for example
`"请用特别开心、轻快的语气，语速稍快。"`. The upstream uses only the first item
in `context_texts`, so the plugin sends one instruction per request. Requests
with different instructions produced different PCM payloads in the live test.

The declared voices are the two Seed-TTS 2.0 voices verified against the live
endpoint: `zh_female_xiaohe_uranus_bigtts` (default) and
`zh_female_vv_uranus_bigtts`. Add more with:

```bash
lax model capability add seed-tts-2.0 --provider doubao-seed --voices <voice>
```

## Speech to text

The upstream endpoint is:

```text
POST https://openspeech.bytedance.com/api/v3/auc/bigmodel/recognize/flash
X-Api-Key: <account key>
X-Api-Resource-Id: volc.seedasr.auc
X-Api-Request-Id: <uuid>
Content-Type: application/json
```

Request body shape:

```json
{
  "user": {"uid": "lax"},
  "audio": {
    "format": "wav",
    "language": "zh-CN",
    "data": "<base64 audio>"
  },
  "request": {
    "model_name": "bigmodel",
    "enable_itn": true,
    "enable_punc": true,
    "show_utterances": true
  }
}
```

The audio bytes from the inbound multipart `file` part are base64-encoded into
`audio.data`. `language` must be on `audio`, not at the top level.

| Inbound value | Doubao value |
| --- | --- |
| `language: zh` | `audio.language: zh-CN` |
| `language: en` | `audio.language: en-US` |
| `language: auto` or absent | `audio.language` omitted, upstream auto-detects |
| `response_format: json` | `{"text":"…"}` |
| `response_format: text` | plain text, `text/plain; charset=utf-8` |
| `response_format: verbose_json` | `task`, `language`, `duration`, `text`, `segments` |

`segments` comes from the upstream `result.utterances` timestamps. `duration`
comes from `audio_info.duration` and falls back to `result.additions.duration`;
the value is milliseconds upstream and seconds in the OpenAI shape. A 200
response with no `result` is treated as an upstream protocol error rather than
an empty transcript.

Example through lax:

```bash
curl -s http://127.0.0.1:6543/v1/audio/transcriptions \
    -F model=doubao-seed-asr \
    -F language=zh \
    -F response_format=json \
    -F file=@hello.mp3
```

## Limits

- Both directions are non-streaming. `stream_format: "sse"` and ASR
  `stream: true` are rejected locally by lax.
- No audio transcoding: TTS accepts `mp3`, `wav` and `pcm`; ASR accepts only
  `wav` and `mp3`. Ogg/opus and SILK must be converted before sending.
- ASR audio is inlined into the sandbox request as base64. The sandbox limit is
  8 MiB of injected audio, so larger files must be compressed or split first.
- ASR accepts `json`, `text` and `verbose_json`; `srt` and `vtt` are rejected
  locally.
- ASR `prompt` and `temperature` are parsed by lax but not forwarded, because
  this upstream shape has no corresponding fields.
- Resource IDs and voice families must match. The declared Uranus voices require
  `X-Api-Resource-Id: seed-tts-2.0`; `*_moon_bigtts` returns
  `code=55000000 resource ID is mismatched with speaker related resource`.
  `seed-tts-1.0` and `seed-tts-2.0-concurr` returned HTTP 403
  `requested resource not granted` on the test account.
- The gateway buffers the whole upstream response before decoding it; this is
  not chunked delivery to the client.

## Verification

Unit coverage lives in this directory's `plugin_test.go`:

- `TestDoubaoSeedPrepare` and `TestDoubaoSeedOmitsEmptyInstructions` pin the TTS
  body, headers, tone instruction and speed conversion.
- `TestDoubaoSeedDecodeJoinsBase64FragmentsAsBytes` pins byte-correct fragment
  assembly.
- `TestDoubaoSeedASRPrepare`, `TestDoubaoSeedASRDecodeFormats`,
  `TestDoubaoSeedASRErrors` and `TestDoubaoSeedASRRejectsMissingAudio` pin the
  ASR JSON body, all three response formats, upstream business errors and the
  8 MiB gate.
The gateway regression is generic and lives in
`internal/gateway/transcriptions_dialect_test.go`. It uses a fake hook dialect
on the `openai-transcriptions` wire, sends multipart through the gateway to a
fake upstream, and asserts that the upstream receives JSON rather than
multipart. The test contains no vendor or model names: the gateway only needs
to know that a plugin dialect must run before multipart passthrough.

Live checks used the local Volcengine key through stdin and did not put the key
in any command line or fixture. Through `127.0.0.1:6543`:

- TTS returned HTTP 200, `audio/mpeg`, a 24 kHz mono MP3.
- ASR `json` returned `{"text":"今天天气真好，我们出去走走吧。"}`.
- ASR `text` returned the same sentence as `text/plain`.
- ASR `verbose_json` returned `duration: 2.447`, language `zh`, and one segment
  from `0.12` to `2.28` seconds.
- An English WAV with `language=en` returned
  `Hello. This is a speech recognition test.` with duration `2.595` and one
  segment from `0.08` to `2.24` seconds.
- Request records showed `openai-transcriptions → openai-transcriptions`,
  alias `doubao-seed-asr → bigmodel`, account `doubao`, HTTP 200.
