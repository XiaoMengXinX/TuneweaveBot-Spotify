package main

import (
	"bytes"
	"fmt"
	"os"

	widevine "github.com/iyear/gowidevine"
)

func LoadWVDeviceBytes(data []byte) (*widevine.Device, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("native: empty Widevine device data — set [plugins.spotify] wvd_path to a .wvd file (needed to decrypt Spotify AAC)")
	}
	dev, err := widevine.NewDevice(widevine.FromWVD(bytes.NewReader(data)))
	if err != nil {
		return nil, fmt.Errorf("native: load wvd: %w", err)
	}
	return dev, nil
}

// LoadWVDeviceFile 读取沙箱内的 .wvd，路径相对 [plugins] fs_dir 的只读挂载。
func LoadWVDeviceFile(path string) (*widevine.Device, error) {
	if path == "" {
		return nil, fmt.Errorf("native: no Widevine device configured — set [plugins.spotify] wvd_path to a .wvd file (needed to decrypt Spotify AAC)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("native: open wvd %q: %w", path, err)
	}
	return LoadWVDeviceBytes(data)
}
