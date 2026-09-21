package vc

import (
	"ashokshau/tgmusic/config"
	"ashokshau/tgmusic/src/core"
	"ashokshau/tgmusic/src/utils"
	_ "embed"
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

// player_template.png is a cleaned copy of the user's reference player card.
// Only the artwork and track-specific text are rendered dynamically.
//
//go:embed player_template.png
var playerTemplate []byte

const (
	playerWidth  = 1173
	playerHeight = 610
	fontRegular  = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	fontBold     = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"
)

// SendNowPlaying renders the supplied song inside the exact reference-style
// player card and sends that generated image with the existing controls.
func SendNowPlaying(bot *td.Client, chatID int64, oldMessage *td.Message, song *utils.CachedTrack, mode string) (*td.Message, error) {
	if song == nil {
		return nil, fmt.Errorf("track is nil")
	}

	playerImage, err := ensurePlayerImage(song)
	if err != nil {
		return nil, err
	}

	msg, err := bot.SendPhoto(chatID, td.InputFileLocal{Path: playerImage}, &td.SendPhotoOpts{
		ReplyMarkup: core.ControlButtons(mode),
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
	filter := "color=c=#0a0a0a:s=900x900,format=rgb24,drawbox=x=2:y=2:w=896:h=896:color=#171717@1:t=3,drawtext=fontfile='" + fontBold + "':text='MUSIC':fontcolor=white:fontsize=80:x=(w-text_w)/2:y=(h-text_h)/2"
	return runFFmpeg("-y", "-f", "lavfi", "-i", filter, "-frames:v", "1", "-q:v", "3", path)
}

func renderPlayerCard(thumb, output string, song *utils.CachedTrack) error {
	templatePath := filepath.Join(config.DownloadsDir, ".player_template.png")
	if err := os.WriteFile(templatePath, playerTemplate, 0644); err != nil {
		return fmt.Errorf("write player template: %w", err)
	}

	name := fitText(strings.TrimSpace(song.Name), 34)
	if name == "" {
		name = "Unknown Track"
	}
	artist := fitText(strings.TrimSpace(song.Channel), 25)
	if artist == "" {
		artist = "Unknown Artist"
	}
	platform := fitText(platformLabel(song.Platform), 16)
	if platform == "" {
		platform = "YouTube"
	}
	duration := utils.SecToMin(song.Duration)
	if duration == "" {
		duration = "0:00"
	}

	tmpDir, err := os.MkdirTemp(config.DownloadsDir, ".player-text-")
	if err != nil {
		return fmt.Errorf("create player text directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	texts := map[string]string{
		"platform":  platform,
		"name":      name,
		"artist":    artist,
		"elapsed":   "0:00",
		"remaining": "-" + duration,
	}
	paths := make(map[string]string, len(texts))
	for key, value := range texts {
		path := filepath.Join(tmpDir, key+".txt")
		if err := os.WriteFile(path, []byte(value), 0644); err != nil {
			return fmt.Errorf("write player text %s: %w", key, err)
		}
		paths[key] = path
	}

	// IMPORTANT: coordinates below are measured directly from the user's
	// supplied reference image after cropping its outer phone UI. The template
	// already contains the exact rounded card, controls, volume bar and
	// "Now Playing" styling. We only replace the artwork and dynamic metadata.
	filter := strings.Join([]string{
		"[0:v]scale=520:520:force_original_aspect_ratio=increase,crop=520:520,setsar=1[art]",
		"[1:v]scale=1173:610:force_original_aspect_ratio=disable[base]",
		"[base][art]overlay=44:46:format=auto[v0]",
		"[v0]drawtext=fontfile='" + fontRegular + "':textfile='" + paths["platform"] + "':fontcolor=#bcbcbc:fontsize=22:x=607:y=42[v1]",
		"[v1]drawtext=fontfile='" + fontBold + "':textfile='" + paths["name"] + "':fontcolor=#f4f4f4:fontsize=30:x=607:y=68[v2]",
		"[v2]drawtext=fontfile='" + fontRegular + "':textfile='" + paths["artist"] + "':fontcolor=#a9a9a9:fontsize=23:x=607:y=106[v3]",
		"[v3]drawtext=fontfile='" + fontRegular + "':textfile='" + paths["elapsed"] + "':fontcolor=#c8c8c8:fontsize=19:x=607:y=153[v4]",
		"[v4]drawtext=fontfile='" + fontRegular + "':textfile='" + paths["remaining"] + "':fontcolor=#c8c8c8:fontsize=19:x=1068:y=153[v5]",
		"[v5]format=yuvj420p[out]",
	}, ";")

	return runFFmpeg("-y", "-i", thumb, "-i", templatePath, "-filter_complex", filter, "-map", "[out]", "-frames:v", "1", "-q:v", "2", output)
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

func runFFmpeg(args ...string) error {
	cmd := exec.Command("ffmpeg", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg player render failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
