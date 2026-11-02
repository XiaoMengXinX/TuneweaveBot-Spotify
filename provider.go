package main

import (
	"context"

	"github.com/XiaoMengXinX/TuneweaveBot/pluginsdk"
)

// qualityBitrate 是优先音质，0 表示取最高可用（约 AAC 256k）。
const qualityBitrate = 256

func resolve(c *pluginsdk.Context, ref Ref) (any, error) {
	if !client.configured() {
		return nil, pluginsdk.Fail("auth_required", "未配置 sp_dc，请用 /login spotify token 登录")
	}
	ctx := context.Background()
	meta, err := client.getTrack(ctx, ref.ID)
	if err != nil {
		if err == errNotFound {
			return nil, pluginsdk.Fail("not_found", "track not found")
		}
		return nil, err
	}

	if client.device == nil {
		return nil, pluginsdk.Fail("internal", "未配置 Widevine 设备(.wvd)，无法下载音频")
	}

	sink, err := c.OpenMedia("m4a")
	if err != nil {
		return nil, err
	}
	auth, err := client.webAuth(ctx)
	if err != nil {
		_ = sink.Abort()
		return nil, err
	}
	bitrate, derr := DownloadAndDecrypt(ctx, client.http, auth, client.device, ref.ID, qualityBitrate, sink)
	if derr != nil {
		_ = sink.Abort()
		return nil, pluginsdk.Fail("internal", derr.Error())
	}
	if err := sink.Close(); err != nil {
		return nil, err
	}

	track := Track{
		Ref:          makeRef(ref.ID),
		Title:        meta.Name,
		Artists:      meta.artists(),
		Album:        meta.Album.Name,
		DurationMS:   meta.DurationMs,
		CoverURL:     meta.cover(),
		AudioURL:     pluginsdk.MediaRef(sink.Token()),
		Ext:          "m4a",
		BitRate:      bitrate / 1000,
		ProviderName: displayName,
	}
	return map[string]any{"track": track}, nil
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
			Artists:    it.artists(),
			Album:      it.Album.Name,
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
		Artists: meta.artists(),
		Album:   meta.Album.Name,
	}, nil
}
