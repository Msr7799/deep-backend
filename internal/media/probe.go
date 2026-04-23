package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ProbeResult holds the output of ffprobe for a given URL/file.
type ProbeResult struct {
	Format  FormatInfo   `json:"format"`
	Streams []StreamInfo `json:"streams"`
}

type FormatInfo struct {
	Filename   string            `json:"filename"`
	FormatName string            `json:"format_name"`
	Duration   string            `json:"duration"`
	Size       string            `json:"size"`
	BitRate    string            `json:"bit_rate"`
	Tags       map[string]string `json:"tags"`
}

type StreamInfo struct {
	Index      int               `json:"index"`
	CodecName  string            `json:"codec_name"`
	CodecType  string            `json:"codec_type"`
	Width      int               `json:"width"`
	Height     int               `json:"height"`
	BitRate    string            `json:"bit_rate"`
	SampleRate string            `json:"sample_rate"`
	Channels   int               `json:"channels"`
	DurationTS int64             `json:"duration_ts"`
	Tags       map[string]string `json:"tags"`
}

// Prober wraps ffprobe to extract media metadata.
type Prober struct {
	ffprobePath string
}

func NewProber(ffprobePath string) *Prober {
	return &Prober{ffprobePath: ffprobePath}
}

// Probe runs ffprobe against a URL or local path and returns structured results.
// For YouTube page URLs, callers should use yt-dlp first instead of ffprobe.
func (p *Prober) Probe(ctx context.Context, input string) (*ProbeResult, error) {
	args := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-user_agent", "Mozilla/5.0 (Linux; Android 14; Pixel 9) AppleWebKit/537.36",
		input,
	}

	cmd := exec.CommandContext(ctx, p.ffprobePath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("ffprobe exit %d: %s", exitErr.ExitCode(), strings.TrimSpace(string(out)))
		}
		return nil, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(string(out)))
	}

	var result ProbeResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("ffprobe parse: %w", err)
	}
	return &result, nil
}

func DurationMs(pr *ProbeResult) int64 {
	if pr == nil {
		return 0
	}
	if d, err := strconv.ParseFloat(pr.Format.Duration, 64); err == nil {
		return int64(d * 1000)
	}
	return 0
}

func BitRate(pr *ProbeResult) int64 {
	if pr == nil {
		return 0
	}
	if b, err := strconv.ParseInt(strings.TrimSpace(pr.Format.BitRate), 10, 64); err == nil {
		return b
	}
	return 0
}

func VideoStream(pr *ProbeResult) *StreamInfo {
	for i := range pr.Streams {
		if pr.Streams[i].CodecType == "video" {
			return &pr.Streams[i]
		}
	}
	return nil
}

func AudioStream(pr *ProbeResult) *StreamInfo {
	for i := range pr.Streams {
		if pr.Streams[i].CodecType == "audio" {
			return &pr.Streams[i]
		}
	}
	return nil
}

func ContainerFromFormat(fmtName string) string {
	parts := strings.Split(fmtName, ",")
	if len(parts) > 0 {
		return strings.TrimSpace(parts[0])
	}
	return fmtName
}
