package roku

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestTheChannelZipRendersTheChannel(t *testing.T) {
	b, err := channelZip()
	if err != nil {
		t.Fatalf("channelZip() error = %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("reading zip: %v", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = data
	}

	if !strings.Contains(string(files["manifest"]), "title=Rokucastor") {
		t.Errorf("manifest = %q, want the channel title rendered", files["manifest"])
	}
	if _, ok := files["components/MainScene.brs"]; !ok {
		t.Error("rendered MainScene.brs missing from the archive")
	}
	if strings.Contains(string(files["components/MainScene.brs"]), "{{") {
		t.Error("template placeholders leaked into the channel")
	}
	scene := string(files["components/MainScene.brs"])
	if !strings.Contains(scene, "subtitleTracks") {
		t.Error("the channel does not side-load its subtitle track")
	}
	if !strings.Contains(scene, "content.subtitleConfig = { TrackName: subtitle }") {
		t.Error("the channel does not select its side-loaded subtitle track")
	}
	if strings.Contains(scene, "m.video.subtitleTrack") {
		t.Error("the channel selects subtitles using an unsupported Video field")
	}
	if !strings.Contains(scene, "content.playStart") {
		t.Error("the channel does not start playback at the requested position")
	}
	if len(files["images/icon_focus_hd.png"]) == 0 || len(files["images/splash_hd.png"]) == 0 {
		t.Error("channel images missing from the archive")
	}
	for name := range files {
		if strings.HasSuffix(name, ".tmpl") {
			t.Errorf("template source %q was packed instead of rendered", name)
		}
	}
}
