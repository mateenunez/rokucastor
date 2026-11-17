package whisper

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// cacheSample is how much of each end of a large video identifies it. Hashing a
// multi-gigabyte movie on every cast would cost minutes; size plus both ends
// and the chosen model and language tell finished transcripts apart.
const cacheSample = 1 << 20

// cacheKey names the SRT a video, model and language produce.
func cacheKey(video, model, language string) (string, error) {
	file, err := os.Open(video)
	if err != nil {
		return "", fmt.Errorf("opening %q: %w", video, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("reading %q: %w", video, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file", video)
	}

	hash := sha256.New()
	fmt.Fprintf(hash, "rokucastor-whisper\x00%s\x00%s\x00%s\x00%d\x00", releaseVersion, model, language, info.Size())
	switch {
	case info.Size() <= 2*cacheSample:
		if _, err := io.Copy(hash, file); err != nil {
			return "", fmt.Errorf("hashing %q: %w", video, err)
		}
	default:
		if _, err := io.CopyN(hash, file, cacheSample); err != nil {
			return "", fmt.Errorf("hashing %q: %w", video, err)
		}
		if _, err := file.Seek(-cacheSample, io.SeekEnd); err != nil {
			return "", fmt.Errorf("hashing %q: %w", video, err)
		}
		if _, err := io.CopyN(hash, file, cacheSample); err != nil {
			return "", fmt.Errorf("hashing %q: %w", video, err)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
