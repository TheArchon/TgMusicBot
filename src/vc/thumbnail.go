package vc

import (
	"ashokshau/tgmusic/config"
	"ashokshau/tgmusic/src/core"
	"ashokshau/tgmusic/src/utils"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	td "github.com/AshokShau/gotdbot"
)

const (
	playerWidth  = 1600
	playerHeight = 900
	fontRegular  = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	fontBold     = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"
)

// SendNowPlaying builds a cinematic player card from the track thumbnail and
// sends that generated image instead of sending the raw thumbnail directly.
// The Telegram inline controls are kept below the image.
func SendNowPlaying(bot *td.Client, chatID int64, oldMessage *td.Message, song *utils.CachedTrack, mode string) (*td.Message, error) {
	if song == nil {
		return nil, fmt.Errorf("track is nil")
	}

	playerImage, err := ensurePlayerImage(song)
	if err != nil {
		return nil, err
	}

	caption := fmt.Sprintf(
		"<b>Now Playing</b>\n<b>Title:</b> <a href='%s'>%s</a>\n<b>Duration:</b> %s\n<b>Requested by:</b> %s",
		td.EscapeHTML(song.URL),
		td.EscapeHTML(song.Name),
		utils.SecToMin(song.Duration),
		td.EscapeHTML(song.User),
	)

	msg, err := bot.SendPhoto(chatID, td.InputFileLocal{Path: playerImage}, &td.SendPhotoOpts{
		Caption:               caption,
		ParseMode:             "HTML",
		ShowCaptionAboveMedia: false,
		ReplyMarkup:           core.ControlButtons(mode),
	})
	if err != nil {
		return nil, err
	}

	if oldMessage != nil {
		_ = oldMessage.Delete(bot, true)
	}

	return msg, nil
}

func ensurePlayerImage(song *utils.CachedTrack) (string, error) {
	if err := os.MkdirAll(config.DownloadsDir, 0755); err != nil {
		return "", fmt.Errorf("create downloads directory: %w", err)
	}

	id := sanitizeThumbnailID(song.TrackID)
	if id == "" {
		id = sanitizeThumbnailID(song.Name)
	}
	if id == "" {
		id = "track"
	}

	output := filepath.Join(config.DownloadsDir, "player_"+id+".jpg")
	if stat, err := os.Stat(output); err == nil && stat.Size() > 0 {
		return output, nil
	}

	thumb := filepath.Join(config.DownloadsDir, "thumb_"+id+".jpg")
	if stat, err := os.Stat(thumb); err != nil || stat.Size() == 0 {
		if song.Thumbnail != "" {
			if err := downloadThumbnail(song.Thumbnail, thumb); err != nil {
				if err := generateFallbackThumbnail(thumb); err != nil {
					return "", err
				}
			}
		} else if err := generateFallbackThumbnail(thumb); err != nil {
			return "", err
		}
	}

	if err := renderPlayerCard(thumb, output, song); err != nil {
		return "", err
	}
	return output, nil
}

func sanitizeThumbnailID(id string) string {
	id = filepath.Base(strings.TrimSpace(id))
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func downloadThumbnail(rawURL, path string) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("thumbnail http status: %s", resp.Status)
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, io.LimitReader(resp.Body, 8<<20))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func generateFallbackThumbnail(path string) error {
	filter := "color=c=#111318:s=800x450,format=rgb24,drawbox=x=24:y=24:w=752:h=402:color=#252832@1:t=4,drawtext=fontfile='" + fontBold + "':text='MUSIC':fontcolor=white:fontsize=72:x=(w-text_w)/2:y=(h-text_h)/2"
	return runFFmpeg("-y", "-f", "lavfi", "-i", filter, "-frames:v", "1", "-q:v", "3", path)
}

func renderPlayerCard(thumb, output string, song *utils.CachedTrack) error {
	name := song.Name
	if name == "" {
		name = "Unknown Track"
	}
	artist := song.Channel
	if artist == "" {
		artist = "Unknown Artist"
	}
	platform := strings.TrimSpace(song.Platform)
	if platform == "" {
		platform = "Music"
	}
	requester := song.User
	if requester == "" {
		requester = "Unknown"
	}

	name = shortenText(name, 34)
	artist = shortenText(artist, 30)
	requester = shortenText(requester, 24)
	platform = shortenText(platform, 18)
	duration := utils.SecToMin(song.Duration)
	if duration == "" {
		duration = "0:00"
	}

	// The card intentionally mirrors the supplied reference: album artwork on
	// the left and a clean black player interface on the right.
	filter := strings.Join([]string{
		"[0:v]scale=700:700:force_original_aspect_ratio=increase,crop=700:700,setsar=1,eq=contrast=1.05:saturation=0.82,boxblur=0.35[art]",
		"color=c=#070809:s=1600x900:d=1[bg]",
		"[bg][art]overlay=70:100:format=auto[v0]",
		"[v0]drawbox=x=68:y=98:w=704:h=704:color=#34363a@0.95:t=3[v1]",
		"[v1]drawtext=fontfile='" + fontRegular + "':text='" + ffmpegText(platform) + "':fontcolor=#a8abb0:fontsize=30:x=820:y=118[v2]",
		"[v2]drawtext=fontfile='" + fontBold + "':text='" + ffmpegText(name) + "':fontcolor=#ffffff:fontsize=46:x=820:y=168[v3]",
		"[v3]drawtext=fontfile='" + fontRegular + "':text='" + ffmpegText(artist) + "':fontcolor=#aeb2b8:fontsize=34:x=820:y=228[v4]",
		"[v4]drawtext=fontfile='" + fontRegular + "':text='0:00':fontcolor=#c7c9cc:fontsize=25:x=820:y=292[v5]",
		"[v5]drawtext=fontfile='" + fontRegular + "':text='-" + ffmpegText(duration) + "':fontcolor=#c7c9cc:fontsize=25:x=1410:y=292[v6]",
		"[v6]drawbox=x=900:y=304:w=500:h=7:color=#41444a:t=fill[v7]",
		"[v7]drawbox=x=900:y=304:w=150:h=7:color=#eeeeee:t=fill[v8]",
		"[v8]drawtext=fontfile='" + fontRegular + "':text='Now Playing':fontcolor=#f0f0f0:fontsize=30:x=820:y=370[v9]",
		"[v9]drawtext=fontfile='" + fontRegular + "':text='" + ffmpegText("Requested by: "+requester) + "':fontcolor=#c4c6ca:fontsize=27:x=820:y=655[v10]",
		"[v10]drawtext=fontfile='" + fontBold + "':text='|<':fontcolor=#ffffff:fontsize=55:x=900:y=475[v11]",
		"[v11]drawtext=fontfile='" + fontBold + "':text='||':fontcolor=#ffffff:fontsize=55:x=1115:y=475[v12]",
		"[v12]drawtext=fontfile='" + fontBold + "':text='>|':fontcolor=#ffffff:fontsize=55:x=1320:y=475[v13]",
		"[v13]drawtext=fontfile='" + fontRegular + "':text='VOLUME':fontcolor=#d8dadd:fontsize=30:x=820:y=735[v14]",
		"[v14]drawbox=x=900:y=742:w=500:h=7:color=#41444a:t=fill[v15]",
		"[v15]drawbox=x=900:y=742:w=180:h=7:color=#eeeeee:t=fill[v16]",
		"[v16]drawtext=fontfile='" + fontRegular + "':text='♡':fontcolor=#d8dadd:fontsize=48:x=1450:y=370[v17]",
		"[v17]drawtext=fontfile='" + fontRegular + "':text='◉':fontcolor=#d8dadd:fontsize=38:x=1455:y=490[v18]",
		"[v18]format=yuvj420p[out]",
	}, ";")

	return runFFmpeg("-y", "-i", thumb, "-filter_complex", filter, "-map", "[out]", "-frames:v", "1", "-q:v", "2", output)
}

func ffmpegText(s string) string {
	// drawtext parses these characters even when passed as an argv item.
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, ":", "\\:")
	s = strings.ReplaceAll(s, "%", "\\%")
	return s
}

func shortenText(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func runFFmpeg(args ...string) error {
	cmd := exec.Command("ffmpeg", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg player render failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
