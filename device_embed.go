package main

import (
	_ "embed"
	"fmt"

	widevine "github.com/iyear/gowidevine"
)

// builtinWVD 是回落用的内置 Widevine L3 设备。
//
// 仓库中为空占位：operator 想开箱即用可把一个公开 L3 .wvd 的字节放进
// builtin.wvd（见 plugins/spotify/README）。为空时 builtinDevice 返回错误，
// 此时必须通过 [plugins.spotify] wvd_path 指定设备。
//
// 注意：公开 L3 设备随时可能被 Google/Spotify 吊销（届时音频下载开始失败），
// 且分发他人的设备密钥有合规风险——优先用自备 .wvd。
//
//go:embed builtin.wvd
var builtinWVD []byte

func builtinDevice() (*widevine.Device, error) {
	if len(builtinWVD) == 0 {
		return nil, fmt.Errorf("无内置 Widevine 设备；请设置 [plugins.spotify] wvd_path")
	}
	return LoadWVDeviceBytes(builtinWVD)
}
