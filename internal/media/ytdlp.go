package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// newExecCmd builds an exec.Cmd safely.
func newExecCmd(ctx context.Context, bin string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, bin, args...)
}

// ytDlpInfo matches the yt-dlp --dump-json schema (partial).
type ytDlpInfo struct {
	Title    string        `json:"title"`
	Duration float64       `json:"duration"`
	Formats  []ytDlpFormat `json:"formats"`
}

type ytDlpFormat struct {
	FormatID   string  `json:"format_id"`
	Ext        string  `json:"ext"`
	Resolution string  `json:"resolution"`
	FPS        float64 `json:"fps"`
	VCodec     string  `json:"vcodec"`
	ACodec     string  `json:"acodec"`
	TBR        float64 `json:"tbr"`
	VBR        float64 `json:"vbr"`
	ABR        float64 `json:"abr"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	URL        string  `json:"url"`
	FormatNote string  `json:"format_note"`
	Protocol   string  `json:"protocol"`
}

// parseYtDlpJSON converts raw yt-dlp JSON into our ytFormat slice.
func parseYtDlpJSON(raw []byte) ([]ytFormat, string, error) {
	var info ytDlpInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, "", fmt.Errorf("parse yt-dlp json: %w", err)
	}

	durationMs := int64(info.Duration * 1000)
	seen := map[string]bool{}
	var formats []ytFormat

	for _, f := range info.Formats {
		if f.URL == "" {
			continue
		}
		// Deduplicate by resolution+codec combo
		key := fmt.Sprintf("%s|%s|%s", f.Resolution, f.VCodec, f.ACodec)
		if seen[key] {
			continue
		}
		seen[key] = true

		isAudio := f.VCodec == "none" || f.VCodec == ""
		isVideo := f.ACodec == "none" || f.ACodec == ""
		isAdaptive := isAudio || isVideo

		label := buildLabel(f)
		tbr := int64(f.TBR * 1000)

		container := f.Ext
		mimeType := mimeFromContainer(container, isAudio, isVideo)

		formats = append(formats, ytFormat{
			Label:      label,
			Container:  container,
			VCodec:     f.VCodec,
			ACodec:     f.ACodec,
			TBR:        tbr,
			Width:      f.Width,
			Height:     f.Height,
			DurationMs: durationMs,
			IsAdaptive: isAdaptive,
			URL:        f.URL,
			MimeType:   mimeType,
		})
	}

	return formats, info.Title, nil
}

func buildLabel(f ytDlpFormat) string {
	if f.Height > 0 {
		label := fmt.Sprintf("%dp", f.Height)
		if f.FPS >= 50 {
			label += fmt.Sprintf("%.0ffps", f.FPS)
		}
		return label
	}
	if f.FormatNote != "" {
		return f.FormatNote
	}
	if f.VCodec == "none" || f.VCodec == "" {
		// Audio only
		codec := strings.Split(f.ACodec, ".")[0]
		if f.ABR > 0 {
			return fmt.Sprintf("Audio %s %.0fkbps", codec, f.ABR)
		}
		return fmt.Sprintf("Audio %s", codec)
	}
	return strings.ToUpper(f.Ext)
}
