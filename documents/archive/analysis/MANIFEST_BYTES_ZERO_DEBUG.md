# Manifest and Bytes Zero Debug Guide

## Issue

Both manifest counts and total bytes are showing 0 in the TUI, even though segments are being downloaded.

## Diagnostic Steps

### 1. Check Parser Status

The TUI now shows diagnostic info when playlists are 0 but segments exist:
- Look for `(parsers: N, lines: M)` next to "Refreshed: 0"
- This indicates:
  - `parsers: N` = Number of debug parsers active
  - `lines: M` = Total log lines processed across all parsers

**If parsers = 0**: Debug parsers aren't being created (check `-stats` flag)
**If lines = 0**: Logs aren't being parsed (check FFmpeg output)
**If lines > 0 but playlists = 0**: Events aren't matching regex or log level too low

### 2. Manifest Count Issue

**Possible Causes:**

1. **Log Level Too Low**
   - Default: `-stats-loglevel verbose`
   - Playlist opens at INFO level should be visible, but try:
   ```bash
   ./go-ffmpeg-hls-swarm -tui -stats-loglevel debug -clients 10 http://...
   ```

2. **VOD vs Live Stream**
   - VOD streams: Only one initial manifest open (AVFormatContext) - should be counted
   - Live streams: Periodic refreshes should be counted
   - If using VOD, you should see at least 1 manifest (initial open)

3. **Regex Not Matching**
   - Fixed regex to match both `[hls @ ...]` and `[AVFormatContext @ ...]`
   - Test with: `grep "Opening.*m3u8"` on FFmpeg stderr output

4. **Events Not Being Parsed**
   - Check if `LinesProcessed` > 0 in diagnostic
   - If 0, stderr isn't reaching the parser

### 3. Bytes Zero Issue

**Possible Causes:**

1. **Progress Parser Not Working**
   - Bytes come from FFmpeg `-progress` output
   - Check if progress updates are being received
   - Verify `-progress pipe:1` or `-progress unix://...` is in FFmpeg command

2. **TotalSize Always 0**
   - FFmpeg might not be reporting `total_size` in progress output
   - Check progress output format

3. **ClientStats Not Updated**
   - Bytes flow: ProgressParser → createProgressCallback → ClientStats.UpdateCurrentBytes()
   - Verify progress callback is being called

### 4. Quick Diagnostic Commands

```bash
# Test with debug log level (should show more events)
./go-ffmpeg-hls-swarm -tui -stats-loglevel debug -clients 5 http://...

# Check FFmpeg command being generated
./go-ffmpeg-hls-swarm -print-cmd -clients 1 http://... | grep -E "(progress|loglevel)"

# Test parser directly (if you have sample FFmpeg output)
# Look for "Opening.*m3u8" lines in the output
```

### 5. Expected Behavior

**For VOD Stream:**
- Manifests: Should be **at least 1** (initial AVFormatContext open)
- Bytes: Should increment as segments download

**For Live Stream:**
- Manifests: Should increment periodically (every targetDuration seconds)
- Bytes: Should increment continuously

### 6. Next Steps

1. **Check TUI Diagnostic**: Look for `(parsers: N, lines: M)` in Playlists section
2. **Try Debug Level**: Use `-stats-loglevel debug` to see if more events appear
3. **Verify FFmpeg Output**: Check if playlist opens are actually in the logs
4. **Check Progress**: Verify `-progress` output contains `total_size` field

## Code Locations

- **Parser**: `internal/parser/debug_events.go` - `rePlaylistOpen` regex
- **Aggregation**: `internal/orchestrator/client_manager.go` - `GetDebugStats()`
- **Bytes Tracking**: `internal/orchestrator/client_manager.go` - `createProgressCallback()`
- **TUI Display**: `internal/tui/view.go` - `renderHLSLayer()`
