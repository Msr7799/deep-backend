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

// ytDlpInfo matches a subset of the yt-dlp --dump-single-json schema.
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
	formats := make([]ytFormat, 0, len(info.Formats))

	for _, f := range info.Formats {
		if strings.TrimSpace(f.URL) == "" {
			continue
		}

		vCodec := normalizeCodec(f.VCodec)
		aCodec := normalizeCodec(f.ACodec)
		isAudio := vCodec == ""
		isVideo := aCodec == ""
		if isAudio && isVideo {
			continue
		}
		if f.Ext == "mhtml" {
			continue
		}

		key := fmt.Sprintf("%s|%s|%s|%s|%d|%d", f.FormatID, f.Ext, vCodec, aCodec, f.Width, f.Height)
		if seen[key] {
			continue
		}
		seen[key] = true

		container := strings.TrimSpace(strings.ToLower(f.Ext))
		if container == "" {
			container = "mp4"
		}

		formats = append(formats, ytFormat{
			Label:      buildLabel(f),
			Container:  container,
			VCodec:     vCodec,
			ACodec:     aCodec,
			TBR:        int64(f.TBR * 1000),
			Width:      f.Width,
			Height:     f.Height,
			DurationMs: durationMs,
			IsAdaptive: isAudio || isVideo,
			URL:        f.URL,
			MimeType:   mimeFromContainer(container, isAudio, isVideo),
		})
	}

	return formats, info.Title, nil
}

func buildLabel(f ytDlpFormat) string {
	vCodec := normalizeCodec(f.VCodec)
	aCodec := normalizeCodec(f.ACodec)

	if f.Height > 0 {
		label := fmt.Sprintf("%dp", f.Height)
		if f.FPS >= 50 {
			label += fmt.Sprintf(" %.0ffps", f.FPS)
		}
		if aCodec == "" {
			label += " video-only"
		}
		return label
	}
	if vCodec == "" {
		codec := aCodec
		if codec == "" {
			codec = strings.TrimSpace(strings.ToLower(f.Ext))
		}
		if f.ABR > 0 {
			return fmt.Sprintf("Audio %s %.0fkbps", codec, f.ABR)
		}
		return fmt.Sprintf("Audio %s", codec)
	}
	if f.FormatNote != "" {
		return f.FormatNote
	}
	if f.Resolution != "" && f.Resolution != "audio only" {
		return f.Resolution
	}
	return strings.ToUpper(strings.TrimSpace(f.Ext))
}
