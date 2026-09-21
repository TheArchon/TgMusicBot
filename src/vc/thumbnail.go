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
	name := strings.TrimSpace(song.Name)
	if name == "" {
		name = "Unknown Track"
	}
	artist := strings.TrimSpace(song.Channel)
	if artist == "" {
		artist = "Unknown Artist"
	}
	platform := platformLabel(song.Platform)
	duration := utils.SecToMin(song.Duration)
	if duration == "" {
		duration = "0:00"
	}

	name = fitText(name, 22)
	artist = fitText(artist, 18)
	platform = fitText(platform, 18)

	tmpDir, err := os.MkdirTemp(config.DownloadsDir, ".player-text-")
	if err != nil {
		return fmt.Errorf("create player text directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	texts := map[string]string{
		"platform": platform, "name": name, "artist": artist,
		"elapsed": "0:00", "remaining": "-" + duration,
		"now": "Now Playing", "volume": "",
	}
	paths := make(map[string]string, len(texts))
	for key, value := range texts {
		path := filepath.Join(tmpDir, key+".txt")
		if err := os.WriteFile(path, []byte(value), 0644); err != nil {
			return fmt.Errorf("write player text %s: %w", key, err)
		}
		paths[key] = path
	}

	filter := strings.Join([]string{
		"color=c=#070707:s=1280x720:d=1[bg]",
		"[0:v]scale=520:520:force_original_aspect_ratio=increase,crop=520:520,setsar=1[art]",
		"[bg][art]overlay=70:100:format=auto[v0]",
		"[v0]drawbox=x=68:y=98:w=524:h=524:color=#101010@1:t=2[v1]",
		"[v1]drawbox=x=625:y=72:w=585:h=576:color=#070707@1:t=fill[v2]",
		"[v2]drawtext=fontfile='" + fontRegular + "':textfile='" + paths["platform"] + "':fontcolor=#a7a7a7:fontsize=27:x=660:y=92[v3]",
		"[v3]drawtext=fontfile='" + fontBold + "':textfile='" + paths["name"] + "':fontcolor=#f4f4f4:fontsize=34:x=660:y=135[v4]",
		"[v4]drawtext=fontfile='" + fontRegular + "':textfile='" + paths["artist"] + "':fontcolor=#a7a7a7:fontsize=27:x=660:y=185[v5]",
		"[v5]drawtext=fontfile='" + fontRegular + "':textfile='" + paths["elapsed"] + "':fontcolor=#bdbdbd:fontsize=20:x=660:y=228[v6]",
		"[v6]drawtext=fontfile='" + fontRegular + "':textfile='" + paths["remaining"] + "':fontcolor=#bdbdbd:fontsize=20:x=1150:y=228[v7]",
		"[v7]drawbox=x=730:y=237:w=400:h=5:color=#454545:t=fill[v8]",
		"[v8]drawbox=x=730:y=237:w=125:h=5:color=#e6e6e6:t=fill[v9]",
		"[v9]drawtext=fontfile='" + fontRegular + "':textfile='" + paths["now"] + "':fontcolor=#eeeeee:fontsize=24:x=660:y=280[v10]",
		"[v10]drawtext=fontfile='" + fontRegular + "':text='♡':fontcolor=#eeeeee:fontsize=42:x=1148:y=270[v11]",
		"[v11]drawtext=fontfile='" + fontRegular + "':text='|◀':fontcolor=#f5f5f5:fontsize=45:x=730:y=360[v12]",
		"[v12]drawtext=fontfile='" + fontBold + "':text='Ⅱ':fontcolor=#f5f5f5:fontsize=48:x=900:y=360[v13]",
		"[v13]drawtext=fontfile='" + fontRegular + "':text='▶|':fontcolor=#f5f5f5:fontsize=45:x=1060:y=360[v14]",
		"[v14]drawtext=fontfile='" + fontRegular + "':text='◎':fontcolor=#d8d8d8:fontsize=36:x=1150:y=365[v15]",
		"[v15]drawbox=x=705:y=543:w=425:h=5:color=#454545:t=fill[v16]",
		"[v16]drawbox=x=705:y=543:w=145:h=5:color=#e6e6e6:t=fill[v17]",
		"[v17]drawtext=fontfile='" + fontRegular + "':text='V':fontcolor=#d8d8d8:fontsize=24:x=1148:y=527[v18]",
		"[v18]format=yuvj420p[out]",
	}, ";")
	return runFFmpeg("-y", "-i", thumb, "-filter_complex", filter, "-map", "[out]", "-frames:v", "1", "-q:v", "2", output)
}

func fitText(s string, max int) string {
	s = strings.TrimSpace(s)
	if max < 2 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func platformLabel(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case utils.YouTube:
		return "YouTube"
	case utils.Spotify:
		return "Spotify"
	case utils.JioSaavn:
		return "JioSaavn"
	case utils.Apple:
		return "Apple Music"
	case utils.SoundCloud:
		return "SoundCloud"
	case utils.Telegram:
		return "Telegram"
	default:
		if platform == "" {
			return "Music"
		}
		return platform
	}
}

func ffmpegText(s string) string {
	// drawtext parses these characters even when passed as an argv item.
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, ":", "\\\\:")
	s = strings.ReplaceAll(s, "%", "\\\\%")
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
