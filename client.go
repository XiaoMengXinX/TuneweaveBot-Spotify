package main

import (
	"context"
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
	apiBase    = "https://api.spotify.com/v1"
	lyricsBase = "https://spclient.wg.spotify.com/color-lyrics/v2/track/"
)

// apiClient 用 sp_dc cookie 换取的 web-player token 访问 Spotify。
// 元数据/搜索走公开 Web API，歌词走 color-lyrics，音频走 Widevine（widevine.go）。
type apiClient struct {
	http   *http.Client
	spDC   string
	market string
	device *widevine.Device

	mu          sync.Mutex
	auth        WebAuth
	tokenExpiry time.Time
}

func newAPIClient(hc *http.Client, spDC, market string, device *widevine.Device) *apiClient {
	if market == "" {
		market = "US"
	}
	return &apiClient{http: hc, spDC: strings.TrimSpace(spDC), market: market, device: device}
}

func (c *apiClient) configured() bool { return c != nil && c.spDC != "" }

// webAuth 返回缓存的 web-player 凭据，临近过期时刷新。
func (c *apiClient) webAuth(ctx context.Context) (WebAuth, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.auth.Bearer != "" && time.Now().Before(c.tokenExpiry.Add(-60*time.Second)) {
		return c.auth, nil
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
	return c.auth, nil
}

// apiGet 用 web-player bearer 访问公开 Web API。
func (c *apiClient) apiGet(ctx context.Context, path string, query url.Values, out any) error {
	auth, err := c.webAuth(ctx)
	if err != nil {
		return err
	}
	full := apiBase + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+auth.Bearer)
	req.Header.Set("Accept", "application/json")
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
	case http.StatusUnauthorized:
		c.dropToken()
		return fmt.Errorf("spotify: 未授权（sp_dc 可能失效）")
	case http.StatusNotFound:
		return errNotFound
	default:
		return fmt.Errorf("spotify: GET %s 返回 %d", path, resp.StatusCode)
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
}

var errNotFound = fmt.Errorf("spotify: 资源不存在")

// apiTrack 是 Web API 的曲目结构，只取我们需要的字段。
type apiTrack struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	DurationMs int    `json:"duration_ms"`
	Artists    []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Album struct {
		Name   string `json:"name"`
		Images []struct {
			URL string `json:"url"`
		} `json:"images"`
	} `json:"album"`
	ExternalURLs struct {
		Spotify string `json:"spotify"`
	} `json:"external_urls"`
}

func (t apiTrack) artists() []string {
	out := make([]string, 0, len(t.Artists))
	for _, a := range t.Artists {
		if a.Name != "" {
			out = append(out, a.Name)
		}
	}
	return out
}

func (t apiTrack) cover() string {
	if len(t.Album.Images) > 0 {
		return t.Album.Images[0].URL
	}
	return ""
}

func (c *apiClient) getTrack(ctx context.Context, id string) (*apiTrack, error) {
	q := url.Values{}
	q.Set("market", c.market)
	var t apiTrack
	if err := c.apiGet(ctx, "/tracks/"+url.PathEscape(id), q, &t); err != nil {
		return nil, err
	}
	if t.ID == "" {
		return nil, errNotFound
	}
	return &t, nil
}

func (c *apiClient) search(ctx context.Context, keyword string, limit, offset int) ([]apiTrack, int, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	q := url.Values{}
	q.Set("q", keyword)
	q.Set("type", "track")
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	q.Set("market", c.market)
	var resp struct {
		Tracks struct {
			Items []apiTrack `json:"items"`
			Total int        `json:"total"`
		} `json:"tracks"`
	}
	if err := c.apiGet(ctx, "/search", q, &resp); err != nil {
		return nil, 0, err
	}
	items := make([]apiTrack, 0, len(resp.Tracks.Items))
	for _, it := range resp.Tracks.Items {
		if it.ID != "" {
			items = append(items, it)
		}
	}
	return items, resp.Tracks.Total, nil
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
