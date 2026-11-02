package main

import (
	"context"

	"github.com/XiaoMengXinX/TuneweaveBot/pluginsdk"
)

func authStatus(_ *pluginsdk.Context) (any, error) {
	if !client.configured() {
		return AuthStatus{Authenticated: false}, nil
	}
	// 能换到非匿名 token 即视为已登录。
	if _, err := client.webAuth(context.Background()); err != nil {
		return AuthStatus{Authenticated: false}, nil
	}
	return AuthStatus{Authenticated: true}, nil
}

// authBegin 只支持 token 登录：提示用户回复 sp_dc cookie（忽略 mode）。
func authBegin(_ string) (any, error) {
	return AuthChallenge{
		Type:    "token",
		Content: "请回复本条消息，发送 open.spotify.com Cookie 中的 sp_dc 值",
		Key:     "sp_dc",
	}, nil
}

// authSubmit 校验提交的 sp_dc：能换到有效 token 即成功，返回凭据供宿主持久化。
func authSubmit(_ *pluginsdk.Context, token string) (any, error) {
	prev := client.spDC
	client.spDC = token
	client.dropToken()
	if _, err := client.webAuth(context.Background()); err != nil {
		client.spDC = prev
		client.dropToken()
		return AuthState{State: "expired"}, nil
	}
	return AuthState{
		State:      "success",
		Credential: token,
		Persist:    &Persist{Section: "spotify", Key: "sp_dc"},
	}, nil
}

func authApply(c *pluginsdk.Context, credential string) error {
	client.spDC = credential
	client.dropToken()
	if credential == "" {
		return nil
	}
	if err := c.KVSet("sp_dc", []byte(credential)); err != nil {
		c.Log("warn", "保存 sp_dc 失败: "+err.Error())
	}
	return nil
}
