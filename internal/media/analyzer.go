package media

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"deep-backend/internal/domain"

	"github.com/google/uuid"
)

// Analyzer analyses a source URL and produces a list of MediaVariants.
// It supports: direct media files, YouTube (via ytdlp hint detection),
// and generic video/audio streams detected by ffprobe.
type Analyzer struct {
	prober *Prober
}

func NewAnalyzer(prober *Prober) *Analyzer {
	return &Analyzer{prober: prober}
}

// AnalyzeResult is the output of an analysis.
type AnalyzeResult struct {
	Variants []*domain.MediaVariant
	Title    string
}

// Analyze inspects the source URL and returns available variants.
func (a *Analyzer) Analyze(ctx context.Context, jobID uuid.UUID, sourceURL string) (*AnalyzeResult, error) {
	switch {
	case isYouTubeURL(sourceURL):
		return a.analyzeYouTube(ctx, jobID, sourceURL)
	default:
		return a.analyzeDirect(ctx, jobID, sourceURL)
	}
}

// ─────────────────────────────────────────────
//  Direct media analysis
// ─────────────────────────────────────────────

func (a *Analyzer) analyzeDirect(ctx context.Context, jobID uuid.UUID, sourceURL string) (*AnalyzeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	pr, err := a.prober.Probe(ctx, sourceURL)
	if err != nil {
		return nil, fmt.Errorf("probe %s: %w", sourceURL, err)
	}

	vs := VideoStream(pr)
	as := AudioStream(pr)
	dms := DurationMs(pr)
	br := BitRate(pr)
	container := ContainerFromFormat(pr.Format.FormatName)

	var variants []*domain.MediaVariant

	if vs != nil && as != nil {
		// Combined stream
		variants = append(variants, &domain.MediaVariant{
			ID:          uuid.New(),
			MediaJobID:  jobID,
			Label:       labelFromStream(vs, pr),
			Container:   container,
			CodecVideo:  vs.CodecName,
			CodecAudio:  as.CodecName,
			Bitrate:     br,
			Width:       vs.Width,
			Height:      vs.Height,
			DurationMs:  dms,
			IsAudioOnly: false,
			IsVideoOnly: false,
			IsAdaptive:  false,
			SourceURL:   sourceURL,
			MimeType:    mimeFromContainer(container, false, false),
		})
	} else if vs != nil {
		variants = append(variants, &domain.MediaVariant{
			ID:          uuid.New(),
			MediaJobID:  jobID,
			Label:       labelFromStream(vs, pr),
			Container:   container,
			CodecVideo:  vs.CodecName,
			Bitrate:     br,
			Width:       vs.Width,
			Height:      vs.Height,
			DurationMs:  dms,
			IsVideoOnly: true,
			SourceURL:   sourceURL,
			MimeType:    mimeFromContainer(container, false, true),
		})
	} else if as != nil {
		variants = append(variants, &domain.MediaVariant{
			ID:          uuid.New(),
			MediaJobID:  jobID,
			Label:       fmt.Sprintf("Audio %s", as.CodecName),
			Container:   container,
			CodecAudio:  as.CodecName,
			Bitrate:     br,
			DurationMs:  dms,
			IsAudioOnly: true,
			SourceURL:   sourceURL,
			MimeType:    mimeFromContainer(container, true, false),
		})
	}

	title := pr.Format.Tags["title"]
	return &AnalyzeResult{Variants: variants, Title: title}, nil
}

// ─────────────────────────────────────────────
//  YouTube analysis (yt-dlp integration)
// ─────────────────────────────────────────────

// analyzeYouTube uses yt-dlp to enumerate available formats.
// yt-dlp must be installed on the host or in the container.
// No shell interpolation: we pass args directly.
func (a *Analyzer) analyzeYouTube(ctx context.Context, jobID uuid.UUID, sourceURL string) (*AnalyzeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	formats, title, err := ytDlpListFormats(ctx, sourceURL)
	if err != nil {
		// Fallback: try direct probe (works for some YouTube embed URLs)
		return a.analyzeDirect(ctx, jobID, sourceURL)
	}

	var variants []*domain.MediaVariant
	for _, f := range formats {
		v := &domain.MediaVariant{
			ID:         uuid.New(),
			MediaJobID: jobID,
			Label:      f.Label,
			Container:  f.Container,
			CodecVideo: f.VCodec,
			CodecAudio: f.ACodec,
			Bitrate:    f.TBR,
			Width:      f.Width,
			Height:     f.Height,
			DurationMs: f.DurationMs,
			IsAudioOnly: f.VCodec == "" || f.VCodec == "none",
			IsVideoOnly: f.ACodec == "" || f.ACodec == "none",
			IsAdaptive:  f.IsAdaptive,
			SourceURL:   f.URL,
			MimeType:    f.MimeType,
		}
		variants = append(variants, v)
	}

	return &AnalyzeResult{Variants: variants, Title: title}, nil
}

// ─────────────────────────────────────────────
//  yt-dlp helpers
// ─────────────────────────────────────────────

type ytFormat struct {
	Label      string
	Container  string
	VCodec     string
	ACodec     string
	TBR        int64
	Width      int
	Height     int
	DurationMs int64
	IsAdaptive bool
	URL        string
	MimeType   string
}

// ytDlpListFormats calls yt-dlp to enumerate formats.
// It uses the JSON output format for reliable parsing.
func ytDlpListFormats(ctx context.Context, sourceURL string) ([]ytFormat, string, error) {
	cmd := newExecCmd(ctx, "yt-dlp",
		"--dump-json",
		"--no-warnings",
		"--no-playlist",
		"--user-agent", "Mozilla/5.0 (Linux; Android 14; Pixel 9) AppleWebKit/537.36",
		sourceURL,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, "", fmt.Errorf("yt-dlp: %w", err)
	}
	return parseYtDlpJSON(out)
}

// ─────────────────────────────────────────────
//  URL classification helpers
// ─────────────────────────────────────────────

func isYouTubeURL(u string) bool {
	low := strings.ToLower(u)
	return strings.Contains(low, "youtube.com/watch") ||
		strings.Contains(low, "youtu.be/") ||
		strings.Contains(low, "youtube.com/shorts/")
}

func labelFromStream(vs *StreamInfo, pr *ProbeResult) string {
	if vs == nil {
		return "Unknown"
	}
	if vs.Height > 0 {
		return fmt.Sprintf("%dp", vs.Height)
	}
	return strings.ToUpper(ContainerFromFormat(pr.Format.FormatName))
}

func mimeFromContainer(container string, audioOnly, videoOnly bool) string {
	switch {
	case audioOnly:
		switch container {
		case "mp3":
			return "audio/mpeg"
		case "ogg":
			return "audio/ogg"
		case "wav":
			return "audio/wav"
		default:
			return "audio/mp4"
		}
	case videoOnly || !audioOnly:
		switch container {
		case "webm":
			return "video/webm"
		case "matroska", "mkv":
			return "video/x-matroska"
		default:
			return "video/mp4"
		}
	default:
		return "application/octet-stream"
	}
}

// ValidateSourceURL does a quick HEAD check to confirm the URL is reachable.
func ValidateSourceURL(ctx context.Context, rawURL string) error {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return fmt.Errorf("url must start with http:// or https://")
	}
	if isYouTubeURL(rawURL) {
		return nil // skip HEAD check for YouTube
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("url unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("url returned %d", resp.StatusCode)
	}
	return nil
}
