package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
)

const base62Charset = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// tagMetadata 与宿主 tagging.Metadata 结构一致，对应 tag_format "id3"。
type tagMetadata struct {
	Title        string            `json:"title"`
	Artists      []string          `json:"artists"`
	Album        string            `json:"album"`
	AlbumArtists []string          `json:"album_artists,omitempty"`
	Date         string            `json:"date,omitempty"`
	TrackNumber  int               `json:"track_number,omitempty"`
	TrackTotal   int               `json:"track_total,omitempty"`
	DiscNumber   int               `json:"disc_number,omitempty"`
	Composers    []string          `json:"composers,omitempty"`
	Copyright    string            `json:"copyright,omitempty"`
	Custom       map[string]string `json:"custom,omitempty"`
}

// spclientTrack 是 metadata/4/track 中用于标签的字段。
type spclientTrack struct {
	Number     int `json:"number"`
	DiscNumber int `json:"disc_number"`
	Album      struct {
		Label  string `json:"label"`
		Artist []struct {
			Name string `json:"name"`
		} `json:"artist"`
	} `json:"album"`
	ExternalID []struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"external_id"`
}

type trackCredits struct {
	RoleCredits []struct {
		RoleTitle string `json:"roleTitle"`
		Artists   []struct {
			Name string `json:"name"`
		} `json:"artists"`
	} `json:"roleCredits"`
}

// buildTags 以 GQL 元数据为基础，补充 spclient 的专辑艺人、厂牌、ISRC 等与作曲者。
// 补充信息只影响标签完整度，获取失败时忽略。
func buildTags(ctx context.Context, auth WebAuth, meta *apiTrack) tagMetadata {
	tags := tagMetadata{
		Title:       meta.Name,
		Artists:     meta.Artists,
		Album:       meta.AlbumName,
		Date:        meta.ReleaseDate,
		TrackNumber: meta.TrackNumber,
		TrackTotal:  meta.TrackTotal,
		Copyright:   meta.Copyright,
	}
	if gid, err := spotifyIDToHex(meta.ID); err == nil {
		var st spclientTrack
		if getJSON(ctx, spclientHost+"/metadata/4/track/"+gid+"?market=from_token", auth, &st) == nil {
			for _, a := range st.Album.Artist {
				tags.AlbumArtists = append(tags.AlbumArtists, a.Name)
			}
			if tags.TrackNumber == 0 {
				tags.TrackNumber = st.Number
			}
			tags.DiscNumber = st.DiscNumber
			custom := map[string]string{"label": st.Album.Label}
			for _, id := range st.ExternalID {
				switch t := strings.ToUpper(id.Type); t {
				case "ISRC", "UPC", "EAN":
					custom[t] = id.ID
				}
			}
			tags.Custom = custom
		}
	}
	var credits trackCredits
	if getJSON(ctx, spclientHost+"/track-credits-view/v0/experimental/"+meta.ID+"/credits", auth, &credits) == nil {
		for _, r := range credits.RoleCredits {
			if r.RoleTitle == "Writers" {
				for _, a := range r.Artists {
					tags.Composers = append(tags.Composers, a.Name)
				}
			}
		}
	}
	return tags
}

func getJSON(ctx context.Context, url string, auth WebAuth, out any) error {
	body, status, err := getRawAuth(ctx, client.http, url, auth)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, status)
	}
	return json.Unmarshal(body, out)
}

// spotifyIDToHex 把 22 位 base62 ID 转为 32 位十六进制 GID。
func spotifyIDToHex(id string) (string, error) {
	n := new(big.Int)
	base := big.NewInt(62)
	for i := 0; i < len(id); i++ {
		v := strings.IndexByte(base62Charset, id[i])
		if v < 0 {
			return "", fmt.Errorf("invalid spotify id %q", id)
		}
		n.Mul(n, base).Add(n, big.NewInt(int64(v)))
	}
	return fmt.Sprintf("%032x", n), nil
}
