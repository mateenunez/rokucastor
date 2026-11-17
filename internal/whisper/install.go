package whisper

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/stupside/rokucastor/internal/config"
)

// The tool and models are pinned by release tag, size and SHA-256, so a
// compromised or changed download is refused instead of run.
const (
	releaseVersion = "v1.9.2"
	toolZipName    = "whisper-bin-x64.zip"
	toolMarker     = "whisper-cli.version"
)

// Model names accepted by Options.Model and StreamOptions.Model. The tiny
// models are what live transcription can keep up with; small and base remain
// available for the offline `rokucastor subtitles` command.
const (
	ModelTiny   = "tiny"
	ModelTinyEN = "tiny.en"
	ModelSmall  = "small"
	ModelBase   = "base"

	DefaultModel       = ModelSmall
	DefaultStreamModel = ModelTiny
)

type artifact struct {
	url    string
	size   int64
	sha256 string
	detail string
}

var toolArtifact = artifact{
	url:    "https://github.com/ggerganov/whisper.cpp/releases/download/" + releaseVersion + "/" + toolZipName,
	size:   8_194_445,
	sha256: "49dcc16de826f20bd53d44f947a1ae49dfa81f86cad67a64d80820cb192d674a",
	detail: toolZipName,
}

var modelArtifacts = map[string]artifact{
	ModelTiny: {
		url:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-tiny.bin",
		size:   77_691_713,
		sha256: "be07e048e1e599ad46341c8d2a135645097a538221678b7acdd1b1919c6e1b21",
		detail: "ggml-tiny.bin",
	},
	ModelTinyEN: {
		url:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-tiny.en.bin",
		size:   77_704_715,
		sha256: "921e4cf8686fdd993dcd081a5da5b6c365bfde1162e72b08d75ac75289920b1f",
		detail: "ggml-tiny.en.bin",
	},
	ModelSmall: {
		url:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin",
		size:   487_601_967,
		sha256: "1be3a9b2063867b937e64e2ec7483364a79917e157fa98c5d94b5c1fffea987b",
		detail: "ggml-small.bin",
	},
	ModelBase: {
		url:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin",
		size:   147_951_465,
		sha256: "60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe",
		detail: "ggml-base.bin",
	},
}

// vadArtifact is the Silero voice-activity model whisper.cpp uses to skip
// silence before each inference.
var vadArtifact = artifact{
	url:    "https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v5.1.2.bin",
	size:   885_098,
	sha256: "29940d98d42b91fbd05ce489f3ecf7c72f0a42f027e4875919a28fb4c04ea2cf",
	detail: "ggml-silero-v5.1.2.bin",
}

func modelArtifact(name string) (artifact, error) {
	art, ok := modelArtifacts[name]
	if !ok {
		return artifact{}, fmt.Errorf("unknown Whisper model %q: use %q, %q, %q or %q", name, ModelTiny, ModelTinyEN, ModelBase, ModelSmall)
	}
	return art, nil
}

// ValidModel reports whether name is a Whisper model rokucastor can download.
func ValidModel(name string) bool {
	_, ok := modelArtifacts[name]
	return ok
}

// installer owns the per-user files Whisper needs.
type installer struct {
	layout   config.Layout
	http     *http.Client
	ffmpeg   string
	progress func(Progress)
}

func (i *installer) report(p Progress) {
	if i.progress != nil {
		i.progress(p)
	}
}

// tool downloads and unpacks whisper-cli the first time, and returns its path.
func (i *installer) tool(ctx context.Context) (string, error) {
	cli, _, err := i.tools(ctx)
	return cli, err
}

// tools downloads and unpacks whisper-cli and whisper-server the first time,
// and returns both paths. An older extraction that predates whisper-server is
// unpacked again from the same pinned ZIP.
func (i *installer) tools(ctx context.Context) (cli, server string, err error) {
	cli = filepath.Join(i.layout.Bin, "whisper-cli.exe")
	server = filepath.Join(i.layout.Bin, "whisper-server.exe")
	marker := filepath.Join(i.layout.Bin, toolMarker)
	if installed(cli, marker, releaseVersion) && regular(server) {
		i.report(Progress{Stage: StageTool, Percent: 100})
		return cli, server, nil
	}

	zipPath := filepath.Join(i.layout.Bin, toolZipName)
	if err := i.fetch(ctx, toolArtifact, zipPath, StageTool); err != nil {
		return "", "", err
	}
	if err := extractTool(zipPath, i.layout.Bin); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(marker, []byte(releaseVersion+"\n"), 0o600); err != nil {
		return "", "", fmt.Errorf("recording the whisper-cli version: %w", err)
	}
	return cli, server, nil
}

// model downloads the selected ggml model the first time.
func (i *installer) model(ctx context.Context, name string) (string, error) {
	art, err := modelArtifact(name)
	if err != nil {
		return "", err
	}
	path := filepath.Join(i.layout.Models, art.detail)
	if err := i.fetch(ctx, art, path, StageModel); err != nil {
		return "", err
	}
	return path, nil
}

// vadModel downloads the Silero voice-activity model the first time.
func (i *installer) vadModel(ctx context.Context) (string, error) {
	path := filepath.Join(i.layout.Models, vadArtifact.detail)
	if err := i.fetch(ctx, vadArtifact, path, StageModel); err != nil {
		return "", err
	}
	return path, nil
}

// installed reports whether an extraction completed for the pinned release.
func installed(exe, marker, version string) bool {
	if !regular(exe) {
		return false
	}
	saved, err := os.ReadFile(marker)
	return err == nil && strings.TrimSpace(string(saved)) == version
}

// fetch downloads art into dest unless a verified copy is already there. The
// sidecar .sha256 file lets later runs trust a large model without rehashing
// it every time.
func (i *installer) fetch(ctx context.Context, art artifact, dest string, stage Stage) error {
	if verified(dest, art) {
		i.report(Progress{Stage: stage, Percent: 100, Detail: art.detail})
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return fmt.Errorf("creating %q: %w", filepath.Dir(dest), err)
	}

	part := dest + ".part"
	defer os.Remove(part)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, art.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "rokucastor")
	resp, err := i.http.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", art.detail, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: %s", art.detail, resp.Status)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != art.size {
		return fmt.Errorf("downloading %s: server offers %d bytes, want %d", art.detail, resp.ContentLength, art.size)
	}

	out, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("writing %s: %w", art.detail, err)
	}
	hash := sha256.New()
	done := &progressReader{total: art.size, report: func(percent int) {
		i.report(Progress{Stage: stage, Percent: percent, Detail: art.detail})
	}}
	written, copyErr := io.Copy(io.MultiWriter(out, hash), io.TeeReader(resp.Body, done))
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("downloading %s: %w", art.detail, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("writing %s: %w", art.detail, closeErr)
	}
	if written != art.size {
		return fmt.Errorf("downloading %s: got %d bytes, want %d", art.detail, written, art.size)
	}
	if sum := hex.EncodeToString(hash.Sum(nil)); sum != art.sha256 {
		return fmt.Errorf("downloading %s: SHA-256 is %s, want %s", art.detail, sum, art.sha256)
	}
	if err := os.Rename(part, dest); err != nil {
		return fmt.Errorf("saving %s: %w", art.detail, err)
	}
	if err := os.WriteFile(dest+".sha256", []byte(art.sha256+"\n"), 0o600); err != nil {
		return fmt.Errorf("recording the checksum of %s: %w", art.detail, err)
	}
	i.report(Progress{Stage: stage, Percent: 100, Detail: art.detail})
	return nil
}

// verified trusts a same-size file only when its recorded checksum matches, or
// when a fresh hash of it does. A first run has no sidecar, so a half-written
// file from an interrupted download is caught here.
func verified(path string, art artifact) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != art.size {
		return false
	}
	if saved, err := os.ReadFile(path + ".sha256"); err == nil {
		return strings.TrimSpace(string(saved)) == art.sha256
	}
	sum, err := fileSHA256(path)
	if err != nil || sum != art.sha256 {
		return false
	}
	_ = os.WriteFile(path+".sha256", []byte(art.sha256+"\n"), 0o600)
	return true
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// extractTool unpacks whisper-cli.exe, whisper-server.exe and the DLLs next to
// them. Only base names are written, so a crafted archive cannot escape the bin
// directory.
func extractTool(zipPath, dir string) error {
	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", filepath.Base(zipPath), err)
	}
	defer archive.Close()

	var cli, server bool
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		name := filepath.Base(entry.Name)
		lower := strings.ToLower(name)
		if lower != "whisper-cli.exe" && lower != "whisper-server.exe" && !strings.HasSuffix(lower, ".dll") {
			continue
		}
		if err := extractEntry(entry, filepath.Join(dir, name)); err != nil {
			return err
		}
		cli = cli || lower == "whisper-cli.exe"
		server = server || lower == "whisper-server.exe"
	}
	if !cli || !server {
		return fmt.Errorf("%s does not contain whisper-cli.exe and whisper-server.exe", filepath.Base(zipPath))
	}
	return nil
}

func extractEntry(entry *zip.File, dest string) error {
	in, err := entry.Open()
	if err != nil {
		return fmt.Errorf("reading %s from the archive: %w", entry.Name, err)
	}
	defer in.Close()

	// The file may be whisper-cli.exe already in use; write beside it and swap.
	part := dest + ".part"
	out, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o700)
	if err != nil {
		return fmt.Errorf("unpacking %s: %w", filepath.Base(dest), err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(part)
		return fmt.Errorf("unpacking %s: %w", filepath.Base(dest), copyErr)
	}
	if closeErr != nil {
		os.Remove(part)
		return fmt.Errorf("unpacking %s: %w", filepath.Base(dest), closeErr)
	}
	if err := os.Rename(part, dest); err != nil {
		os.Remove(part)
		return fmt.Errorf("unpacking %s: %w", filepath.Base(dest), err)
	}
	return nil
}

func regular(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// progressReader turns byte counts into whole percentages, reporting each one
// only when it changes.
type progressReader struct {
	total  int64
	read   int64
	last   int
	report func(int)
}

func (r *progressReader) Write(p []byte) (int, error) {
	r.read += int64(len(p))
	if r.total <= 0 {
		return len(p), nil
	}
	percent := int(r.read * 100 / r.total)
	percent = min(percent, 99) // 100 is sent after the checksum passes
	if percent != r.last {
		r.last = percent
		r.report(percent)
	}
	return len(p), nil
}

// ErrNoSubtitles means whisper-cli ran but did not write an SRT.
var ErrNoSubtitles = errors.New("whisper produced no subtitles")
