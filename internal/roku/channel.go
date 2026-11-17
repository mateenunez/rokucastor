package roku

import (
	"archive/zip"
	"bytes"
	"embed"
	"io/fs"
	"strings"
	"text/template"
)

// rokucastor's Roku channel is a SceneGraph app whose only job is to play a
// local file's URL and its side-loaded subtitles, both passed over ECP.

// Launch parameter names the channel reads and the launcher must send.
const (
	paramURL                 = "url"
	paramFormat              = "format"
	paramStart               = "start"
	paramSubtitle            = "subtitle"
	paramSubtitleLang        = "subtitleLang"
	paramSubtitleDescription = "subtitleDescription"
)

const (
	channelTitle = "Rokucastor"
	// legacyChannelTitle is what earlier builds sideloaded. A Roku holding it
	// still plays our media, so it is only replaced when a password allows it.
	legacyChannelTitle = "Rokucast"
)

//go:embed channel
var channelAssets embed.FS

var channelData = struct {
	Title                    string
	ParamURL                 string
	ParamFormat              string
	ParamStart               string
	ParamSubtitle            string
	ParamSubtitleLang        string
	ParamSubtitleDescription string
}{
	channelTitle,
	paramURL,
	paramFormat,
	paramStart,
	paramSubtitle,
	paramSubtitleLang,
	paramSubtitleDescription,
}

// channelZip packs the rendered channel into a sideload archive with the
// manifest at the root.
func channelZip() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	assets, err := fs.Sub(channelAssets, "channel")
	if err != nil {
		return nil, err
	}
	err = fs.WalkDir(assets, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(assets, p)
		if err != nil {
			return err
		}
		name, content, err := renderAsset(p, raw)
		if err != nil {
			return err
		}
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write(content)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// renderAsset expands a .tmpl source and drops the suffix; anything else
// passes through unchanged.
func renderAsset(name string, raw []byte) (string, []byte, error) {
	rendered, ok := strings.CutSuffix(name, ".tmpl")
	if !ok {
		return name, raw, nil
	}
	t, err := template.New(name).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return "", nil, err
	}
	var out bytes.Buffer
	if err := t.Execute(&out, channelData); err != nil {
		return "", nil, err
	}
	return rendered, out.Bytes(), nil
}
