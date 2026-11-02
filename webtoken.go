package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	totpSecretDictURL = "https://git.gay/thereallo/totp-secrets/raw/branch/main/secrets/secretDict.json"
	webUA             = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36"
	webAppVersion     = "1.2.87.27.ga2033a72"
)

// builtinTOTP* 是拉取在线密钥字典失败时的回落值；Spotify 会定期轮换密钥。
var (
	builtinTOTPSecretVer = "61"
	builtinTOTPCipher    = []int{44, 55, 47, 42, 70, 40, 34, 114, 76, 74, 50, 111, 120, 97, 75, 76, 94, 102, 43, 69, 49, 120, 118, 80, 64, 78}
)

type WebAuth struct {
	Bearer      string
	ClientToken string
}

// deriveSecret 把字典里混淆过的密钥还原成 HMAC-SHA1 key：
// 第 i 字节异或 (i%33)+9，转十进制字符串后拼接。
func deriveSecret(cipher []int) []byte {
	var sb strings.Builder
	for i, b := range cipher {
		sb.WriteString(strconv.Itoa(b ^ ((i % 33) + 9)))
	}
	return []byte(sb.String())
}

// fetchTOTPSecret 返回在线密钥字典中版本最高的 TOTP 密钥；拉取失败时回落内置密钥。
// source 说明密钥来源，回落时附带原因，供日志诊断。
func fetchTOTPSecret(ctx context.Context, hc *http.Client) (version string, cipher []int, source string) {
	builtin := func(reason string) (string, []int, string) {
		return builtinTOTPSecretVer, builtinTOTPCipher, "内置（" + reason + "）"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, totpSecretDictURL, nil)
	if err != nil {
		return builtin(err.Error())
	}
	resp, err := hc.Do(req)
	if err != nil {
		return builtin("拉取 git.gay 失败: " + err.Error())
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var dict map[string][]int
	if json.Unmarshal(b, &dict) != nil || len(dict) == 0 {
		return builtin(fmt.Sprintf("git.gay 返回 HTTP %d，内容无法解析", resp.StatusCode))
	}
	bestVer, bestNum := "", -1
	for k := range dict {
		if n, err := strconv.Atoi(k); err == nil && n > bestNum {
			bestNum, bestVer = n, k
		}
	}
	arr := dict[bestVer]
	if len(arr) == 0 {
		return builtin("git.gay 字典中没有可用版本")
	}
	return bestVer, arr, "git.gay"
}

// generateTOTP computes the 6-digit HMAC-SHA1 TOTP Spotify expects, using the
// raw secret bytes as the HMAC key and a 30-second step.
func generateTOTP(secret []byte, serverTimeMs int64) string {
	counter := serverTimeMs / 1000 / 30
	cb := make([]byte, 8)
	binary.BigEndian.PutUint64(cb, uint64(counter))
	h := hmac.New(sha1.New, secret)
	h.Write(cb)
	sum := h.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := uint32(sum[off]&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	return fmt.Sprintf("%06d", bin%1000000)
}

// WebTokenResult carries the web-player token plus a diagnostic trace.
type WebTokenResult struct {
	AccessToken string
	ClientID    string
	ClientToken string
	ExpiresAtMs int64
	Steps       []string
	TOTPWarning string // /api/token 成功但提示 TOTP 版本过期时的说明
}

// WebTokenFromCookie exchanges an sp_dc cookie for a web-player access token.
func WebTokenFromCookie(ctx context.Context, hc *http.Client, spDC string) (*WebTokenResult, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	spDC = strings.TrimSpace(spDC)
	if spDC == "" {
		return nil, fmt.Errorf("native: empty sp_dc cookie")
	}
	res := &WebTokenResult{}
	add := func(f string, a ...any) { res.Steps = append(res.Steps, fmt.Sprintf(f, a...)) }

	add("sp_dc 长度 %d", len(spDC))

	// 1) server time (cookie-authenticated)
	stReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://open.spotify.com/api/server-time", nil)
	cookieHeaders(stReq, spDC)
	stResp, err := hc.Do(stReq)
	if err != nil {
		return res, fmt.Errorf("server-time: %w", err)
	}
	stBody, _ := io.ReadAll(io.LimitReader(stResp.Body, 1<<16))
	stResp.Body.Close()
	var stj struct {
		ServerTime int64 `json:"serverTime"`
	}
	if json.Unmarshal(stBody, &stj) != nil || stj.ServerTime == 0 {
		return res, fmt.Errorf("server-time parse failed (HTTP %d): %s", stResp.StatusCode, snippet(stBody))
	}
	add("server-time %d", stj.ServerTime)

	// 2) TOTP (derive the real HMAC key from the obfuscated cipher first)
	ver, cipher, source := fetchTOTPSecret(ctx, hc)
	totp := generateTOTP(deriveSecret(cipher), stj.ServerTime*1000)
	add("TOTP 密钥 v%s，来源 %s", ver, source)

	// 3) /api/token (cookie + TOTP)
	tokURL := fmt.Sprintf("https://open.spotify.com/api/token?reason=init&productType=web-player&totp=%s&totpServer=%s&totpVer=%s", totp, totp, ver)
	tReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, tokURL, nil)
	cookieHeaders(tReq, spDC)
	tResp, err := hc.Do(tReq)
	if err != nil {
		// 未拿到 /api/token 的响应（超时、DNS 等）属于网络问题，不判定 sp_dc 失效
		return res, fmt.Errorf("token: %w", err)
	}
	tBody, _ := io.ReadAll(io.LimitReader(tResp.Body, 1<<16))
	tResp.Body.Close()
	add("/api/token HTTP %d", tResp.StatusCode)
	// TOTP 先于 sp_dc 校验：400 说明 TOTP 密钥错误，无法据此判断 sp_dc；
	// 401 说明 TOTP 正确而 sp_dc 无效。
	switch tResp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return res, fmt.Errorf("%w: /api/token HTTP 401: %s", errSpDCInvalid, snippet(tBody))
	case http.StatusBadRequest:
		return res, fmt.Errorf("/api/token 拒绝 TOTP（密钥 v%s，来源 %s），密钥可能已轮换: %s", ver, source, snippet(tBody))
	default:
		return res, fmt.Errorf("/api/token HTTP %d: %s", tResp.StatusCode, snippet(tBody))
	}
	var tj struct {
		AccessToken                      string `json:"accessToken"`
		ClientID                         string `json:"clientId"`
		IsAnonymous                      bool   `json:"isAnonymous"`
		AccessTokenExpirationTimestampMs int64  `json:"accessTokenExpirationTimestampMs"`
		TOTPVerExpired                   string `json:"totpVerExpired"`
		TOTPValidUntil                   string `json:"totpValidUntil"`
	}
	if json.Unmarshal(tBody, &tj) != nil || tj.AccessToken == "" {
		return res, fmt.Errorf("/api/token 响应无法解析: %s", snippet(tBody))
	}
	if tj.IsAnonymous {
		return res, fmt.Errorf("%w: /api/token 返回匿名 token", errSpDCInvalid)
	}
	if tj.TOTPVerExpired != "" {
		res.TOTPWarning = fmt.Sprintf("TOTP 密钥 v%s（来源 %s）已过期（totpVerExpired=%s，有效期至 %s），需更新密钥", ver, source, tj.TOTPVerExpired, tj.TOTPValidUntil)
		add("%s", res.TOTPWarning)
	}
	add("web token 有效（长度 %d，到期 %s）", len(tj.AccessToken), time.UnixMilli(tj.AccessTokenExpirationTimestampMs).UTC().Format(time.RFC3339))
	res.AccessToken = tj.AccessToken
	res.ClientID = tj.ClientID
	res.ExpiresAtMs = tj.AccessTokenExpirationTimestampMs

	// Fetch a client-token: spclient calls (track-playback, storage-resolve,
	// widevine-license) require it in addition to the Bearer token.
	if tj.ClientID != "" {
		if ct, cterr := fetchWebClientToken(ctx, hc, tj.ClientID); cterr == nil {
			res.ClientToken = ct
			add("client-token 有效（长度 %d）", len(ct))
		} else {
			add("client-token 获取失败（继续使用 web token）: %v", cterr)
		}
	}
	return res, nil
}

// fetchWebClientToken obtains a client-token for spclient calls. The web player
// POSTs the client id + version to clienttoken.spotify.com and reads back a
// granted token. Spotify accepts a JSON request/response for the web client.
func fetchWebClientToken(ctx context.Context, hc *http.Client, clientID string) (string, error) {
	body := fmt.Sprintf(`{"client_data":{"client_version":%q,"client_id":%q,"js_sdk_data":{}}}`, webAppVersion, clientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://clienttoken.spotify.com/v1/clienttoken", strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", webUA)
	req.Header.Set("Origin", "https://open.spotify.com")
	req.Header.Set("Referer", "https://open.spotify.com/")
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("clienttoken HTTP %d: %s", resp.StatusCode, snippet(b))
	}
	var ctr struct {
		GrantedToken struct {
			Token string `json:"token"`
		} `json:"granted_token"`
	}
	if json.Unmarshal(b, &ctr) != nil || ctr.GrantedToken.Token == "" {
		return "", fmt.Errorf("clienttoken parse failed: %s", snippet(b))
	}
	return ctr.GrantedToken.Token, nil
}

func cookieHeaders(req *http.Request, spDC string) {
	req.Header.Set("Cookie", "sp_dc="+spDC)
	req.Header.Set("User-Agent", webUA)
	req.Header.Set("App-Platform", "WebPlayer")
	req.Header.Set("Spotify-App-Version", webAppVersion)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Origin", "https://open.spotify.com")
	req.Header.Set("Referer", "https://open.spotify.com/")
	req.Header.Set("Accept", "application/json")
}

// snippet truncates a byte body for inclusion in error messages.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
