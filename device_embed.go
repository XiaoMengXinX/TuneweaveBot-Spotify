package main

import (
	_ "embed"
	"fmt"

	widevine "github.com/iyear/gowidevine"
)

// builtinWVD 是回落用的内置 Widevine L3 设备，开箱即用。
//
// operator 想换成自备设备，在 [plugins.spotify] 设 wvd_path 覆盖即可；留空则用
// 这个内置设备。为空（被清掉重编）时 builtinDevice 返回错误。
//
// 注意：公开 L3 设备随时可能被 Google/Spotify 吊销（届时音频下载开始失败），
// 分发设备密钥也有合规风险——需要稳定时优先用自备 .wvd。
//
//go:embed builtin.wvd
var builtinWVD []byte

func builtinDevice() (*widevine.Device, error) {
	if len(builtinWVD) == 0 {
		return nil, fmt.Errorf("无内置 Widevine 设备；请设置 [plugins.spotify] wvd_path")
	}
	return LoadWVDeviceBytes(builtinWVD)
}
