package vc

import (
	"ashokshau/tgmusic/config"
	"ashokshau/tgmusic/src/core"
	"ashokshau/tgmusic/src/utils"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	td "github.com/AshokShau/gotdbot"
)

// SendNowPlaying sends the player as a photo with the track thumbnail and controls.
// If a platform thumbnail cannot be downloaded, a generic music thumbnail is generated.
func SendNowPlaying(bot *td.Client, chatID int64, oldMessage *td.Message, song *utils.CachedTrack, mode string) (*td.Message, error) {
	if song == nil {
		return nil, fmt.Errorf("track is nil")
	}

	thumbnail, err := ensureTrackThumbnail(song)
	if err != nil {
		return nil, err
	}

	caption := fmt.Sprintf(
		"<u><b>| Started streaming</b></u>\n\n<b>Title:</b> <a href='%s'>%s</a>\n\n<b>Duration:</b> %s min\n<b>Requested by:</b> %s",
		td.EscapeHTML(song.URL),
		td.EscapeHTML(song.Name),
		utils.SecToMin(song.Duration),
		td.EscapeHTML(song.User),
	)

	msg, err := bot.SendPhoto(chatID, td.InputFileLocal{Path: thumbnail}, &td.SendPhotoOpts{
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

func ensureTrackThumbnail(song *utils.CachedTrack) (string, error) {
	if err := os.MkdirAll(config.DownloadsDir, 0755); err != nil {
		return "", fmt.Errorf("create downloads directory: %w", err)
	}

	id := sanitizeThumbnailID(song.TrackID)
	if id == "" {
		id = "track"
	}
	path := filepath.Join(config.DownloadsDir, "thumb_"+id+".jpg")

	if stat, err := os.Stat(path); err == nil && stat.Size() > 0 {
		return path, nil
	}

	if song.Thumbnail != "" {
		if err := downloadThumbnail(song.Thumbnail, path); err == nil {
			return path, nil
		}
	}

	if err := generateGenericThumbnail(path); err != nil {
		return "", err
	}

	return path, nil
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
	client := &http.Client{Timeout: 12 * time.Second}
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

func generateGenericThumbnail(path string) error {
	const width, height = 800, 450
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Dark music-player style background.
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{18, 18, 24, 255}}, image.Point{}, draw.Src)

	// Simple abstract record/disc and music note. No external font/assets required.
	cx, cy := width/2, height/2
	for radius := 150; radius >= 30; radius -= 20 {
		col := color.RGBA{
			uint8(35 + (150-radius)/3),
			uint8(35 + (150-radius)/4),
			uint8(48 + (150-radius)/2),
			255,
		}
		for y := cy - radius; y <= cy+radius; y++ {
			for x := cx - radius; x <= cx+radius; x++ {
				dx, dy := x-cx, y-cy
				if dx*dx+dy*dy <= radius*radius {
					img.Set(x, y, col)
				}
			}
		}
	}

	// Music note made from rectangles/circles.
	note := color.RGBA{235, 235, 245, 255}
	draw.Draw(img, image.Rect(cx+30, cy-100, cx+55, cy+65), &image.Uniform{C: note}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(cx+55, cy-100, cx+125, cy-75), &image.Uniform{C: note}, image.Point{}, draw.Src)

	for y := cy + 40; y <= cy+90; y++ {
		for x := cx + 5; x <= cx+55; x++ {
			dx, dy := x-(cx+30), y-(cy+65)
			if dx*dx+dy*dy <= 25*25 {
				img.Set(x, y, note)
			}
		}
	}
	for y := cy - 15; y <= cy+35; y++ {
		for x := cx + 100; x <= cx+150; x++ {
			dx, dy := x-(cx+125), y-(cy+10)
			if dx*dx+dy*dy <= 25*25 {
				img.Set(x, y, note)
			}
		}
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return jpeg.Encode(f, img, &jpeg.Options{Quality: 88})
}
