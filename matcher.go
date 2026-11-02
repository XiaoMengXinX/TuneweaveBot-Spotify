package main

import (
	"net/url"
	"regexp"
	"strings"
)

// spotifyID 是 Spotify 的 base-62 ID：22 位 [A-Za-z0-9]。
var spotifyID = regexp.MustCompile(`^[A-Za-z0-9]{22}$`)

// matchTrack 从分享链接、spotify:track: URI 或裸 ID 中提取 track ID。
// 本插件按单曲点歌，album/playlist 等不在 provider 单曲模型内。
func matchTrack(text string) string {
	text = strings.TrimSpace(text)
	if spotifyID.MatchString(text) {
		return text
	}
	if rest, ok := strings.CutPrefix(text, "spotify:track:"); ok && spotifyID.MatchString(rest) {
		return rest
	}
	return webURLTrackID(text)
}

// webURLTrackID 取 open.spotify.com/(intl-xx/)track/<id> 中的 ID。
func webURLTrackID(raw string) string {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Host), "www.")
	if host != "open.spotify.com" && host != "play.spotify.com" {
		return ""
	}
	segments := strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' })
	for i := 0; i+1 < len(segments); i++ {
		if strings.EqualFold(segments[i], "track") && spotifyID.MatchString(segments[i+1]) {
			return segments[i+1]
		}
	}
	return ""
}
