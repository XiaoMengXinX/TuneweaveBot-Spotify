package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/XiaoMengXinX/TuneweaveBot/pluginsdk"
)

// qualityBitrate 是优先音质，0 表示取最高可用（约 AAC 256k）。
const qualityBitrate = 256

func resolve(c *pluginsdk.Context, ref Ref) (any, error) {
	if !client.configured() {
		return nil, pluginsdk.Fail("auth_required", "未配置 sp_dc，请用 /login spotify token 登录")
	}
	if client.device == nil {
		return nil, pluginsdk.Fail("internal", "Widevine 设备不可用，无法下载音频")
	}
	ctx := context.Background()
	meta, err := client.getTrack(ctx, ref.ID)
	if err != nil {
		if err == errNotFound {
			return nil, pluginsdk.Fail("not_found", "track not found")
		}
		return nil, err
	}

	sink, err := c.OpenMedia("m4a")
	if err != nil {
		return nil, err
	}
	opts := &pluginsdk.DownloadOptions{Title: meta.Name, Album: meta.AlbumName}
	out := &countingWriter{w: sink}
	// token 被拒只会发生在 license 之前，此时尚未写入 sink，可以安全重试
	var bitrate int
	derr := client.withToken(ctx, func(auth WebAuth) (err error) {
		bitrate, err = DownloadAndDecrypt(ctx, c, client.http, auth, client.device, ref.ID, qualityBitrate, opts, out)
		return err
	})
	if derr != nil {
		_ = sink.Abort()
		if errors.Is(derr, errSpDCInvalid) {
			return nil, derr
		}
		return nil, pluginsdk.Fail("internal", derr.Error())
	}
	if err := sink.Close(); err != nil {
		return nil, err
	}
	// 与其他 provider 一致报告平均码率（bps）；缺少时长时用格式标称码率
	if meta.DurationMs > 0 {
		bitrate = int(out.n * 8 * 1000 / int64(meta.DurationMs))
	}
	// 标签补充信息尽力获取，token 不可用时只缺对应字段
	auth, _ := client.webAuth(ctx)
	tagPayload, err := json.Marshal(buildTags(ctx, auth, meta))
	if err != nil {
		return nil, err
	}

	// 不设 Size：宿主以落盘后的明文文件大小为准。
	track := Track{
		Ref:          makeRef(ref.ID),
		Title:        meta.Name,
		Artists:      meta.Artists,
		Album:        meta.AlbumName,
		DurationMS:   meta.DurationMs,
		CoverURL:     meta.CoverURL,
		AudioURL:     pluginsdk.MediaRef(sink.Token()),
		Ext:          "m4a",
		BitRate:      bitrate,
		ProviderName: displayName,
		TagFormat:    "id3",
		TagPayload:   tagPayload,
	}
	return map[string]any{"track": track}, nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func metadata(ref Ref) (any, error) {
	if !client.configured() {
		return nil, pluginsdk.Fail("auth_required", "未配置 sp_dc")
	}
	meta, err := client.getTrack(context.Background(), ref.ID)
	if err != nil {
		if err == errNotFound {
			return nil, pluginsdk.Fail("not_found", "track not found")
		}
		return nil, err
	}
	return map[string]any{"track": TrackSummary{
		Ref:        makeRef(ref.ID),
		Title:      meta.Name,
		Artists:    meta.Artists,
		Album:      meta.AlbumName,
		DurationMS: meta.DurationMs,
	}}, nil
}

func searchTracks(_ *pluginsdk.Context, keyword string, limit, offset int) (any, error) {
	if !client.configured() {
		return nil, pluginsdk.Fail("auth_required", "未配置 sp_dc，请用 /login spotify token 登录")
	}
	items, total, err := client.search(context.Background(), keyword, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]TrackSummary, 0, len(items))
	for _, it := range items {
		out = append(out, TrackSummary{
			Ref:        makeRef(it.ID),
			Title:      it.Name,
			Artists:    it.Artists,
			Album:      it.AlbumName,
			DurationMS: it.DurationMs,
		})
	}
	return map[string]any{"items": out, "total": total}, nil
}

func lyricsFor(_ *pluginsdk.Context, ref Ref) (any, error) {
	if !client.configured() {
		return nil, pluginsdk.Fail("auth_required", "未配置 sp_dc")
	}
	ctx := context.Background()
	meta, err := client.getTrack(ctx, ref.ID)
	if err != nil {
		if err == errNotFound {
			return nil, pluginsdk.Fail("not_found", "track not found")
		}
		return nil, err
	}
	lrc, err := client.lyrics(ctx, ref.ID)
	if err != nil {
		return nil, err
	}
	return Lyric{
		LRC:     lrc,
		Title:   meta.Name,
		Artists: meta.Artists,
		Album:   meta.AlbumName,
	}, nil
}
