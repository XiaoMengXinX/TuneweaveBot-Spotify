package main

import (
	"net/url"
	"regexp"
	"strings"
)

// spotifyID 是 Spotify 的 base-62 ID：22 位 [A-Za-z0-9]。track/album/artist/playlist 同形。
var spotifyID = regexp.MustCompile(`^[A-Za-z0-9]{22}$`)

// matchTrack 从分享链接、spotify: URI 或裸 ID 中提取 track ID。
// 只认 track：本插件按单曲点歌，album/playlist 不在 provider 单曲模型内。
func matchTrack(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if spotifyID.MatchString(text) {
		return text
	}
	if id, kind, ok := parseURI(text); ok && kind == "track" {
		return id
	}
	if id, kind, ok := parseWebURL(text); ok && kind == "track" {
		return id
	}
	return ""
}

func parseURI(raw string) (id, kind string, ok bool) {
	if !strings.HasPrefix(raw, "spotify:") {
		return "", "", false
	}
	parts := strings.Split(raw, ":")
	if len(parts) < 3 {
		return "", "", false
	}
	candidate := parts[len(parts)-1]
	if !spotifyID.MatchString(candidate) {
		return "", "", false
	}
	return candidate, strings.ToLower(parts[len(parts)-2]), true
}

func parseWebURL(raw string) (id, kind string, ok bool) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", "", false
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Host), "www.")
	if host != "open.spotify.com" && host != "play.spotify.com" {
		return "", "", false
	}
	segments := strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' })
	// 跳过 "intl-xx" 地区前缀段。
	if len(segments) > 0 && strings.HasPrefix(strings.ToLower(segments[0]), "intl-") {
		segments = segments[1:]
	}
	for i := 0; i+1 < len(segments); i++ {
		switch strings.ToLower(segments[i]) {
		case "track", "album", "artist", "playlist":
			if candidate := segments[i+1]; spotifyID.MatchString(candidate) {
				return candidate, strings.ToLower(segments[i]), true
			}
		}
	}
	return "", "", false
}
