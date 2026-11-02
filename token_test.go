package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type memKV map[string][]byte

func (m memKV) KVGet(key string) ([]byte, bool, error) { v, ok := m[key]; return v, ok, nil }
func (m memKV) KVSet(key string, value []byte) error   { m[key] = value; return nil }
func (m memKV) KVDelete(key string) error              { delete(m, key); return nil }

type roundTripFunc func(*http.Request) *http.Response

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r), nil }

func reply(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

// newTestClient 依次发放 tokens 中的 token；respond 按请求携带的 token 决定响应。
func newTestClient(t *testing.T, tokens []string, respond func(token string, r *http.Request) *http.Response) (*apiClient, *int) {
	t.Helper()
	minted := 0
	orig := mintWebToken
	mintWebToken = func(context.Context, *http.Client, string) (*WebTokenResult, error) {
		tok := tokens[min(minted, len(tokens)-1)]
		minted++
		return &WebTokenResult{AccessToken: tok}, nil
	}
	t.Cleanup(func() { mintWebToken = orig })
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) *http.Response {
		return respond(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), r)
	})}
	return newAPIClient(hc, "dc", "US", nil, memKV{}), &minted
}

func TestRejectedTokenIsRefreshedOnce(t *testing.T) {
	c, minted := newTestClient(t, []string{"stale", "fresh"}, func(token string, _ *http.Request) *http.Response {
		if token == "fresh" {
			return reply(http.StatusOK, `{"data":{}}`)
		}
		return reply(http.StatusUnauthorized, "")
	})
	if err := c.pathfinderQuery(context.Background(), "op", "hash", nil, nil); err != nil {
		t.Fatalf("换新 token 后应成功: %v", err)
	}
	if *minted != 2 {
		t.Fatalf("minted = %d, want 2", *minted)
	}
}

// 新 token 有效且非匿名时仍失败：普通错误，不判定 sp_dc 失效，只重试一次。
func TestPersistentRejectionStopsAfterRetry(t *testing.T) {
	c, minted := newTestClient(t, []string{"a", "b", "c"}, func(string, *http.Request) *http.Response {
		return reply(http.StatusForbidden, `{"error":"forbidden"}`)
	})
	var logs []string
	c.log = func(level, msg string) {
		if level != "debug" {
			logs = append(logs, level+": "+msg)
		}
	}
	err := c.pathfinderQuery(context.Background(), "getTrack", "hash", nil, nil)
	if err == nil || errors.Is(err, errSpDCInvalid) || errors.Is(err, errTokenRejected) || *minted != 2 {
		t.Fatalf("err = %v, minted = %d", err, *minted)
	}
	if !strings.Contains(err.Error(), "pathfinder getTrack HTTP 403") || !strings.Contains(err.Error(), "sp_dc 正常") {
		t.Errorf("err = %v", err)
	}
	if len(logs) != 2 || !strings.HasPrefix(logs[0], "warn: ") || !strings.HasPrefix(logs[1], "error: ") {
		t.Errorf("logs = %q", logs)
	}
}

// color-lyrics 用 403 表示没有歌词，不应触发换 token。
func TestLyricsForbiddenMeansNoLyrics(t *testing.T) {
	c, minted := newTestClient(t, []string{"a", "b"}, func(string, *http.Request) *http.Response {
		return reply(http.StatusForbidden, "")
	})
	lrc, err := c.lyrics(context.Background(), "id")
	if err != nil || lrc != "" || *minted != 1 {
		t.Fatalf("lrc = %q, err = %v, minted = %d", lrc, err, *minted)
	}
}

func TestTrackPlaybackRejectionStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized} {
		c, _ := newTestClient(t, []string{"a"}, func(string, *http.Request) *http.Response {
			return reply(status, "")
		})
		_, err := resolveMP4Files(context.Background(), c.http, WebAuth{Bearer: "a"}, "id")
		if !errors.Is(err, errTokenRejected) || !strings.Contains(err.Error(), "track-playback HTTP") {
			t.Fatalf("status %d: err = %v, want errTokenRejected", status, err)
		}
	}
}

// 400 同样换新 token 重试，仍失败时错误带有接口与状态码。
func TestBadRequestRefreshesToken(t *testing.T) {
	c, minted := newTestClient(t, []string{"stale", "fresh"}, func(token string, _ *http.Request) *http.Response {
		if token == "fresh" {
			return reply(http.StatusOK, `{"lyrics":{"syncType":"UNSYNCED","lines":[]}}`)
		}
		return reply(http.StatusBadRequest, "")
	})
	if _, err := c.lyrics(context.Background(), "id"); err != nil || *minted != 2 {
		t.Fatalf("err = %v, minted = %d", err, *minted)
	}

	c, _ = newTestClient(t, []string{"a", "b"}, func(string, *http.Request) *http.Response {
		return reply(http.StatusBadRequest, "")
	})
	err := c.pathfinderQuery(context.Background(), "getTrack", "hash", nil, nil)
	if err == nil || errors.Is(err, errSpDCInvalid) || !strings.Contains(err.Error(), "pathfinder getTrack HTTP 400") {
		t.Fatalf("err = %v", err)
	}
}

// 换 token 时 sp_dc 失效直接返回 errSpDCInvalid，由 handle 转为 auth_required。
func TestInvalidSpDCDuringRetry(t *testing.T) {
	orig := mintWebToken
	t.Cleanup(func() { mintWebToken = orig })
	calls := 0
	mintWebToken = func(context.Context, *http.Client, string) (*WebTokenResult, error) {
		calls++
		if calls == 1 {
			return &WebTokenResult{AccessToken: "old"}, nil
		}
		return nil, errSpDCInvalid
	}
	hc := &http.Client{Transport: roundTripFunc(func(*http.Request) *http.Response { return reply(http.StatusUnauthorized, "") })}
	c := newAPIClient(hc, "dc", "US", nil, memKV{})
	if err := c.pathfinderQuery(context.Background(), "getTrack", "hash", nil, nil); !errors.Is(err, errSpDCInvalid) {
		t.Fatalf("err = %v, want errSpDCInvalid", err)
	}
}

func TestAuthStatus(t *testing.T) {
	origMint, origClient := mintWebToken, client
	t.Cleanup(func() { mintWebToken, client = origMint, origClient })
	for _, c := range []struct {
		name    string
		mintErr error
		authed  bool
		wantErr bool
	}{
		{"valid", nil, true, false},
		{"invalid sp_dc", fmt.Errorf("%w: /api/token 返回匿名 token", errSpDCInvalid), false, false},
		{"network", errors.New("server-time: timeout"), false, true},
	} {
		mintWebToken = func(context.Context, *http.Client, string) (*WebTokenResult, error) {
			if c.mintErr != nil {
				return nil, c.mintErr
			}
			return &WebTokenResult{AccessToken: "t"}, nil
		}
		client = newAPIClient(http.DefaultClient, "dc", "US", nil, memKV{})
		out, err := authStatus(nil)
		if (err != nil) != c.wantErr {
			t.Fatalf("%s: err = %v", c.name, err)
		}
		if err == nil && out.(AuthStatus).Authenticated != c.authed {
			t.Fatalf("%s: status = %+v", c.name, out)
		}
	}
}

// 按实测行为分类 /api/token：TOTP 先于 sp_dc 校验，400 是 TOTP 错误，401 与匿名 token 才是 sp_dc 无效。
func TestWebTokenFromCookieClassifiesErrors(t *testing.T) {
	for _, c := range []struct {
		name        string
		token       func() (*http.Response, error)
		wantInvalid bool
		wantMsg     string
	}{
		{"401 invalid sp_dc", func() (*http.Response, error) { return reply(http.StatusUnauthorized, "denied"), nil }, true, "HTTP 401"},
		{"anonymous", func() (*http.Response, error) {
			return reply(http.StatusOK, `{"accessToken":"x","isAnonymous":true}`), nil
		}, true, "匿名"},
		{"400 wrong totp", func() (*http.Response, error) { return reply(http.StatusBadRequest, "Unauthorized request"), nil }, false, "拒绝 TOTP"},
		{"500", func() (*http.Response, error) { return reply(http.StatusInternalServerError, ""), nil }, false, "HTTP 500"},
		{"bad body", func() (*http.Response, error) { return reply(http.StatusOK, "not json"), nil }, false, "无法解析"},
		{"network", func() (*http.Response, error) { return nil, errors.New("timeout") }, false, "timeout"},
	} {
		hc := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/api/server-time":
				return reply(http.StatusOK, `{"serverTime":1700000000}`), nil
			case "/api/token":
				return c.token()
			}
			return reply(http.StatusNotFound, ""), nil
		})}
		_, err := WebTokenFromCookie(context.Background(), hc, "dc")
		if err == nil || errors.Is(err, errSpDCInvalid) != c.wantInvalid || !strings.Contains(err.Error(), c.wantMsg) {
			t.Errorf("%s: err = %v, want invalid=%v containing %q", c.name, err, c.wantInvalid, c.wantMsg)
		}
	}
}

// 成功换取时记录步骤（不含凭据内容），并带出 TOTP 版本过期提示。
func TestWebTokenStepsAndTOTPWarning(t *testing.T) {
	hc := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/server-time":
			return reply(http.StatusOK, `{"serverTime":1700000000}`), nil
		case "/api/token":
			return reply(http.StatusOK, `{"accessToken":"secret-token","clientId":"","isAnonymous":false,"accessTokenExpirationTimestampMs":1700003600000,"totpVerExpired":"error","totpValidUntil":"2025-10-18T09:00:00.000Z"}`), nil
		}
		return reply(http.StatusNotFound, ""), nil
	})}
	res, err := WebTokenFromCookie(context.Background(), hc, "cookie-value")
	if err != nil {
		t.Fatal(err)
	}
	steps := strings.Join(res.Steps, "\n")
	if strings.Contains(steps, "secret-token") || strings.Contains(steps, "cookie-value") {
		t.Fatalf("步骤日志包含凭据: %s", steps)
	}
	if !strings.Contains(steps, "来源 内置") || !strings.Contains(res.TOTPWarning, "已过期") {
		t.Fatalf("steps = %s, warning = %q", steps, res.TOTPWarning)
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
