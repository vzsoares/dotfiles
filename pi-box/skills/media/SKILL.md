---
name: media
description: Download and transcode audio or video with yt-dlp and ffmpeg. Use for video/audio downloads, format conversion, clipping, thumbnails and metadata.
---

# media

Everything lands in `out/`. Keep downloads polite: one at a time unless told
otherwise, and `--sleep-interval` of `CRAWL_DELAY_MS`/1000 seconds.

## Download

```bash
yt-dlp -P out -o '%(title).80B [%(id)s].%(ext)s' \
  --sleep-interval "$(( ${CRAWL_DELAY_MS:-1000} / 1000 ))" \
  --retries 5 "$URL"
yt-dlp -x --audio-format mp3 -P out "$URL"      # audio only
yt-dlp --dump-json --skip-download "$URL" > out/meta.json
yt-dlp --write-subs --write-auto-subs --sub-langs 'en.*' --skip-download -P out "$URL"
```

Logins: `yt-dlp --cookies ../../in/cookies/<site>.txt ...`.
`--cookies-from-browser` is banned: no browser profile exists in the box.

## ffmpeg

```bash
ffmpeg -i out/in.mp4 -c:v libx264 -crf 28 -c:a aac out/small.mp4   # shrink
ffmpeg -ss 00:01:00 -t 30 -i out/in.mp4 -c copy out/clip.mp4        # clip
ffmpeg -i out/in.mp4 -vn -c:a libmp3lame out/audio.mp3              # audio
ffmpeg -i out/in.mp4 -vf fps=1/10 out/frame_%04d.png                # frames
ffprobe -v error -show_format -show_streams -of json out/in.mp4     # inspect
```

The container has 2 GB of RAM and no GPU; prefer `-crf` and `-preset veryfast`
for long files.
