# rokucastor

[English](README.md)

Reproduce videos locales de Windows en un Roku desde la terminal. Conserva los
formatos compatibles y transcodifica el resto con FFmpeg.

## Requisitos

- Windows 10/11 x64.
- Roku en la misma red.
- `ffprobe` en el `PATH`; `ffmpeg` para transcodificar o generar subtítulos.
- Go 1.27+ para compilar.
- Para instalar el canal: modo desarrollador del Roku habilitado y su contraseña.

## Compilar

```powershell
go build -o rokucastor.exe ./cmd/rokucastor
# o
make build
```

## Uso

```powershell
# Interfaz interactiva: busca Rokus y permite navegar videos y subtítulos
rokucastor

# Buscar dispositivos
rokucastor devices

# Reproducir
rokucastor cast "C:\Videos\Pelicula.mkv" --device 192.168.1.50

# Instalar/actualizar el canal y reproducir un SRT
rokucastor cast "C:\Videos\Pelicula.mkv" --device 192.168.1.50 `
  --password mi-clave --subtitle "C:\Videos\Pelicula.srt" --language es

# Empezar desde una posición
rokucastor cast "C:\Videos\Pelicula.mp4" --device 192.168.1.50 --at 1:12:30

# Transcribir y guardar un SRT; los resultados se cachean
rokucastor subtitles "C:\Videos\Pelicula.mkv" --language es
```

Sin subcomando, la interfaz permite seleccionar Roku, video y subtítulos.
`↑`/`↓` o `j`/`k` navegan; `↵` selecciona; `esc` vuelve o detiene el cast;
`r` vuelve a buscar Rokus; `espacio` pausa; `q` sale.

## Opciones

| Opción | Comando | Descripción |
|---|---|---|
| `--device`, `-d` | TUI, `cast` | Dirección del Roku. En `cast` es obligatoria. |
| `--password`, `-p` | TUI, `cast` | Contraseña de modo desarrollador; instala o reemplaza el canal. Se guarda en configuración. |
| `--startup-timeout` | TUI, `cast` | Segundos de espera para iniciar la reproducción (30 por defecto); se guarda en configuración. |
| `--app` | TUI, `cast` | ID de un canal ya instalado, en lugar del canal propio. |
| `--at` | `cast` | Posición inicial (`1:12`, `1:12:30` o `1h12m`; `1:00` significa una hora). |
| `--subtitle`, `-s` | `cast` | Archivo SRT para cargar en el Roku. |
| `--generate-subtitles` | `cast` | Transcribe en vivo con Whisper y quema los subtítulos en la imagen. Requiere audio y no se combina con `--subtitle`. |
| `--language`, `-l` | TUI, `cast`, `subtitles` | Idioma hablado o del SRT (ISO 639-1/2); por defecto `es`. |
| `--stream-model` | TUI, `cast` | Modelo Whisper para subtítulos en vivo: `tiny` (por defecto), `tiny.en`, `base` o `small`. |
| `--model` | TUI, `subtitles` | Modelo para generar SRT: `small` (por defecto), `base`, `tiny` o `tiny.en`. |
| `--force` | `subtitles` | Regenera el SRT aunque haya una versión en caché. |

El comando `subtitles` guarda SRT reutilizables en la caché. En la TUI también
se ofrecen los SRT junto al video y las transcripciones cacheadas.

## Datos y diagnóstico

La configuración, modelos, subtítulos y registros se guardan en
`%LOCALAPPDATA%\rokucastor`. `rokucastor paths` muestra las rutas. Los registros
se escriben en `logs\rokucastor.log`.

## Licencia

MIT. Ver [LICENSE](LICENSE) y [NOTICE](NOTICE).
