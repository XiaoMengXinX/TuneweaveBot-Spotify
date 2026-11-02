package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/Eyevinn/mp4ff/mp4"
	widevine "github.com/iyear/gowidevine"
	"github.com/iyear/gowidevine/widevinepb"
	"google.golang.org/protobuf/proto"

	"github.com/XiaoMengXinX/TuneweaveBot/pluginsdk"
)

const spclientHost = "https://spclient.wg.spotify.com"

// scratchDir 是沙箱内唯一可写的挂载点。
const scratchDir = "/scratch"

// webHeaders 模拟 web player 的请求头，缺少时 spclient 会拒绝 web-player token。
func webHeaders(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("App-Platform", "WebPlayer")
	req.Header.Set("Spotify-App-Version", webAppVersion)
	req.Header.Set("Origin", "https://open.spotify.com")
	req.Header.Set("Referer", "https://open.spotify.com/")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-site")
	req.Header.Set("User-Agent", webUA)
}

func ApplyWebAuthHeaders(req *http.Request, auth WebAuth) {
	webHeaders(req, auth.Bearer)
	if auth.ClientToken != "" {
		req.Header.Set("Client-Token", auth.ClientToken)
	}
}

// getRawAuth 不把非 200 视为错误，由调用方按状态码处理。
func getRawAuth(ctx context.Context, hc *http.Client, url string, auth WebAuth) ([]byte, int, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	ApplyWebAuthHeaders(req, auth)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return b, resp.StatusCode, nil
}

type wvFile struct {
	FileID  string // 40-hex file id
	Format  string // storage-resolve format id: "11"=MP4_256, "10"=MP4_128
	Bitrate int    // bits per second, derived from Format for selection/labeling
}

func mp4FormatBitrate(format string) int {
	switch format {
	case "11":
		return 256000
	case "10":
		return 128000
	default:
		return 0
	}
}

// trackPlaybackResp 中 media 是以不透明 id 为键的对象。
type trackPlaybackResp struct {
	Media map[string]struct {
		Item struct {
			Manifest struct {
				FileIDsMP4 []struct {
					FileID string `json:"file_id"`
					Format string `json:"format"`
				} `json:"file_ids_mp4"`
			} `json:"manifest"`
		} `json:"item"`
	} `json:"media"`
}

// resolveMP4Files 按码率从高到低返回曲目可用的 MP4 文件。
func resolveMP4Files(ctx context.Context, hc *http.Client, auth WebAuth, trackID string) ([]wvFile, error) {
	tpURL := fmt.Sprintf("%s/track-playback/v1/media/spotify:track:%s?manifestFileFormat=file_ids_mp4", spclientHost, trackID)
	raw, status, err := getRawAuth(ctx, hc, tpURL, auth)
	if err != nil {
		return nil, fmt.Errorf("track-playback request: %w", err)
	}
	if tokenRejected(status) {
		return nil, rejectedErr("track-playback", status, raw)
	}
	if status != 200 {
		return nil, fmt.Errorf("track-playback HTTP %d: %s", status, snippet(raw))
	}
	var tp trackPlaybackResp
	if err := json.Unmarshal(raw, &tp); err != nil {
		return nil, fmt.Errorf("track-playback decode: %w (raw: %s)", err, snippet(raw))
	}
	var files []wvFile
	seen := map[string]bool{}
	for _, m := range tp.Media {
		for _, f := range m.Item.Manifest.FileIDsMP4 {
			if f.FileID == "" || seen[f.FileID] {
				continue
			}
			seen[f.FileID] = true
			files = append(files, wvFile{
				FileID:  f.FileID,
				Format:  f.Format,
				Bitrate: mp4FormatBitrate(f.Format),
			})
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no MP4 (Widevine) files in manifest (raw: %s)", snippet(raw))
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Bitrate > files[j].Bitrate })
	return files, nil
}

// selectMP4 picks the file at-or-below the preferred bitrate (0 = highest).
func selectMP4(files []wvFile, preferredBitrate int) wvFile {
	if preferredBitrate <= 0 {
		return files[0] // already sorted high→low
	}
	target := preferredBitrate * 1000
	best := files[0]
	for _, f := range files {
		if f.Bitrate <= target && f.Bitrate > 0 {
			return f
		}
		best = f
	}
	return best
}

// selectMP4Candidates 返回首选文件及更低码率的回落项：Free 账号拿不到 256k license。
func selectMP4Candidates(files []wvFile, preferredBitrate int) []wvFile {
	if len(files) == 0 {
		return nil
	}
	selected := selectMP4(files, preferredBitrate)
	candidates := []wvFile{selected}
	for _, file := range files {
		if file.FileID == selected.FileID || file.Bitrate >= selected.Bitrate {
			continue
		}
		candidates = append(candidates, file)
	}
	return candidates
}

func storageResolveMP4(ctx context.Context, hc *http.Client, auth WebAuth, fileID, format string) (string, error) {
	if format == "" {
		return "", fmt.Errorf("storage-resolve: empty format id for file %s", fileID)
	}
	srURL := fmt.Sprintf("%s/storage-resolve/v2/files/audio/interactive/%s/%s?version=10000000&product=9&platform=39&alt=json",
		spclientHost, format, fileID)
	raw, status, err := getRawAuth(ctx, hc, srURL, auth)
	if err != nil {
		return "", fmt.Errorf("storage-resolve request: %w", err)
	}
	if tokenRejected(status) {
		return "", rejectedErr("storage-resolve", status, raw)
	}
	if status != 200 {
		return "", fmt.Errorf("storage-resolve[%s] HTTP %d: %s", format, status, snippet(raw))
	}
	var sr struct {
		Result string   `json:"result"`
		CDNURL []string `json:"cdnurl"`
	}
	if json.Unmarshal(raw, &sr) != nil || len(sr.CDNURL) == 0 {
		return "", fmt.Errorf("storage-resolve[%s] no cdn urls (result=%s)", format, sr.Result)
	}
	return sr.CDNURL[0], nil
}

// buildSpotifyPSSH 由 40 位十六进制 file_id 构造 PSSH box。五个字段缺一不可，
// 否则 license 服务器返回 403。
func buildSpotifyPSSH(fileID string) ([]byte, error) {
	raw, err := hex.DecodeString(fileID)
	if err != nil || len(raw) < 16 {
		return nil, fmt.Errorf("bad file id for pssh: %q", fileID)
	}
	psshData := &widevinepb.WidevinePsshData{
		Algorithm:        widevinepb.WidevinePsshData_AESCTR.Enum(),
		KeyIds:           [][]byte{raw[:16]},
		Provider:         proto.String("spotify"),
		ContentId:        raw,
		ProtectionScheme: proto.Uint32(1667591779), // 'cenc' in little-endian
	}
	inner, err := proto.Marshal(psshData)
	if err != nil {
		return nil, fmt.Errorf("marshal pssh data: %w", err)
	}
	// Wrap in a full version-0 PSSH box: size|'pssh'|version+flags|systemid|datasize|data
	var buf bytes.Buffer
	boxLen := 32 + len(inner)
	_ = binary.Write(&buf, binary.BigEndian, uint32(boxLen))
	buf.WriteString("pssh")
	_ = binary.Write(&buf, binary.BigEndian, uint32(0)) // version 0 + flags
	buf.Write(widevineSystemID)
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(inner)))
	buf.Write(inner)
	return buf.Bytes(), nil
}

var widevineSystemID = []byte{
	0xed, 0xef, 0x8b, 0xa9, 0x79, 0xd6, 0x4a, 0xce,
	0xa3, 0xc8, 0x27, 0xdc, 0xd5, 0x1d, 0x21, 0xed,
}

func postLicense(ctx context.Context, hc *http.Client, auth WebAuth, payload []byte) ([]byte, int, http.Header, error) {
	licURL := fmt.Sprintf("%s/widevine-license/v1/audio/license", spclientHost)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, licURL, bytes.NewReader(payload))
	ApplyWebAuthHeaders(req, auth)
	// 正文是二进制 protobuf，但服务端要求 Content-Type 为 application/json。
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return body, resp.StatusCode, resp.Header, nil
}

// fetchProductState 返回账号等级（free/premium 等），用于诊断 license 失败。
func fetchProductState(ctx context.Context, hc *http.Client, auth WebAuth) (string, error) {
	url := spclientHost + "/melody/v1/product_state"
	body, status, err := getRawAuth(ctx, hc, url, auth)
	if err != nil {
		return "", err
	}
	if status != 200 {
		return "", fmt.Errorf("product_state HTTP %d", status)
	}
	var ps struct {
		Product string `json:"product"`
	}
	if err := json.Unmarshal(body, &ps); err != nil {
		return "", fmt.Errorf("product_state decode: %w", err)
	}
	return ps.Product, nil
}

// acquireContentKey 以非 privacy 模式请求 STREAMING license，返回 CONTENT key。
func acquireContentKey(ctx context.Context, hc *http.Client, auth WebAuth, device *widevine.Device, psshBytes []byte, bitrate int) ([]*widevine.Key, error) {
	pssh, err := widevine.NewPSSH(psshBytes)
	if err != nil {
		return nil, fmt.Errorf("parse pssh: %w", err)
	}
	cdm := widevine.NewCDM(device)

	challenge, parseLicense, err := cdm.GetLicenseChallenge(pssh, widevinepb.LicenseType_STREAMING, false)
	if err != nil {
		return nil, fmt.Errorf("license challenge: %w", err)
	}

	body, status, hdr, err := postLicense(ctx, hc, auth, challenge)
	if err != nil {
		return nil, fmt.Errorf("license post: %w", err)
	}
	if tokenRejected(status) {
		return nil, rejectedErr("license", status, body)
	}
	if status != 200 {
		diag := fmt.Sprintf("HTTP %d", status)
		for _, h := range []string{"Www-Authenticate", "X-Error-Code", "X-Spotify-Error", "Retry-After", "Content-Type", "Cf-Mitigated"} {
			if v := hdr.Get(h); v != "" {
				diag += fmt.Sprintf(" | %s=%s", h, v)
			}
		}
		if len(body) > 0 {
			diag += " | body=" + snippet(body)
		}
		if status == 403 && len(body) == 0 {
			if prod, perr := fetchProductState(ctx, hc, auth); perr == nil && prod != "" {
				if prod != "premium" {
					if bitrate > 128000 {
						diag += fmt.Sprintf(" — account product=%q; AAC %dk requires Premium. Retrying AAC 128k may succeed.", prod, bitrate/1000)
					} else {
						diag += fmt.Sprintf(" — account product=%q. AAC %dk is normally available to free accounts; the Widevine device may be revoked or the token may be rejected.", prod, bitrate/1000)
					}
				} else {
					diag += " — account is premium, so this is a blocklisted/revoked Widevine device. Supply a non-revoked .wvd (KeyDive from a physical Android 13/14+ device) via [plugins.spotify] wvd_path."
				}
			} else {
				diag += " — bare 403; the requested quality may be unavailable for this account, or the Widevine device/token was rejected."
			}
		}
		return nil, fmt.Errorf("license %s", diag)
	}
	keys, err := parseLicense(body)
	if err != nil {
		return nil, fmt.Errorf("parse license: %w", err)
	}
	return keys, nil
}

// downloadAndDecryptMP4 由宿主下载密文到 scratch，在插件内解密到另一个 scratch
// 文件，完全成功后才拷贝到 dst，保证失败时 dst 没有残留，可换码率重试。
func downloadAndDecryptMP4(c *pluginsdk.Context, keys []*widevine.Key, cdnURL string, opts *pluginsdk.DownloadOptions, dst io.Writer) error {
	dl, err := c.DownloadToScratch(cdnURL, opts)
	if err != nil {
		return fmt.Errorf("cdn download: %w", err)
	}
	defer os.Remove(dl.Path)

	tmp, err := os.Open(dl.Path)
	if err != nil {
		return fmt.Errorf("open scratch file: %w", err)
	}
	defer tmp.Close()

	out, err := os.CreateTemp(scratchDir, "spotify-aac-*.mp4")
	if err != nil {
		return fmt.Errorf("create output scratch file: %w", err)
	}
	defer os.Remove(out.Name())
	defer out.Close()

	bw := bufio.NewWriterSize(out, 512<<10)
	if err := decryptMP4Stream(bufio.NewReaderSize(tmp, 512<<10), keys, bw); err != nil {
		return fmt.Errorf("decrypt mp4: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("write decrypted output: %w", err)
	}
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind output scratch file: %w", err)
	}
	// 大块拷贝：dst 是 media sink，每次 Write 都是一次 base64 host-call，
	// 用 512KB 缓冲把单轨的 host-call 次数从数百降到十几次。
	if _, err := io.CopyBuffer(dst, out, make([]byte, 512<<10)); err != nil {
		return fmt.Errorf("write decrypted output: %w", err)
	}
	return nil
}

// decryptMP4Stream 逐个 moof+mdat 分片解密分片 MP4，内存峰值只有一个分片，与音轨时长无关。
// sidx 记录的是密文分片大小，解密移除 senc/saiz/saio 后失效，因此不输出。
func decryptMP4Stream(r io.Reader, keys []*widevine.Key, w io.Writer) error {
	var key []byte
	for _, k := range keys {
		if k.Type == widevinepb.License_KeyContainer_CONTENT {
			key = k.Key
			break
		}
	}
	if key == nil {
		return errors.New("license has no content key")
	}

	init := mp4.NewMP4Init()
	var (
		di      mp4.DecryptInfo
		frag    *mp4.Fragment
		pending []mp4.Box // moov 之后、moof 之前的盒子（emsg、prft 等），归入下一个分片
		pos     uint64
	)
	for {
		box, err := mp4.DecodeBox(pos, r)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		pos += box.Size()

		switch b := box.(type) {
		case *mp4.MoovBox:
			init.AddChild(b)
			if di, err = mp4.DecryptInit(init); err != nil {
				return fmt.Errorf("decrypt init: %w", err)
			}
			if err := init.Encode(w); err != nil {
				return err
			}
		case *mp4.SidxBox, *mp4.MfraBox:
		case *mp4.StypBox:
			if err := b.Encode(w); err != nil {
				return err
			}
		case *mp4.MoofBox:
			if init.Moov == nil {
				return errors.New("moof before moov")
			}
			frag = mp4.NewFragment()
			for _, p := range pending {
				frag.AddChild(p)
			}
			pending = pending[:0]
			frag.AddChild(b)
		case *mp4.MdatBox:
			if frag == nil {
				return errors.New("mdat without moof")
			}
			frag.AddChild(b)
			// 允许未加密分片（无 senc）与加密分片混排
			if err := mp4.DecryptFragment(frag, di, key); err != nil && err.Error() != "no senc box in traf" {
				return fmt.Errorf("decrypt fragment at %d: %w", b.StartPos, err)
			}
			if err := frag.Encode(w); err != nil {
				return err
			}
			frag = nil
		default:
			if init.Moov == nil {
				init.AddChild(b)
			} else {
				pending = append(pending, b)
			}
		}
	}
	if init.Moov == nil {
		return errors.New("no moov box")
	}
	return nil
}

func describeFiles(files []wvFile) string {
	parts := make([]string, 0, len(files))
	for _, f := range files {
		parts = append(parts, fmt.Sprintf("%dk(format %s)", f.Bitrate/1000, f.Format))
	}
	return strings.Join(parts, ", ")
}

// DownloadAndDecrypt 把解密后的 MP4(AAC) 写入 dst，返回实际码率（bps）。
// preferredBitrate 单位 kbps，0 表示最高；失败时依次尝试更低码率。
func DownloadAndDecrypt(ctx context.Context, c *pluginsdk.Context, hc *http.Client, auth WebAuth, device *widevine.Device, trackID string, preferredBitrate int, opts *pluginsdk.DownloadOptions, dst io.Writer) (bitrate int, err error) {
	files, err := resolveMP4Files(ctx, hc, auth, trackID)
	if err != nil {
		return 0, err
	}

	candidates := selectMP4Candidates(files, preferredBitrate)
	c.Log("debug", fmt.Sprintf("spotify: %s 可用 MP4 %d 个，按顺序尝试 %s", trackID, len(files), describeFiles(candidates)))
	var attemptErrs []error
	defer func() {
		// 回落到较低码率时记录前面失败的原因
		if err == nil && len(attemptErrs) > 0 {
			c.Log("debug", fmt.Sprintf("spotify: %s 使用 %dk，跳过的码率: %v", trackID, bitrate/1000, errors.Join(attemptErrs...)))
		}
	}()
	for _, file := range candidates {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		cdnURL, resolveErr := storageResolveMP4(ctx, hc, auth, file.FileID, file.Format)
		if resolveErr != nil {
			attemptErrs = append(attemptErrs, fmt.Errorf("%dk storage-resolve: %w", file.Bitrate/1000, resolveErr))
			continue
		}
		psshBytes, psshErr := buildSpotifyPSSH(file.FileID)
		if psshErr != nil {
			attemptErrs = append(attemptErrs, fmt.Errorf("%dk build pssh: %w", file.Bitrate/1000, psshErr))
			continue
		}
		keys, keyErr := acquireContentKey(ctx, hc, auth, device, psshBytes, file.Bitrate)
		if keyErr != nil {
			attemptErrs = append(attemptErrs, fmt.Errorf("%dk license: %w", file.Bitrate/1000, keyErr))
			continue
		}
		if decryptErr := downloadAndDecryptMP4(c, keys, cdnURL, opts, dst); decryptErr != nil {
			attemptErrs = append(attemptErrs, fmt.Errorf("%dk decrypt: %w", file.Bitrate/1000, decryptErr))
			continue
		}
		return file.Bitrate, nil
	}

	return 0, fmt.Errorf("widevine download failed for all quality candidates: %w", errors.Join(attemptErrs...))
}
