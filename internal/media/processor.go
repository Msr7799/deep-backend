package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Processor wraps ffmpeg for media processing operations.
type Processor struct {
	ffmpegPath string
	tempDir    string
}

func NewProcessor(ffmpegPath, tempDir string) *Processor {
	return &Processor{ffmpegPath: ffmpegPath, tempDir: tempDir}
}

type ExtractAudioResult struct {
	FilePath string
	MimeType string
	Filename string
	Codec    string
}

// ExtractAudio extracts AAC audio from a direct media URL or local file.
// For YouTube page URLs, callers should analyze with yt-dlp first and pass the selected format URL.
func (p *Processor) ExtractAudio(ctx context.Context, sourceURL, jobID string, progressCb func(int)) (*ExtractAudioResult, error) {
	if err := os.MkdirAll(p.tempDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir temp: %w", err)
	}

	outPath := filepath.Join(p.tempDir, SafeFilename(jobID+".m4a"))
	args := []string{
		"-y",
		"-nostdin",
		"-loglevel", "error",
		"-user_agent", "Mozilla/5.0 (Linux; Android 14; Pixel 9) AppleWebKit/537.36",
		"-i", sourceURL,
		"-vn",
		"-acodec", "aac",
		"-b:a", "192k",
		"-movflags", "+faststart",
		outPath,
	}

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, p.ffmpegPath, args...)
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg extract audio: %w\nstderr: %s", err, strings.TrimSpace(stderr.String()))
	}
	if progressCb != nil {
		progressCb(100)
	}

	return &ExtractAudioResult{
		FilePath: outPath,
		MimeType: "audio/mp4",
		Filename: SafeFilename(jobID + ".m4a"),
		Codec:    "aac",
	}, nil
}

type MergeAVResult struct {
	FilePath string
	MimeType string
	Filename string
}

// MergeAV merges a video-only stream with a separate audio-only stream into an MP4.
func (p *Processor) MergeAV(ctx context.Context, videoURL, audioURL, jobID string) (*MergeAVResult, error) {
	if err := os.MkdirAll(p.tempDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir temp: %w", err)
	}

	outPath := filepath.Join(p.tempDir, SafeFilename(jobID+"_merged.mp4"))
	ua := "Mozilla/5.0 (Linux; Android 14; Pixel 9) AppleWebKit/537.36"
	args := []string{
		"-y",
		"-nostdin",
		"-loglevel", "error",
		"-user_agent", ua,
		"-i", videoURL,
		"-user_agent", ua,
		"-i", audioURL,
		"-c:v", "copy",
		"-c:a", "aac",
		"-map", "0:v:0",
		"-map", "1:a:0",
		"-movflags", "+faststart",
		outPath,
	}

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, p.ffmpegPath, args...)
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg merge: %w\nstderr: %s", err, strings.TrimSpace(stderr.String()))
	}

	return &MergeAVResult{
		FilePath: outPath,
		MimeType: "video/mp4",
		Filename: SafeFilename(jobID + "_merged.mp4"),
	}, nil
}

type TranscodeResult struct {
	FilePath string
	MimeType string
	Filename string
}

// Transcode re-encodes a direct media URL or local file into MP4 (H.264 + AAC).
func (p *Processor) Transcode(ctx context.Context, sourceURL, jobID string) (*TranscodeResult, error) {
	if err := os.MkdirAll(p.tempDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir temp: %w", err)
	}

	outPath := filepath.Join(p.tempDir, SafeFilename(jobID+"_tc.mp4"))
	args := []string{
		"-y",
		"-nostdin",
		"-loglevel", "error",
		"-user_agent", "Mozilla/5.0 (Linux; Android 14; Pixel 9) AppleWebKit/537.36",
		"-i", sourceURL,
		"-c:v", "libx264",
		"-preset", "fast",
		"-crf", "23",
		"-c:a", "aac",
		"-b:a", "192k",
		"-movflags", "+faststart",
		outPath,
	}

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, p.ffmpegPath, args...)
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg transcode: %w\nstderr: %s", err, strings.TrimSpace(stderr.String()))
	}

	return &TranscodeResult{
		FilePath: outPath,
		MimeType: "video/mp4",
		Filename: SafeFilename(jobID + "_tc.mp4"),
	}, nil
}

func OpenFile(path string) (io.ReadCloser, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

func Cleanup(path string) {
	_ = os.Remove(path)
}

func SafeFilename(name string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		"..", "_",
		" ", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	return replacer.Replace(name)
}
