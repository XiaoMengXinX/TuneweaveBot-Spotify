# TuneweaveBot-Spotify

[TuneweaveBot](https://github.com/XiaoMengXinX/TuneweaveBot) 的 Spotify 插件。

用 `sp_dc` cookie 换取 web-player token 访问 Spotify：元数据/搜索走公开 Web API，
歌词走 color-lyrics，音频走 Widevine（track-playback → storage-resolve → 本地构造
PSSH → license → CENC 解密），解密后的 AAC/MP4 经宿主的 `media` 能力落盘上传。

这是**数据面重**的插件：密文由宿主下载到 `/scratch`，插件按分片流式解密，内存占用
与音轨时长无关，明文经 `media.*` 流式交给宿主，详见插件开发文档
[plugins/README.md §9](https://github.com/XiaoMengXinX/TuneweaveBot/blob/master/plugins/README.md)。

## 构建

```sh
bash build-plugins.sh   # 输出 dist/plugins/spotify.wasm.gz 与 version.json
```

构建时把 commit 与构建时间注入插件 manifest（显示在 `/status`），`version.json` 记录版本、
构建时间、commit 和 sha256。产物经 gzip 压缩，TuneweaveBot 直接加载，把 `spotify.wasm.gz`
放进插件目录（`[plugins] dir`，默认 `./plugins`）即可；`[plugins] enabled` 非空时需在其中
加上 `spotify`。

## 发布

推送到 master 后 GitHub Actions 自动测试、构建，并更新固定的
[plugin](https://github.com/XiaoMengXinX/TuneweaveBot-Spotify/releases/tag/plugin) release：

- `spotify.wasm.gz`：https://github.com/XiaoMengXinX/TuneweaveBot-Spotify/releases/download/plugin/spotify.wasm.gz
- `version.json`：`{"version": "...", "build_time": "...", "commit": "...", "sha256": "..."}`

发布构建会把这两个地址写入插件 manifest，TuneweaveBot 据此自动检查更新，管理员可一键更新；
本地构建不带更新地址，不参与自动更新。

## 配置

```toml
[plugins]
fs_dir = "./plugin-fs"   # 只读挂进沙箱的目录，wvd_path 相对它解析

[plugins.spotify]
sp_dc = ""               # open.spotify.com Cookie 里的 sp_dc；留空可用 /login spotify token 提交
wvd_path = "spotify.wvd" # Widevine L3 设备文件，相对 [plugins] fs_dir
market = "US"            # 元数据/搜索地区代码
instances = 2            # 常驻实例数（搜索/匹配），上限 4；下载并发由 [download] concurrency 控制
```

### sp_dc

登录 open.spotify.com 后从浏览器 Cookie 里取 `sp_dc`，填进配置或用
`/login spotify token` 提交（宿主会持久化到 KV，优先级高于配置文件）。失效时
`auth.status` 会报未登录，宿主暂停下载并提醒管理员。

### Widevine 设备（.wvd）

解密 Spotify 的 Widevine CENC 音频需要一个 L3 设备文件（`.wvd`）。插件运行在
wazero 沙箱里，宿主实例化时只把 `[plugins] fs_dir` 这个目录**只读**挂进沙箱，
所以 `wvd_path` 是相对 `fs_dir` 的路径，不是任意宿主绝对路径。

取得 `.wvd`：在一台实体 Android 设备（建议 13/14+）上用 KeyDive 之类工具提取。
把文件放进 `fs_dir`，`wvd_path` 填相对名即可。

### 内置设备

`device_embed.go` 中的 `builtinWVD` 是一份内置的公开 L3 设备，**开箱即用**：不配 `wvd_path` 时自动使用它。
但：

- 公开 L3 设备随时可能被 Google/Spotify 吊销，届时音频下载开始失败；
- 它是和别人共享的公开设备，稳定性和速率都没保证。

需要稳定使用时优先自备 `.wvd` + `wvd_path`，内置设备只作开箱即用的回落。把
`builtinWVD` 置空再重新构建即可禁用内置回落（此时必须配 `wvd_path`）。

## 标签

宿主为 m4a 写入 iTunes 标签与封面：标题、艺人、专辑、专辑艺人、发行日期、曲目号/总数、
碟号、作曲、版权（℗），以及 `ISRC`/`UPC`/`EAN`/`label` 自定义字段。专辑艺人、厂牌、
ISRC 等来自 spclient `metadata/4/track`，作曲来自 credits，这两项获取失败时只缺对应字段。

## 质量

优先 AAC 256k（format `11`），Free 账号拿不到 256k license 时自动回落 128k
（format `10`）。403 会带诊断：区分账号等级不足、设备被吊销、token 被拒等。
