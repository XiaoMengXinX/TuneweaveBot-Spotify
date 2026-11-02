package main

import "encoding/json"

const (
	providerName = "spotify"
	displayName  = "Spotify"
	providerCode = "sp"
)

// Ref 与宿主的 provider.Ref 结构一致。
type Ref struct {
	Provider  string `json:"provider,omitempty"`
	ID        string `json:"id"`
	Sub       string `json:"sub,omitempty"`
	URL       string `json:"url,omitempty"`
	PageURL   string `json:"page_url,omitempty"`
	ShareText string `json:"share_text,omitempty"`
}

// Track 与宿主的 provider.Track 结构一致。
type Track struct {
	Ref
	Title        string            `json:"title"`
	Artists      []string          `json:"artists"`
	Album        string            `json:"album"`
	DurationMS   int               `json:"duration_ms"`
	CoverURL     string            `json:"cover_url,omitempty"`
	AudioURL     string            `json:"audio_url,omitempty"`
	Ext          string            `json:"ext,omitempty"`
	Size         int64             `json:"size,omitempty"`
	BitRate      int               `json:"bit_rate,omitempty"`
	ProviderName string            `json:"provider_name,omitempty"`
	TagFormat    string            `json:"tag_format,omitempty"`
	TagPayload   json.RawMessage   `json:"tag_payload,omitempty"`
	Extra        map[string]string `json:"extra,omitempty"`
}

// TrackSummary 与宿主的 provider.TrackSummary 结构一致。
type TrackSummary struct {
	Ref
	Title      string   `json:"title"`
	Artists    []string `json:"artists"`
	Album      string   `json:"album"`
	DurationMS int      `json:"duration_ms"`
}

// Lyric 与宿主的 provider.Lyric 结构一致。
type Lyric struct {
	LRC     string   `json:"lrc"`
	Title   string   `json:"title"`
	Artists []string `json:"artists"`
	Album   string   `json:"album"`
}

// Manifest 是插件能力声明。
type Manifest struct {
	Name             string        `json:"name"`
	DisplayName      string        `json:"display_name"`
	Version          string        `json:"version"`
	Commit           string        `json:"commit,omitempty"`
	BuildTime        string        `json:"build_time,omitempty"`
	UpdateURL        string        `json:"update_url,omitempty"`
	VersionURL       string        `json:"version_url,omitempty"`
	ABI              int           `json:"abi"`
	Code             string        `json:"code"`
	Capabilities     []string      `json:"capabilities"`
	AuthRequired     bool          `json:"auth_required"`
	Commands         []Command     `json:"commands"`
	Resources        Resources     `json:"resources"`
	HTTPAllowedHosts []string      `json:"http_allowed_hosts"`
	LinkHosts        []string      `json:"link_hosts,omitempty"`
	ConfigSchema     []ConfigField `json:"config_schema,omitempty"`
}

// ConfigField 声明 [plugins.spotify] 段的一个配置项，含义与宿主一致。
type ConfigField struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	Default any    `json:"default,omitempty"`
	Comment string `json:"comment,omitempty"`
	Secret  bool   `json:"secret,omitempty"`
}

// Command 是插件声明的命令别名。
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type,omitempty"`
	Template    string `json:"template,omitempty"`
}

// Resources 是插件的资源建议。
type Resources struct {
	MemoryMB  int `json:"memory_mb,omitempty"`
	Instances int `json:"instances,omitempty"`
}

// Config 是 [plugins.spotify] 段传入的配置。
type Config struct {
	SpDC    string `json:"sp_dc"`
	WVDPath string `json:"wvd_path"`
	Market  string `json:"market"`
}

// AuthChallenge / AuthState 与宿主的 provider 结构一致。
type AuthChallenge struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	Key       string `json:"key"`
	ExpiresIn int    `json:"expires_in"`
}

type Persist struct {
	Section string `json:"section"`
	Key     string `json:"key"`
}

type AuthState struct {
	State      string   `json:"state"`
	Credential string   `json:"credential,omitempty"`
	Nickname   string   `json:"nickname,omitempty"`
	Persist    *Persist `json:"persist,omitempty"`
}

type AuthStatus struct {
	Authenticated bool   `json:"authenticated"`
	Account       string `json:"account,omitempty"`
}
