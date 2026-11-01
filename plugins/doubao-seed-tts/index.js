// 豆包 Seed-TTS 2.0 与 Seed-ASR 极速版接口。
//
// 入站报文按 OpenAI audio.speech / audio.transcriptions 的形状。TTS 上游是 HTTP
// 单向流，响应逐行 JSON（NDJSON）；ASR 上游是一次 JSON 请求，音频以 base64 放在
// audio.data 里。这里只负责形状转换；地址、凭据、选号与记账仍由 lax 宿主负责。

const DEFAULT_VOICE = "zh_female_xiaohe_uranus_bigtts";
const DEFAULT_RESOURCE_ID = "seed-tts-2.0";
const DEFAULT_TRANSCRIPTION_RESOURCE_ID = "volc.seedasr.auc";
const STREAM_DONE_CODE = 20000000;
const B64_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

function option(ctx, name, fallback) {
  const value = ctx.options ? ctx.options[name] : undefined;
  return typeof value === "string" && value.trim() !== "" ? value : fallback;
}

function audioFormat(format) {
  if (format === "wav" || format === "pcm") {
    return format;
  }
  return "mp3";
}

// OpenAI 的 speed 是倍率，豆包的 speech_rate 是相对 1.0 的百分比。
function speechRate(speed) {
  if (speed === null || speed === undefined) {
    return null;
  }
  const value = Number(speed);
  if (!Number.isFinite(value) || value <= 0) {
    return null;
  }
  return Math.max(-50, Math.min(100, Math.round((value - 1) * 100)));
}

function mimeFor(format) {
  if (format === "wav") {
    return "audio/wav";
  }
  if (format === "pcm") {
    return "audio/pcm";
  }
  return "audio/mpeg";
}

// OpenAI 的 instructions 是风格指令。豆包 v3 接口把这类辅助信息放在
// req_params.additions.context_texts 里，而且 additions 本身要求是 JSON 字符串，
// 不是对象。只取第一条：上游文档说明当前只使用列表的第一个值。
function styleAdditions(instructions) {
  const text = String(instructions || "").trim();
  if (text === "") {
    return null;
  }
  return JSON.stringify({context_texts: [text]});
}

// OpenAI 只放行 auto、zh、en；豆包要求带地区的语言名。空串表示让上游自动判断。
function transcriptionLanguage(language) {
  switch (String(language || "").trim().toLowerCase()) {
    case "zh":
      return "zh-CN";
    case "en":
      return "en-US";
    default:
      return "";
  }
}

// 把多段独立 base64 拼成一段音频的 base64。不能直接拼字符串：每段都有
// 自己的补位，拼起来会在段之间多出几位 0。这里按字节重编，段边界不影响结果。
function joinBase64(parts) {
  const out = [];
  const pending = [];
  let chars = "";

  function emitByte(byte) {
    pending.push(byte);
    if (pending.length < 3) {
      return;
    }
    const n = (pending[0] << 16) | (pending[1] << 8) | pending[2];
    chars += B64_ALPHABET[(n >> 18) & 63];
    chars += B64_ALPHABET[(n >> 12) & 63];
    chars += B64_ALPHABET[(n >> 6) & 63];
    chars += B64_ALPHABET[n & 63];
    if (chars.length >= 8192) {
      out.push(chars);
      chars = "";
    }
    pending.length = 0;
  }

  for (const part of parts) {
    let acc = 0;
    let bits = 0;
    for (const ch of String(part).replace(/\s+/g, "")) {
      if (ch === "=") {
        break;
      }
      const value = B64_ALPHABET.indexOf(ch);
      if (value < 0) {
        throw lax.error("upstream_protocol_error", "豆包返回的音频片段不是合法 base64");
      }
      acc = (acc << 6) | value;
      bits += 6;
      if (bits >= 8) {
        bits -= 8;
        emitByte((acc >> bits) & 255);
        acc &= (1 << bits) - 1;
      }
    }
  }

  if (pending.length === 1) {
    chars += B64_ALPHABET[pending[0] >> 2];
    chars += B64_ALPHABET[(pending[0] & 3) << 4];
    chars += "==";
  } else if (pending.length === 2) {
    chars += B64_ALPHABET[pending[0] >> 2];
    chars += B64_ALPHABET[((pending[0] & 3) << 4) | (pending[1] >> 4)];
    chars += B64_ALPHABET[(pending[1] & 15) << 2];
    chars += "=";
  }
  out.push(chars);
  return out.join("");
}

function prepareTTS(ctx) {
  const voice = (ctx.input.voice || "").trim() || option(ctx, "default_voice", DEFAULT_VOICE);
  const format = audioFormat(ctx.input.format);
  const audioParams = { format, sample_rate: 24000 };
  const rate = speechRate(ctx.input.speed);
  if (rate !== null) {
    audioParams.speech_rate = rate;
  }

  const reqParams = {
    text: ctx.input.text,
    speaker: voice,
    audio_params: audioParams
  };
  const additions = styleAdditions(ctx.input.instructions);
  if (additions !== null) {
    reqParams.additions = additions;
  }

  return {
    headers: {
      "X-Api-Resource-Id": option(ctx, "resource_id", DEFAULT_RESOURCE_ID),
      "X-Api-Request-Id": lax.randomUUID(),
      "X-Control-Require-Usage-Tokens-Return": "text_words"
    },
    body: {
      kind: "json",
      value: {
        user: {uid: option(ctx, "uid", "lax")},
        req_params: reqParams
      }
    }
  };
}

function prepareTranscription(ctx) {
  const audio = String(ctx.input.audio_base64 || "");
  if (ctx.input.audio_truncated || audio === "") {
    throw lax.error("invalid_input",
      "音频没有完整交给插件：沙箱只内联 8 MiB 以内的音频，先把文件压到 8 MiB 以下再发");
  }
  const format = String(ctx.input.audio_container || "");
  if (format !== "wav" && format !== "mp3") {
    throw lax.error("invalid_input",
      "豆包极速识别只接收 wav 或 mp3 音频，收到的是 " + (format || "未知格式"));
  }

  const audioParams = {format: format, data: audio};
  const language = transcriptionLanguage(ctx.input.language);
  if (language !== "") {
    audioParams.language = language;
  }

  return {
    headers: {
      "X-Api-Resource-Id": option(ctx, "transcription_resource_id", DEFAULT_TRANSCRIPTION_RESOURCE_ID),
      "X-Api-Request-Id": lax.randomUUID()
    },
    body: {
      kind: "json",
      value: {
        user: {uid: option(ctx, "uid", "lax")},
        audio: audioParams,
        request: {
          model_name: "bigmodel",
          enable_itn: true,
          enable_punc: true,
          show_utterances: true
        }
      }
    }
  };
}

export async function prepare_request(ctx) {
  if (ctx.operation === "speech.transcribe@1") {
    return prepareTranscription(ctx);
  }
  return prepareTTS(ctx);
}

function decodeTTS(ctx) {
  const lines = String(ctx.response.text || "").split(/\r?\n/);
  const fragments = [];
  let textWords = 0;
  let done = false;

  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i].trim();
    if (line === "") {
      continue;
    }
    let frame;
    try {
      frame = JSON.parse(line);
    } catch (err) {
      throw lax.error("upstream_protocol_error",
        "豆包返回了不是 JSON 的第 " + (i + 1) + " 行：" + line.slice(0, 200));
    }

    const code = Number(frame.code || 0);
    if (code !== 0 && code !== STREAM_DONE_CODE) {
      const message = frame.message || "没有给出原因";
      throw lax.error("upstream_protocol_error",
        "豆包语音合成失败：code=" + code + "，" + message);
    }
    if (typeof frame.data === "string" && frame.data !== "") {
      fragments.push(frame.data);
    }
    if (frame.usage && Number(frame.usage.text_words) > 0) {
      textWords = Number(frame.usage.text_words);
    }
    if (code === STREAM_DONE_CODE) {
      done = true;
    }
  }

  if (!done) {
    throw lax.error("upstream_protocol_error",
      "豆包语音合成的响应在结束帧之前断了：没有收到 code=20000000，音频不完整");
  }
  if (fragments.length === 0) {
    throw lax.error("upstream_protocol_error",
      "豆包返回了结束帧，但没有任何音频数据");
  }

  const usage = textWords > 0
    ? {tts_characters: textWords, source: "upstream"}
    : {tts_characters: ctx.input.chars, source: "estimated"};
  return {
    result: {
      kind: "media",
      mime: mimeFor(ctx.input.format),
      body_base64: joinBase64(fragments)
    },
    usage
  };
}

function upstreamError(frame, status) {
  const header = frame && typeof frame.header === "object" && frame.header !== null
    ? frame.header
    : null;
  const code = header ? Number(header.code || 0) : 0;
  if (status >= 200 && status < 300 && code === 0) {
    return null;
  }
  const message = header && header.message
    ? header.message
    : (frame && frame.message) || "没有给出原因";
  return "豆包语音识别失败：HTTP " + status + "，code=" + code + "，" + message;
}

function transcriptionDurationMS(frame) {
  const info = frame.audio_info && typeof frame.audio_info === "object" ? frame.audio_info : null;
  const additions = frame.result && frame.result.additions &&
    typeof frame.result.additions === "object" ? frame.result.additions : null;
  const fromInfo = info ? Number(info.duration || 0) : 0;
  const fromAdditions = additions ? Number(additions.duration || 0) : 0;
  return fromInfo > 0 ? fromInfo : fromAdditions;
}

function decodeTranscription(ctx) {
  const status = Number(ctx.response.status || 0);
  let frame = ctx.response.body;
  if (typeof frame !== "object" || frame === null) {
    const text = String(ctx.response.text || "").trim();
    try {
      frame = text === "" ? {} : JSON.parse(text);
    } catch (err) {
      throw lax.error("upstream_protocol_error",
        "豆包语音识别返回的不是 JSON：" + text.slice(0, 200));
    }
  }

  const failure = upstreamError(frame, status);
  if (failure !== null) {
    throw lax.error("upstream_protocol_error", failure);
  }
  if (!frame.result || typeof frame.result !== "object") {
    throw lax.error("upstream_protocol_error",
      "豆包语音识别没有返回 result：响应形状是 " + JSON.stringify(frame).slice(0, 200));
  }

  const text = typeof frame.result.text === "string" ? frame.result.text : "";
  const durationMS = transcriptionDurationMS(frame);
  const seconds = durationMS / 1000;
  const usage = durationMS > 0
    ? {audio_seconds: Math.round(seconds), source: "upstream"}
    : {};

  if (ctx.input.format === "text") {
    return {result: {kind: "text", text: text}, usage: usage};
  }
  if (ctx.input.format === "verbose_json") {
    const utterances = Array.isArray(frame.result.utterances) ? frame.result.utterances : [];
    const segments = [];
    for (let i = 0; i < utterances.length; i += 1) {
      const item = utterances[i];
      if (!item || typeof item !== "object") {
        continue;
      }
      segments.push({
        id: segments.length,
        start: Number(item.start_time || 0) / 1000,
        end: Number(item.end_time || 0) / 1000,
        text: typeof item.text === "string" ? item.text : ""
      });
    }
    const language = String(ctx.input.language || "").trim() || "auto";
    return {
      result: {
        kind: "json",
        json: {
          task: "transcribe",
          language: language,
          duration: seconds,
          text: text,
          segments: segments
        }
      },
      usage: usage
    };
  }
  return {
    result: {kind: "json", json: {text: text}},
    usage: usage
  };
}

export async function decode_response(ctx) {
  if (ctx.operation === "speech.transcribe@1") {
    return decodeTranscription(ctx);
  }
  return decodeTTS(ctx);
}
