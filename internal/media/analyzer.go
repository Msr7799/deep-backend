package media

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"deep-backend/internal/domain"

	"github.com/google/uuid"
)

// Analyzer analyses a source URL and produces a list of MediaVariants.
// It supports direct media files and YouTube links via yt-dlp.
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

// analyzeDirect probes a direct media URL or local file using ffprobe.
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

	switch {
	case vs != nil && as != nil:
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
	case vs != nil:
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
			IsAudioOnly: false,
			IsVideoOnly: true,
			IsAdaptive:  true,
			SourceURL:   sourceURL,
			MimeType:    mimeFromContainer(container, false, true),
		})
	case as != nil:
		variants = append(variants, &domain.MediaVariant{
			ID:          uuid.New(),
			MediaJobID:  jobID,
			Label:       fmt.Sprintf("Audio %s", as.CodecName),
			Container:   container,
			CodecAudio:  as.CodecName,
			Bitrate:     br,
			DurationMs:  dms,
			IsAudioOnly: true,
			IsVideoOnly: false,
			IsAdaptive:  true,
			SourceURL:   sourceURL,
			MimeType:    mimeFromContainer(container, true, false),
		})
	}

	if len(variants) == 0 {
		return nil, fmt.Errorf("probe %s: no playable streams detected", sourceURL)
	}

	title := pr.Format.Tags["title"]
	return &AnalyzeResult{Variants: variants, Title: title}, nil
}

// analyzeYouTube uses yt-dlp to enumerate available formats.
// Important: we do NOT fall back to ffprobe for YouTube page URLs because ffprobe
// cannot probe a normal YouTube watch/share URL directly.
func (a *Analyzer) analyzeYouTube(ctx context.Context, jobID uuid.UUID, sourceURL string) (*AnalyzeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	formats, title, err := ytDlpListFormats(ctx, sourceURL)
	if err != nil {
		return nil, fmt.Errorf("youtube analyze via yt-dlp failed: %w", err)
	}
	if len(formats) == 0 {
		return nil, fmt.Errorf("youtube analyze via yt-dlp returned no formats")
	}

	variants := make([]*domain.MediaVariant, 0, len(formats))
	for _, f := range formats {
		vCodec := normalizeCodec(f.VCodec)
		aCodec := normalizeCodec(f.ACodec)
		isAudioOnly := vCodec == ""
		isVideoOnly := aCodec == ""

		variants = append(variants, &domain.MediaVariant{
			ID:          uuid.New(),
			MediaJobID:  jobID,
			Label:       f.Label,
			Container:   f.Container,
			CodecVideo:  vCodec,
			CodecAudio:  aCodec,
			Bitrate:     f.TBR,
			Width:       f.Width,
			Height:      f.Height,
			DurationMs:  f.DurationMs,
			IsAudioOnly: isAudioOnly,
			IsVideoOnly: isVideoOnly,
			IsAdaptive:  f.IsAdaptive,
			SourceURL:   f.URL,
			MimeType:    f.MimeType,
		})
	}

	return &AnalyzeResult{Variants: variants, Title: title}, nil
}

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

// ytCookiesPath is where startup writes the YouTube cookies decoded from YT_COOKIES_B64.
const ytCookiesPath = "/tmp/yt_cookies.txt"

// ytDlpListFormats calls yt-dlp to enumerate formats.
// It uses JSON output for reliable parsing and captures stderr for diagnostics.
// If /tmp/yt_cookies.txt exists (written at startup from YT_COOKIES_B64), it is
// passed to yt-dlp so YouTube treats the request as an authenticated browser session.
func ytDlpListFormats(ctx context.Context, sourceURL string) ([]ytFormat, string, error) {
	args := []string{
		"--dump-single-json",
		"--no-warnings",
		"--no-playlist",
		"--extractor-args", "youtube:player_client=android,web",
		"--user-agent", "Mozilla/5.0 (Linux; Android 14; Pixel 9) AppleWebKit/537.36",
	}

	// Inject cookies file if it was written at startup from YT_COOKIES_B64.
	if _, err := os.Stat(ytCookiesPath); err == nil {
		args = append(args, "--cookies", ytCookiesPath)
	}

	args = append(args, sourceURL)

	cmd := newExecCmd(ctx, "yt-dlp", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, "", fmt.Errorf("yt-dlp failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return parseYtDlpJSON(out)
}

func isYouTubeURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	path := strings.ToLower(u.Path)
	return strings.Contains(host, "youtube.com") || strings.Contains(host, "youtu.be") || strings.Contains(path, "/shorts/")
}

func normalizeCodec(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" || v == "none" {
		return ""
	}
	return v
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
		case "webm":
			return "audio/webm"
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
		return nil // skip HEAD check for YouTube page URLs
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
