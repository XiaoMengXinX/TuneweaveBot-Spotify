package main

import (
	"bytes"
	"fmt"
	"os"

	widevine "github.com/iyear/gowidevine"
)

// Widevine L3 device loading for Spotify's MP4/AAC (CENC) decryption.
//
// This package deliberately does NOT embed an RSA private key. Decrypting
// Spotify's Widevine-protected audio requires a Widevine L3 device (.wvd), and
// embedding a private key is a credential-leakage risk and a decision the
// operator should make explicitly. The host supplies the device bytes (or a
// path) via the spotify plugin's `wvd_path` config option.

// LoadWVDeviceBytes parses raw .wvd bytes into a gowidevine L3 device.
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

// LoadWVDeviceFile reads a .wvd file and loads it via LoadWVDeviceBytes. In the
// wasm sandbox a host-side file read may be unavailable; the host normally
// passes the device bytes through config, but this variant is kept for parity
// with the native reference.
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
