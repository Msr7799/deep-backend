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

// NewProcessor creates a Processor.
func NewProcessor(ffmpegPath, tempDir string) *Processor {
	return &Processor{ffmpegPath: ffmpegPath, tempDir: tempDir}
}

// ExtractAudioResult contains the result of an audio extraction.
type ExtractAudioResult struct {
	FilePath string
	MimeType string
	Filename string
	Codec    string
}

// ExtractAudio downloads the source URL and extracts an AAC audio track.
// No shell-string interpolation: all args are passed as a []string.
// Stderr is captured and returned on error for debugging.
func (p *Processor) ExtractAudio(ctx context.Context, sourceURL, jobID string, progressCb func(int)) (*ExtractAudioResult, error) {
	if err := os.MkdirAll(p.tempDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir temp: %w", err)
	}

	outPath := filepath.Join(p.tempDir, SafeFilename(jobID+".m4a"))

	args := []string{
		"-y",                  // overwrite without asking
		"-loglevel", "error",
		"-user_agent", "Mozilla/5.0 (Linux; Android 14; Pixel 9) AppleWebKit/537.36",
		"-i", sourceURL,       // input: URL passed as arg, never concatenated into shell string
		"-vn",                 // strip video
		"-acodec", "aac",     // encode to AAC
		"-b:a", "192k",
		"-movflags", "+faststart",
		outPath,
	}

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, p.ffmpegPath, args...)
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg extract audio: %w\nstderr: %s", err, stderr.String())
	}

	return &ExtractAudioResult{
		FilePath: outPath,
		MimeType: "audio/mp4",
		Filename: SafeFilename(jobID + ".m4a"),
		Codec:    "aac",
	}, nil
}

// MergeAVResult is the result of merging a video-only + audio-only adaptive stream.
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
		return nil, fmt.Errorf("ffmpeg merge: %w\nstderr: %s", err, stderr.String())
	}

	return &MergeAVResult{
		FilePath: outPath,
		MimeType: "video/mp4",
		Filename: SafeFilename(jobID + "_merged.mp4"),
	}, nil
}

// TranscodeResult is the result of a generic transcode.
type TranscodeResult struct {
	FilePath string
	MimeType string
	Filename string
}

// Transcode re-encodes the source into an MP4 with H.264+AAC.
func (p *Processor) Transcode(ctx context.Context, sourceURL, jobID string) (*TranscodeResult, error) {
	if err := os.MkdirAll(p.tempDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir temp: %w", err)
	}

	outPath := filepath.Join(p.tempDir, SafeFilename(jobID+"_tc.mp4"))

	args := []string{
		"-y",
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
		return nil, fmt.Errorf("ffmpeg transcode: %w\nstderr: %s", err, stderr.String())
	}

	return &TranscodeResult{
		FilePath: outPath,
		MimeType: "video/mp4",
		Filename: SafeFilename(jobID + "_tc.mp4"),
	}, nil
}

// OpenFile returns an io.ReadCloser for a local file path.
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

// Cleanup removes a local temp file silently.
func Cleanup(path string) {
	_ = os.Remove(path)
}

// SafeFilename removes characters that could be dangerous in filenames.
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
