package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	widevine "github.com/iyear/gowidevine"
)

const (
	pathfinderURL = "https://api-partner.spotify.com/pathfinder/v2/query"
	lyricsBase    = "https://spclient.wg.spotify.com/color-lyrics/v2/track/"

	getTrackOp     = "getTrack"
	getTrackHash   = "612585ae06ba435ad26369870deaae23b5c8800a256cd8a57e08eddc25a37294"
	findTracksOp   = "findTracks"
	findTracksHash = "755858df4daab8d212980b02a81dcf8c9a58447de318b59d07c4651a1d0450b9"
)

// apiClient 用 sp_dc cookie 换取的 web-player token 访问 Spotify。
// 元数据/搜索走 Pathfinder GQL，歌词走 color-lyrics，音频走 Widevine（widevine.go）。
type apiClient struct {
	http   *http.Client
	spDC   string
	market string
	device *widevine.Device
	kv     kvStore

	mu          sync.Mutex
	auth        WebAuth
	tokenExpiry time.Time
}

// kvStore 是宿主 KV，用于在实例间共享 web token。
type kvStore interface {
	KVGet(key string) ([]byte, bool, error)
	KVSet(key string, value []byte) error
	KVDelete(key string) error
}

func newAPIClient(hc *http.Client, spDC, market string, device *widevine.Device, kv kvStore) *apiClient {
	if market == "" {
		market = "US"
	}
	return &apiClient{http: hc, spDC: strings.TrimSpace(spDC), market: market, device: device, kv: kv}
}

const tokenKVKey = "web_token"

// cachedToken 是存进 KV 的 web token。下载在临时实例中进行，不共享就要每首歌重新换一次。
type cachedToken struct {
	Auth      WebAuth   `json:"auth"`
	ExpiresAt time.Time `json:"expires_at"`
	Owner     string    `json:"owner"` // sp_dc 指纹，换账号后旧 token 作废
}

func (c *apiClient) tokenOwner() string {
	sum := sha256.Sum256([]byte(c.spDC))
	return hex.EncodeToString(sum[:8])
}

func tokenFresh(expiry time.Time) bool { return time.Now().Before(expiry.Add(-60 * time.Second)) }

func (c *apiClient) configured() bool { return c != nil && c.spDC != "" }

// webAuth 返回缓存的 web-player 凭据，临近过期时刷新。
func (c *apiClient) webAuth(ctx context.Context) (WebAuth, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.auth.Bearer != "" && tokenFresh(c.tokenExpiry) {
		return c.auth, nil
	}
	if raw, found, err := c.kv.KVGet(tokenKVKey); err == nil && found {
		var cached cachedToken
		if json.Unmarshal(raw, &cached) == nil && cached.Owner == c.tokenOwner() &&
			cached.Auth.Bearer != "" && tokenFresh(cached.ExpiresAt) {
			c.auth, c.tokenExpiry = cached.Auth, cached.ExpiresAt
			return c.auth, nil
		}
	}
	wt, err := WebTokenFromCookie(ctx, c.http, c.spDC)
	if err != nil {
		return WebAuth{}, err
	}
	c.auth = WebAuth{Bearer: wt.AccessToken, ClientToken: wt.ClientToken}
	if wt.ExpiresAtMs > 0 {
		c.tokenExpiry = time.UnixMilli(wt.ExpiresAtMs)
	} else {
		c.tokenExpiry = time.Now().Add(30 * time.Minute)
	}
	if raw, err := json.Marshal(cachedToken{Auth: c.auth, ExpiresAt: c.tokenExpiry, Owner: c.tokenOwner()}); err == nil {
		_ = c.kv.KVSet(tokenKVKey, raw)
	}
	return c.auth, nil
}

// pathfinderQuery 访问 Pathfinder GQL。web-player token 调用公开 Web API 会被 429。
func (c *apiClient) pathfinderQuery(ctx context.Context, operation, hash string, variables map[string]any, out any) error {
	auth, err := c.webAuth(ctx)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"variables":     variables,
		"operationName": operation,
		"extensions": map[string]any{
			"persistedQuery": map[string]any{"version": 1, "sha256Hash": hash},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pathfinderURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	ApplyWebAuthHeaders(req, auth)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return errNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		c.dropToken()
		return fmt.Errorf("spotify: 未授权（sp_dc 可能失效）")
	default:
		return fmt.Errorf("spotify: pathfinder %s 返回 %d", operation, resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *apiClient) dropToken() {
	c.mu.Lock()
	c.auth = WebAuth{}
	c.mu.Unlock()
	_ = c.kv.KVDelete(tokenKVKey)
}

var errNotFound = fmt.Errorf("spotify: 资源不存在")

// apiTrack 是曲目的内部统一结构（由 Pathfinder 响应转换而来）。
type apiTrack struct {
	ID          string
	Name        string
	DurationMs  int
	Artists     []string
	AlbumName   string
	CoverURL    string
	TrackNumber int
	TrackTotal  int
	ReleaseDate string
	Copyright   string
}

// pathfinderTrack 对应 Pathfinder trackUnion / 搜索命中的 track 结构，只取所需字段。
type pathfinderTrack struct {
	ID       string `json:"id"`
	URI      string `json:"uri"`
	Name     string `json:"name"`
	Duration struct {
		TotalMilliseconds int `json:"totalMilliseconds"`
	} `json:"duration"`
	FirstArtist  struct{ Items []pathfinderArtist } `json:"firstArtist"`
	OtherArtists struct{ Items []pathfinderArtist } `json:"otherArtists"`
	Artists      struct{ Items []pathfinderArtist } `json:"artists"`
	TrackNumber  int                                `json:"trackNumber"`
	Album        struct {
		ID   string `json:"id"`
		URI  string `json:"uri"`
		Name string `json:"name"`
		Date struct {
			IsoString string `json:"isoString"`
			Precision string `json:"precision"`
		} `json:"date"`
		Copyright struct {
			Items []struct {
				Text string `json:"text"`
				Type string `json:"type"`
			} `json:"items"`
		} `json:"copyright"`
		Tracks struct {
			TotalCount int `json:"totalCount"`
		} `json:"tracks"`
		CoverArt struct {
			Sources []struct {
				URL    string `json:"url"`
				Width  int    `json:"width"`
				Height int    `json:"height"`
			} `json:"sources"`
		} `json:"coverArt"`
	} `json:"albumOfTrack"`
}

type pathfinderArtist struct {
	URI     string `json:"uri"`
	Profile struct {
		Name string `json:"name"`
	} `json:"profile"`
}

// toAPITrack 把 Pathfinder 曲目转为内部结构。封面取最大一张。
func (p pathfinderTrack) toAPITrack() apiTrack {
	artists := make([]string, 0, len(p.FirstArtist.Items)+len(p.OtherArtists.Items))
	src := append(append([]pathfinderArtist{}, p.FirstArtist.Items...), p.OtherArtists.Items...)
	if len(src) == 0 {
		src = p.Artists.Items
	}
	for _, a := range src {
		if a.Profile.Name != "" {
			artists = append(artists, a.Profile.Name)
		}
	}
	cover := ""
	bestArea := -1
	for _, s := range p.Album.CoverArt.Sources {
		if area := s.Width * s.Height; area > bestArea {
			bestArea, cover = area, s.URL
		}
	}
	return apiTrack{
		ID:         pathfinderID(p.ID, p.URI),
		Name:       p.Name,
		DurationMs: p.Duration.TotalMilliseconds,
		Artists:    artists,
		AlbumName:  p.Album.Name,
		CoverURL:   cover,

		TrackNumber: p.TrackNumber,
		TrackTotal:  p.Album.Tracks.TotalCount,
		ReleaseDate: releaseDate(p.Album.Date.IsoString, p.Album.Date.Precision),
		Copyright:   phonogramCopyright(p.Album.Copyright.Items),
	}
}

// releaseDate 按精度截取 ISO 时间，得到 YYYY、YYYY-MM 或 YYYY-MM-DD。
func releaseDate(iso, precision string) string {
	n := map[string]int{"YEAR": 4, "MONTH": 7}[precision]
	if n == 0 {
		n = 10
	}
	if len(iso) < n {
		return ""
	}
	return iso[:n]
}

// phonogramCopyright 取录音版权（P），统一以 ℗ 开头。
func phonogramCopyright(items []struct {
	Text string `json:"text"`
	Type string `json:"type"`
}) string {
	for _, it := range items {
		if it.Type != "P" {
			continue
		}
		text := strings.TrimSpace(strings.Replace(it.Text, "(P)", "℗", 1))
		if !strings.HasPrefix(text, "℗") {
			text = "℗ " + text
		}
		return text
	}
	return ""
}

// pathfinderID 优先用 id 字段，缺失时从 "spotify:track:<id>" URI 末段取。
func pathfinderID(id, uri string) string {
	if id != "" {
		return id
	}
	if i := strings.LastIndex(uri, ":"); i >= 0 {
		return uri[i+1:]
	}
	return ""
}

func (c *apiClient) getTrack(ctx context.Context, id string) (*apiTrack, error) {
	var resp struct {
		Data struct {
			Track pathfinderTrack `json:"trackUnion"`
		} `json:"data"`
	}
	if err := c.pathfinderQuery(ctx, getTrackOp, getTrackHash, map[string]any{"uri": "spotify:track:" + id}, &resp); err != nil {
		return nil, err
	}
	t := resp.Data.Track.toAPITrack()
	if t.ID == "" {
		return nil, errNotFound
	}
	return &t, nil
}

func (c *apiClient) search(ctx context.Context, keyword string, limit, offset int) ([]apiTrack, int, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var resp struct {
		Data struct {
			SearchV2 struct {
				TracksV2 struct {
					Items []struct {
						Item struct {
							Data pathfinderTrack `json:"data"`
						} `json:"item"`
					} `json:"items"`
					TotalCount int `json:"totalCount"`
				} `json:"tracksV2"`
			} `json:"searchV2"`
		} `json:"data"`
	}
	vars := map[string]any{"query": strings.TrimSpace(keyword), "limit": limit, "offset": offset}
	if err := c.pathfinderQuery(ctx, findTracksOp, findTracksHash, vars, &resp); err != nil {
		return nil, 0, err
	}
	raw := resp.Data.SearchV2.TracksV2.Items
	items := make([]apiTrack, 0, len(raw))
	for _, it := range raw {
		t := it.Item.Data.toAPITrack()
		if t.ID != "" {
			items = append(items, t)
		}
	}
	return items, resp.Data.SearchV2.TracksV2.TotalCount, nil
}

// lyrics 取 color-lyrics 歌词，拼成 LRC。无歌词或未同步时返回空串。
func (c *apiClient) lyrics(ctx context.Context, id string) (string, error) {
	auth, err := c.webAuth(ctx)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lyricsBase+url.PathEscape(id)+"?format=json&market="+c.market, nil)
	if err != nil {
		return "", err
	}
	ApplyWebAuthHeaders(req, auth)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusNotFound:
		return "", nil
	default:
		return "", fmt.Errorf("spotify: lyrics 返回 %d", resp.StatusCode)
	}
	var result struct {
		Lyrics struct {
			SyncType string `json:"syncType"`
			Lines    []struct {
				StartTimeMs string `json:"startTimeMs"`
				Words       string `json:"words"`
			} `json:"lines"`
		} `json:"lyrics"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", nil
	}
	synced := !strings.EqualFold(strings.TrimSpace(result.Lyrics.SyncType), "UNSYNCED")
	var b strings.Builder
	for _, line := range result.Lyrics.Lines {
		words := strings.TrimSpace(line.Words)
		if words == "" {
			continue
		}
		if synced {
			if ms, err := strconv.ParseInt(strings.TrimSpace(line.StartTimeMs), 10, 64); err == nil && ms >= 0 {
				b.WriteString(lrcTimestamp(ms))
			}
		}
		b.WriteString(words)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func lrcTimestamp(ms int64) string {
	m := ms / 60000
	s := (ms % 60000) / 1000
	cs := (ms % 1000) / 10
	return fmt.Sprintf("[%02d:%02d.%02d]", m, s, cs)
}
