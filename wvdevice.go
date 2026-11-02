package main

import (
	"bytes"
	"fmt"
	"os"

	widevine "github.com/iyear/gowidevine"
)

func LoadWVDeviceBytes(data []byte) (*widevine.Device, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("Widevine 设备数据为空")
	}
	dev, err := widevine.NewDevice(widevine.FromWVD(bytes.NewReader(data)))
	if err != nil {
		return nil, fmt.Errorf("解析 .wvd 失败: %w", err)
	}
	return dev, nil
}

// LoadWVDeviceFile 读取沙箱内的 .wvd，路径相对 [plugins] fs_dir 的只读挂载。
func LoadWVDeviceFile(path string) (*widevine.Device, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 .wvd %q 失败: %w", path, err)
	}
	return LoadWVDeviceBytes(data)
}
