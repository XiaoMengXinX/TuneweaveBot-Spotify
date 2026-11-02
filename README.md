# Spotify 插件

用 `sp_dc` cookie 换取 web-player token 访问 Spotify：元数据/搜索走公开 Web API，
歌词走 color-lyrics，音频走 Widevine（track-playback → storage-resolve → 本地构造
PSSH → license → CENC 解密），解密后的 AAC/MP4 经宿主的 `media` 能力落盘上传。

这是**数据面重**的插件：解密在插件内完成，整轨经 `media.*` 流式交给宿主，详见
[plugins/README.md §9](../README.md)。

## 构建

```sh
PLUGINS="spotify" bash build-plugins.sh
```

然后在 `config.toml` 的 `[plugins] enabled` 里加上 `spotify`。默认 `build-plugins.sh`
只构建 netease，Spotify 需显式指定。

## 配置

```toml
[plugins]
fs_dir = "./plugin-fs"   # 只读挂进沙箱的目录，wvd_path 相对它解析

[plugins.spotify]
sp_dc = ""               # open.spotify.com Cookie 里的 sp_dc；留空可用 /login spotify token 提交
wvd_path = "spotify.wvd" # Widevine L3 设备文件，相对 [plugins] fs_dir
market = "US"            # 元数据/搜索地区代码
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

### 内置设备（builtin.wvd）

`builtin.wvd` 是仓库里的**空占位文件**，为空时 `builtinDevice()` 返回错误。想
开箱即用可自行把一个公开 L3 `.wvd` 的字节写进 `builtin.wvd` 再重新构建——但：

- 公开 L3 设备随时可能被 Google/Spotify 吊销，届时音频下载开始失败；
- 把设备密钥嵌进二进制并分发有合规风险。

优先用自备 `.wvd` + `wvd_path`，而非内置设备。

## 质量

优先 AAC 256k（format `11`），Free 账号拿不到 256k license 时自动回落 128k
（format `10`）。403 会带诊断：区分账号等级不足、设备被吊销、token 被拒等。
