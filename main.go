// Command spotify 是 TuneweaveBot 的 Spotify WASM 插件。
//
// 元数据/搜索/歌词走 sp_dc cookie 换取的 web-player token；音频走 Widevine：
// track-playback → storage-resolve → 本地构造 PSSH → license → CENC 解密，
// 解密后的 AAC/MP4 经 media.* 能力交给宿主上传。内置公开 L3 设备可开箱即用，
// 也可用 wvd_path 覆盖为自备 .wvd。
package main

import (
	"encoding/json"

	widevine "github.com/iyear/gowidevine"

	"github.com/XiaoMengXinX/TuneweaveBot/pluginsdk"
)

var (
	client *apiClient
	cfg    Config
)

func init() { pluginsdk.Main(handle) }

func main() {}

func manifest() Manifest {
	return Manifest{
		Name:         providerName,
		DisplayName:  displayName,
		Version:      "1.0.0",
		ABI:          pluginsdk.ABI,
		Code:         providerCode,
		Capabilities: []string{"provider", "auth", "media"},
		AuthRequired: true,
		Commands: []Command{
			{Name: "spotify", Description: "使用 Spotify 点歌"},
		},
		Resources: Resources{MemoryMB: 256, Instances: 1},
		HTTPAllowedHosts: []string{
			"open.spotify.com",
			"accounts.spotify.com",
			"api.spotify.com",
			"api-partner.spotify.com",
			"clienttoken.spotify.com",
			"spclient.wg.spotify.com",
			"gew1-spclient.spotify.com",
			"git.gay",
			"*.scdn.co",
			"*.spotifycdn.com",
			"*.audio-fa.scdn.co",
		},
		ConfigSchema: []ConfigField{
			{Key: "sp_dc", Type: "string", Default: "", Secret: true, Comment: "登录 open.spotify.com 后 Cookie 中的 sp_dc，用 /login spotify token 也可提交"},
			{Key: "wvd_path", Type: "string", Default: "", Comment: "Widevine L3 设备文件(.wvd)路径，相对 [plugins] fs_dir 只读挂载目录；解密音频必需，留空用内置公开设备（可能被封）"},
			{Key: "market", Type: "string", Default: "US", Comment: "元数据/搜索的地区代码(ISO 3166-1 alpha-2)"},
		},
	}
}

func handle(c *pluginsdk.Context) (any, error) {
	switch c.Action() {
	case "manifest":
		return manifest(), nil
	case "init":
		return initPlugin(c)
	case "shutdown":
		return map[string]any{}, nil
	case "provider.match":
		var in struct {
			Text string `json:"text"`
		}
		if err := c.Decode(&in); err != nil {
			return nil, err
		}
		if id := matchTrack(in.Text); id != "" {
			return map[string]any{"ref": makeRef(id)}, nil
		}
		return map[string]any{"ref": nil}, nil
	case "provider.canonicalize":
		var in struct {
			Ref Ref `json:"ref"`
		}
		if err := c.Decode(&in); err != nil {
			return nil, err
		}
		return map[string]any{"ref": makeRef(in.Ref.ID)}, nil
	case "provider.resolve":
		var in struct {
			Ref Ref `json:"ref"`
		}
		if err := c.Decode(&in); err != nil {
			return nil, err
		}
		return resolve(c, in.Ref)
	case "provider.search":
		var in struct {
			Keyword string `json:"keyword"`
			Limit   int    `json:"limit"`
			Offset  int    `json:"offset"`
		}
		if err := c.Decode(&in); err != nil {
			return nil, err
		}
		return searchTracks(c, in.Keyword, in.Limit, in.Offset)
	case "provider.lyrics":
		var in struct {
			Ref Ref `json:"ref"`
		}
		if err := c.Decode(&in); err != nil {
			return nil, err
		}
		return lyricsFor(c, in.Ref)
	case "auth.status":
		return authStatus(c)
	case "auth.begin":
		var in struct {
			Mode string `json:"mode"`
		}
		if err := c.Decode(&in); err != nil {
			return nil, err
		}
		return authBegin(in.Mode)
	case "auth.submit":
		var in struct {
			Token string `json:"token"`
		}
		if err := c.Decode(&in); err != nil {
			return nil, err
		}
		return authSubmit(c, in.Token)
	case "auth.apply":
		var in struct {
			Credential string `json:"credential"`
		}
		if err := c.Decode(&in); err != nil {
			return nil, err
		}
		return map[string]any{}, authApply(c, in.Credential)
	default:
		return nil, pluginsdk.Fail("unsupported", "unknown action "+c.Action())
	}
}

func initPlugin(c *pluginsdk.Context) (any, error) {
	var in struct {
		Config     json.RawMessage `json:"config"`
		Credential string          `json:"credential"`
	}
	if err := c.Decode(&in); err != nil {
		return nil, err
	}
	cfg = Config{}
	if len(in.Config) != 0 {
		if err := json.Unmarshal(in.Config, &cfg); err != nil {
			return nil, pluginsdk.Fail("invalid_request", "invalid plugin config")
		}
	}

	// 凭据优先级：登录持久化的 KV > 配置文件 sp_dc。
	spDC := cfg.SpDC
	if value, found, err := c.KVGet("sp_dc"); err == nil && found {
		spDC = string(value)
	}

	device := loadDevice(c)
	client = newAPIClient(pluginsdk.NewHTTPClient(), spDC, cfg.Market, device)
	return map[string]any{}, nil
}

// loadDevice 优先用配置的 wvd_path，否则回落到内置公开设备。
func loadDevice(c *pluginsdk.Context) *widevine.Device {
	if cfg.WVDPath != "" {
		if dev, err := LoadWVDeviceFile(cfg.WVDPath); err == nil {
			return dev
		} else {
			c.Log("warn", "加载 wvd_path 失败，回落内置设备: "+err.Error())
		}
	}
	dev, err := builtinDevice()
	if err != nil {
		c.Log("warn", "内置 Widevine 设备不可用，音频下载将失败: "+err.Error())
		return nil
	}
	return dev
}

func makeRef(id string) Ref {
	link := "https://open.spotify.com/track/" + id
	return Ref{Provider: providerName, ID: id, PageURL: link, ShareText: link}
}
