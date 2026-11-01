# lax-plugins

[lax](https://github.com/sumwai/lax) 的插件包集合。每个插件都是一个可直接安装的本地目录，包含 manifest、方言、模型目录、运行时入口与测试。

## 插件

| 目录 | 插件 ID | 版本 | 运行时 | 提供的能力 |
| --- | --- | --- | --- | --- |
| `plugins/doubao-seed-tts` | `com.lax.doubao-seed` | 1.2.0 | `moejs` | 豆包 Seed-TTS 2.0 语音合成、Seed-ASR 极速版语音转写 |

字段映射、音色、格式与已知边界见各插件自己的 `README.md`。

## 安装

先安装带 GitHub 插件来源支持的 lax，然后直接从本仓库安装：

```bash
lax init                                      # 首次使用 lax 时
lax plugin install https://github.com/sumwai/lax-plugins/tree/master/plugins/doubao-seed-tts \
    --trust sandboxed --yes
```

`runtime` 是 `moejs` 的插件必须用 `--trust sandboxed`。安装前可以先看计划，不复制文件也不写库：

```bash
lax plugin install https://github.com/sumwai/lax-plugins/tree/master/plugins/doubao-seed-tts \
    --trust sandboxed --dry-run
```

也可以写短形式 `github.com/sumwai/lax-plugins/plugins/doubao-seed-tts`，或用 `@<ref>` 指定分支、tag、提交号。老版本 lax 还不认识 GitHub 来源时，先克隆本仓库，再把本地目录交给安装命令：

```bash
git clone https://github.com/sumwai/lax-plugins.git
lax plugin install ./lax-plugins/plugins/doubao-seed-tts --trust sandboxed --yes
```

如果 `lax serve` 正在运行，装完或升级后执行：

```bash
lax plugin reload
```

## 豆包语音

插件同时提供 TTS 与 ASR。凭据只通过账号命令写入，不放进插件目录：

```bash
printf '%s' "$DOUBAO_API_KEY" \
    | lax account add doubao --provider doubao-seed --models seed-tts-2.0,bigmodel --key -
```

`--key -` 从标准输入读取，不会进入 shell 历史。两个上游模型名都要在账号上放行：TTS 用 `seed-tts-2.0`，ASR 用 `bigmodel`。

```bash
lax model speak --model doubao-seed-tts --voice zh_female_xiaohe_uranus_bigtts \
    --format mp3 --text '你好' --out hello.mp3
lax model transcribe hello.mp3 --model doubao-seed-asr --language zh \
    --format verbose_json
```

客户端模型名是 `doubao-seed-tts` 与 `doubao-seed-asr`，入站端点分别是 `/v1/audio/speech` 与 `/v1/audio/transcriptions`。两条都只做非流式。

## 校验

安装后检查包完整性、manifest 哈希、沙箱钩子与方言登记：

```bash
lax plugin doctor
lax plugin verify --all
lax plugin ls
```

## 目录结构

```text
plugins/<插件名>/
  lax.plugin.json   # 插件 ID、版本、运行时、厂商、路由与权限
  dialects.json     # 方言声明
  models.json       # 模型目录与操作绑定
  index.js          # runtime: "moejs" 时的入口
  plugin_test.go    # 在 lax 仓库内运行的单元测试
  README.md         # 协议映射、用法与边界
```

## 开发

插件版本以 `lax.plugin.json` 的 `version` 为准。同一版本不能覆盖，包内任何文件改动后都要升版本。仓库不另设全局版本号。

`make check` 会取当前 lax（默认从 GitHub 浅克隆，也可以指向本地检出），把插件复制到 lax 的临时目录跑单元测试，再在临时数据目录里安装并运行 `plugin doctor` 与 `plugin verify --all`。需要 Go 工具链与网络：

```bash
make check
LAX_DIR=../lax make check
```

## 安全

插件默认在 `moejs` 沙箱中运行。`doubao-seed-tts` 不申请网络、文件、进程或环境变量权限；上游请求、凭据注入、账号选择与用量记账都由 lax 宿主完成。
