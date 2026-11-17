# rokucastor

[Español](LEEME.md)

Play local Windows videos on a Roku from the terminal. Compatible formats are
streamed as-is; others are transcoded with FFmpeg.

## Requirements

- Windows 10/11 x64.
- A Roku on the same network.
- `ffprobe` on `PATH`; `ffmpeg` for transcoding or subtitle generation.
- Go 1.27+ to build from source.
- To install the channel: Roku developer mode enabled and its password.

## Build

```powershell
go build -o rokucastor.exe ./cmd/rokucastor
# or
make build
```

## Usage

```powershell
# Interactive UI: discover Rokus and browse videos and subtitles
rokucastor

# Discover devices
rokucastor devices

# Play a video
rokucastor cast "C:\Videos\Movie.mkv" --device 192.168.1.50

# Install/update the channel and play an SRT
rokucastor cast "C:\Videos\Movie.mkv" --device 192.168.1.50 `
  --password my-password --subtitle "C:\Videos\Movie.srt" --language en

# Start at a position
rokucastor cast "C:\Videos\Movie.mp4" --device 192.168.1.50 --at 1:12:30

# Transcribe and save an SRT; results are cached
rokucastor subtitles "C:\Videos\Movie.mkv" --language en
```

With no subcommand, the UI lets you select a Roku, video, and subtitles.
`↑`/`↓` or `j`/`k` navigate; `↵` selects; `esc` goes back or stops playback;
`r` searches for Rokus again; `space` pauses; `q` quits.

## Options

| Option | Command | Description |
|---|---|---|
| `--device`, `-d` | UI, `cast` | Roku address. Required for `cast`. |
| `--password`, `-p` | UI, `cast` | Developer-mode password; installs or replaces the channel. Saved in configuration. |
| `--startup-timeout` | UI, `cast` | Seconds to wait for playback to start (30 by default). Saved in configuration. |
| `--app` | UI, `cast` | ID of an installed channel to launch instead of the bundled channel. |
| `--at` | `cast` | Starting position (`1:12`, `1:12:30`, or `1h12m`; `1:00` means one hour). |
| `--subtitle`, `-s` | `cast` | SRT file to load on the Roku. |
| `--generate-subtitles` | `cast` | Transcribe live with Whisper and burn subtitles into the video. Requires audio and cannot be combined with `--subtitle`. |
| `--language`, `-l` | UI, `cast`, `subtitles` | Spoken or subtitle language (ISO 639-1/2); defaults to `es`. |
| `--stream-model` | UI, `cast` | Whisper model for live subtitles: `tiny` (default), `tiny.en`, `base`, or `small`. |
| `--model` | UI, `subtitles` | Model for SRT generation: `small` (default), `base`, `tiny`, or `tiny.en`. |
| `--force` | `subtitles` | Regenerate the SRT even if a cached version exists. |

The `subtitles` command saves reusable SRT files in the cache. The UI also
offers SRT files next to the video and cached transcripts.

## Data and diagnostics

Configuration, models, subtitles, and logs are stored in
`%LOCALAPPDATA%\rokucastor`. Run `rokucastor paths` to print the paths. Logs are
written to `logs\rokucastor.log`.

## License

MIT. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
