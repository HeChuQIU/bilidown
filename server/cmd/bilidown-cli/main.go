package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"bilidown/bilibili"
	"bilidown/common"
	"bilidown/task"
	"bilidown/util"

	qrcode "github.com/skip2/go-qrcode"
	_ "modernc.org/sqlite"
)

const cliVersion = "1.0.0"

// Global flags
var (
	flagJSON    bool
	flagDataDir string
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]

	switch cmd {
	case "help", "--help", "-h":
		printUsage()
		return
	case "--version", "-v", "version":
		fmt.Printf("bilidown-cli %s\n", cliVersion)
		return
	case "login":
		cmdLogin(os.Args[2:])
	case "logout":
		cmdLogout(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	case "info":
		cmdInfo(os.Args[2:])
	case "download":
		cmdDownload(os.Args[2:])
	default:
		printError(fmt.Sprintf("unknown command: %s", cmd))
		printUsage()
		os.Exit(1)
	}
}

func addGlobalFlags(fs *flag.FlagSet) {
	fs.BoolVar(&flagJSON, "json", false, "Output in JSON format (AI agent friendly)")
	fs.StringVar(&flagDataDir, "data-dir", "", "Custom data directory")
}

// ==================== Commands ====================

func cmdLogin(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	addGlobalFlags(fs)
	fs.Parse(args)

	db := mustOpenDB()
	defer db.Close()

	client := &bilibili.BiliClient{}

	for {
		qrInfo, err := client.NewQRInfo()
		if err != nil {
			exitError("Failed to get QR code: " + err.Error())
		}

		if flagJSON {
			printJSON(map[string]any{
				"type":    "qr_code",
				"url":     qrInfo.URL,
				"key":     qrInfo.QrcodeKey,
				"message": "Scan QR code with Bilibili app",
			})
		} else {
			qr, err := qrcode.New(qrInfo.URL, qrcode.Medium)
			if err != nil {
				exitError("Failed to generate QR code: " + err.Error())
			}
			fmt.Println("\nScan this QR code with the Bilibili app:")
			fmt.Println(qr.ToString(false))
		}

		success := pollQRStatus(db, client, qrInfo.QrcodeKey)
		if success {
			return
		}
		// QR expired, loop to generate new one
		if flagJSON {
			printJSON(map[string]any{
				"type":    "qr_status",
				"code":    bilibili.QR_EXPIRES,
				"message": "QR code expired, generating new one",
			})
		} else {
			fmt.Println("\nQR code expired, generating new one...")
		}
	}
}

func pollQRStatus(db *sql.DB, client *bilibili.BiliClient, qrKey string) bool {
	deadline := time.Now().Add(120 * time.Second)

	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)

		qrStatus, sessdata, err := client.GetQRStatus(qrKey)
		if err != nil {
			if flagJSON {
				printJSON(map[string]any{
					"type":    "error",
					"message": err.Error(),
				})
			}
			continue
		}

		switch qrStatus.Code {
		case bilibili.QR_NO_SCAN:
			if flagJSON {
				printJSON(map[string]any{
					"type":    "qr_status",
					"code":    qrStatus.Code,
					"message": "Waiting for scan",
				})
			} else {
				remaining := int(time.Until(deadline).Seconds())
				fmt.Printf("\r  Waiting for scan... (%ds remaining)  ", remaining)
			}
		case bilibili.QR_NO_CLICK:
			if flagJSON {
				printJSON(map[string]any{
					"type":    "qr_status",
					"code":    qrStatus.Code,
					"message": "Scanned, waiting for confirmation",
				})
			} else {
				fmt.Printf("\r  Scanned! Please confirm on your phone...     ")
			}
		case bilibili.QR_EXPIRES:
			if !flagJSON {
				fmt.Println()
			}
			return false
		case bilibili.QR_SUCCESS:
			err = bilibili.SaveSessdata(db, sessdata)
			if err != nil {
				exitError("Failed to save credentials: " + err.Error())
			}
			if flagJSON {
				printJSON(map[string]any{
					"type":    "qr_status",
					"code":    qrStatus.Code,
					"message": "Login successful",
				})
			} else {
				fmt.Println("\n  ✓ Login successful! Credentials saved.")
			}
			return true
		}
	}
	return false
}

func cmdLogout(args []string) {
	fs := flag.NewFlagSet("logout", flag.ExitOnError)
	addGlobalFlags(fs)
	fs.Parse(args)

	db := mustOpenDB()
	defer db.Close()

	err := bilibili.SaveSessdata(db, "")
	if err != nil {
		exitError("Failed to logout: " + err.Error())
	}
	outputResult(map[string]any{"message": "Logged out successfully"})
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	addGlobalFlags(fs)
	fs.Parse(args)

	db := mustOpenDB()
	defer db.Close()

	sessdata, err := bilibili.GetSessdata(db)
	if err != nil || sessdata == "" {
		if flagJSON {
			printJSON(map[string]any{
				"success":   true,
				"logged_in": false,
				"message":   "Not logged in",
			})
		} else {
			fmt.Println("Not logged in. Use 'bilidown-cli login' to login.")
		}
		return
	}

	client := &bilibili.BiliClient{SESSDATA: sessdata}
	loggedIn, err := client.CheckLogin()
	if err != nil || !loggedIn {
		if flagJSON {
			printJSON(map[string]any{
				"success":   true,
				"logged_in": false,
				"message":   "Session expired or invalid",
			})
		} else {
			fmt.Println("Session expired or invalid. Use 'bilidown-cli login' to re-login.")
		}
		return
	}

	if flagJSON {
		printJSON(map[string]any{
			"success":   true,
			"logged_in": true,
			"message":   "Logged in",
		})
	} else {
		fmt.Println("✓ Logged in")
	}
}

func cmdInfo(args []string) {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	addGlobalFlags(fs)
	fs.Parse(args)

	if fs.NArg() < 1 {
		if flagJSON {
			exitError("Missing video URL or BVID")
		}
		fmt.Fprintln(os.Stderr, "Usage: bilidown-cli info [flags] <bvid_or_url>")
		os.Exit(1)
	}

	bvid := parseBVID(fs.Arg(0))
	if bvid == "" {
		exitError("Invalid video URL or BVID: " + fs.Arg(0))
	}

	db := mustOpenDB()
	defer db.Close()

	client := mustGetClient(db)
	videoInfo, err := client.GetVideoInfo(bvid)
	if err != nil {
		exitError("Failed to get video info: " + err.Error())
	}

	if flagJSON {
		printJSON(map[string]any{
			"success": true,
			"data":    videoInfo,
		})
	} else {
		printVideoInfo(videoInfo)
	}
}

func cmdDownload(args []string) {
	fs := flag.NewFlagSet("download", flag.ExitOnError)
	audioOnly := fs.Bool("audio-only", false, "Download only audio (saves as .m4a)")
	outputDir := fs.String("output-dir", "", "Output directory (default: ./download)")
	formatCode := fs.Int("format", 0, "Video quality code (default: best available)")
	page := fs.Int("page", 1, "Which page/part to download (1-based)")
	addGlobalFlags(fs)
	fs.Parse(args)

	if fs.NArg() < 1 {
		if flagJSON {
			exitError("Missing video URL or BVID")
		}
		fmt.Fprintln(os.Stderr, "Usage: bilidown-cli download [flags] <bvid_or_url>")
		os.Exit(1)
	}

	bvid := parseBVID(fs.Arg(0))
	if bvid == "" {
		exitError("Invalid video URL or BVID: " + fs.Arg(0))
	}

	db := mustOpenDB()
	defer db.Close()

	client := mustGetClient(db)

	// Check ffmpeg
	if _, err := util.GetFFmpegPath(); err != nil {
		exitError("FFmpeg not found. Install it from https://www.ffmpeg.org/download.html or place it in ./bin")
	}

	// Get video info
	if !flagJSON {
		fmt.Println("Fetching video info...")
	}
	videoInfo, err := client.GetVideoInfo(bvid)
	if err != nil {
		exitError("Failed to get video info: " + err.Error())
	}

	// Select page
	pageIdx := *page - 1
	if pageIdx < 0 || pageIdx >= len(videoInfo.Pages) {
		exitError(fmt.Sprintf("Invalid page number: %d (available: 1-%d)", *page, len(videoInfo.Pages)))
	}
	selectedPage := videoInfo.Pages[pageIdx]

	// Get play info
	playInfo, err := client.GetPlayInfo(bvid, selectedPage.Cid)
	if err != nil {
		exitError("Failed to get play info: " + err.Error())
	}
	if playInfo.Dash == nil {
		exitError("No DASH stream available for this video")
	}

	// Determine output directory:
	// 1. --output-dir flag
	// 2. Second positional argument (e.g. bilidown-cli download <url> <dir>)
	// 3. Configured default folder from DB
	// 4. ./download
	outDir := *outputDir
	if outDir == "" && fs.NArg() >= 2 {
		outDir = fs.Arg(1)
	}
	if outDir == "" {
		outDir, err = util.GetCurrentFolder(db)
		if err != nil {
			outDir = "./download"
			os.MkdirAll(outDir, os.ModePerm)
		}
	} else {
		os.MkdirAll(outDir, os.ModePerm)
	}
	// 转换为绝对路径，避免传给 FFmpeg 时出现相对路径解析问题
	if absDir, absErr := filepath.Abs(outDir); absErr == nil {
		outDir = absDir
	}

	// Get audio URL
	audioURL := task.GetAudioURL(playInfo.Dash)
	if audioURL == "" {
		exitError("No audio stream available")
	}

	// Build safe title
	safeTitle := util.FilterFileName(videoInfo.Title)
	if len(videoInfo.Pages) > 1 {
		safeTitle = fmt.Sprintf("%s - P%d %s", safeTitle, *page, util.FilterFileName(selectedPage.Part))
	}

	if !flagJSON {
		fmt.Printf("Title: %s\n", videoInfo.Title)
		if len(videoInfo.Pages) > 1 {
			fmt.Printf("Page:  P%d %s\n", *page, selectedPage.Part)
		}
	}

	if *audioOnly {
		downloadAudioOnly(client, audioURL, outDir, safeTitle, playInfo.Dash.Duration)
	} else {
		format := common.MediaFormat(*formatCode)
		if format == 0 {
			format = selectBestFormat(playInfo)
		}

		videoURL, err := task.GetVideoURL(playInfo.Dash.Video, format)
		if err != nil {
			// Try with best available format
			if len(playInfo.Dash.Video) > 0 {
				videoURL = playInfo.Dash.Video[0].BaseURL
				format = playInfo.Dash.Video[0].ID
			} else {
				exitError("No video stream available: " + err.Error())
			}
		}

		if !flagJSON {
			fmt.Printf("Quality: %s\n", formatQualityName(format))
		}

		downloadVideoAudio(client, videoURL, audioURL, outDir, safeTitle, format, playInfo.Dash.Duration)
	}
}

// ==================== Download Functions ====================

func downloadAudioOnly(client *bilibili.BiliClient, audioURL, outDir, title string, duration int) {
	tmpPath := filepath.Join(outDir, title+".tmp.audio")
	outputPath := filepath.Join(outDir, title+".m4a")

	if !flagJSON {
		fmt.Println("Downloading audio only...")
	}

	err := downloadFile(client, audioURL, tmpPath, "Audio")
	if err != nil {
		os.Remove(tmpPath)
		exitError("Failed to download audio: " + err.Error())
	}

	if !flagJSON {
		fmt.Print("  Remuxing to m4a...")
	}
	err = remuxAudio(tmpPath, outputPath)
	os.Remove(tmpPath)
	if err != nil {
		exitError("Failed to remux audio: " + err.Error())
	}
	if !flagJSON {
		fmt.Println(" done")
	}

	outputResult(map[string]any{
		"message": "Download complete",
		"path":    outputPath,
		"type":    "audio",
	})
}

func downloadVideoAudio(client *bilibili.BiliClient, videoURL, audioURL, outDir, title string, format common.MediaFormat, duration int) {
	tmpVideo := filepath.Join(outDir, title+".tmp.video")
	tmpAudio := filepath.Join(outDir, title+".tmp.audio")
	outputPath := filepath.Join(outDir, title+".mp4")

	// Download audio
	err := downloadFile(client, audioURL, tmpAudio, "Audio")
	if err != nil {
		os.Remove(tmpAudio)
		exitError("Failed to download audio: " + err.Error())
	}

	// Download video
	err = downloadFile(client, videoURL, tmpVideo, "Video")
	if err != nil {
		os.Remove(tmpAudio)
		os.Remove(tmpVideo)
		exitError("Failed to download video: " + err.Error())
	}

	// Merge
	if !flagJSON {
		fmt.Print("  Merging audio and video...")
	}
	err = mergeMedia(tmpVideo, tmpAudio, outputPath, duration)
	if err != nil {
		// 合并失败时保留临时文件以便调试
		exitError("Failed to merge: " + err.Error())
	}
	os.Remove(tmpVideo)
	os.Remove(tmpAudio)
	if !flagJSON {
		fmt.Println(" done")
	}

	outputResult(map[string]any{
		"message": "Download complete",
		"path":    outputPath,
		"type":    "video",
		"format":  format,
	})
}

func downloadFile(client *bilibili.BiliClient, url, destPath, label string) error {
	var resp io.ReadCloser
	var total int64
	var err error

	for i := 0; i < 5; i++ {
		httpResp, httpErr := client.SimpleGET(url, nil)
		if httpErr == nil {
			resp = httpResp.Body
			total = httpResp.ContentLength
			break
		}
		err = httpErr
		time.Sleep(time.Duration(i+1) * time.Second)
	}
	if err != nil {
		return err
	}
	defer resp.Close()

	file, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer file.Close()

	var downloaded int64
	buf := make([]byte, 32*1024)
	lastPrint := time.Time{}

	for {
		n, readErr := resp.Read(buf)
		if n > 0 {
			_, writeErr := file.Write(buf[:n])
			if writeErr != nil {
				return writeErr
			}
			downloaded += int64(n)

			if time.Since(lastPrint) > 500*time.Millisecond {
				pct := float64(downloaded) / float64(total) * 100
				if flagJSON {
					printJSON(map[string]any{
						"type":    "progress",
						"stage":   strings.ToLower(label),
						"percent": roundFloat(pct, 1),
						"bytes":   downloaded,
						"total":   total,
					})
				} else if total > 0 {
					fmt.Printf("\r  %s: %.1f%% (%s / %s)    ", label, pct, humanBytes(downloaded), humanBytes(total))
				}
				lastPrint = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	if flagJSON {
		printJSON(map[string]any{
			"type":    "progress",
			"stage":   strings.ToLower(label),
			"percent": 100.0,
			"bytes":   downloaded,
			"total":   total,
		})
	} else {
		fmt.Printf("\r  %s: 100.0%% (%s)                \n", label, humanBytes(downloaded))
	}
	return nil
}

func remuxAudio(inputPath, outputPath string) error {
	ffmpegPath, err := util.GetFFmpegPath()
	if err != nil {
		return err
	}
	cmd := exec.Command(ffmpegPath, "-i", inputPath, "-c:a", "copy", "-y", outputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, string(output))
	}
	return nil
}

func mergeMedia(videoPath, audioPath, outputPath string, duration int) error {
	ffmpegPath, err := util.GetFFmpegPath()
	if err != nil {
		return err
	}

	args := []string{"-i", videoPath, "-i", audioPath, "-c:v", "copy", "-c:a", "copy", "-strict", "-2", "-y"}

	if flagJSON {
		args = append(args, "-progress", "pipe:1")
		args = append(args, outputPath)
		cmd := exec.Command(ffmpegPath, args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}

		scanner := bufio.NewScanner(stdout)
		outTimeRegex := regexp.MustCompile(`out_time_ms=(\d+)`)
		for scanner.Scan() {
			line := scanner.Text()
			match := outTimeRegex.FindStringSubmatch(line)
			if len(match) == 2 {
				outTime, parseErr := strconv.ParseInt(match[1], 10, 64)
				if parseErr != nil {
					continue
				}
				pct := float64(outTime) / float64(duration*1000000) * 100
				if pct > 100 {
					pct = 100
				}
				printJSON(map[string]any{
					"type":    "progress",
					"stage":   "merge",
					"percent": roundFloat(pct, 1),
				})
			}
		}
		return cmd.Wait()
	}

	args = append(args, outputPath)
	cmd := exec.Command(ffmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, string(output))
	}
	return nil
}

// ==================== Database Helpers ====================

func mustOpenDB() *sql.DB {
	dataDir := flagDataDir
	if dataDir == "" {
		var err error
		dataDir, err = util.GetDataDir()
		if err != nil {
			exitError("Failed to get data directory: " + err.Error())
		}
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		exitError("Failed to create data directory: " + err.Error())
	}
	dbPath := filepath.Join(dataDir, "data.db")
	db := util.MustGetDB(dbPath)
	if err := util.InitTables(db); err != nil {
		exitError("Failed to initialize database: " + err.Error())
	}
	return db
}

func mustGetClient(db *sql.DB) *bilibili.BiliClient {
	sessdata, err := bilibili.GetSessdata(db)
	if err != nil || sessdata == "" {
		if flagJSON {
			printJSON(map[string]any{
				"success": false,
				"error":   "not_logged_in",
				"message": "Not logged in. Use 'bilidown-cli login' first.",
			})
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "Error: Not logged in. Use 'bilidown-cli login' first.")
		os.Exit(2)
	}
	return &bilibili.BiliClient{SESSDATA: sessdata}
}

// ==================== Parsing Helpers ====================

func parseBVID(input string) string {
	if util.CheckBvidFormat(input) {
		return input
	}
	re := regexp.MustCompile(`BV1[a-zA-Z0-9]+`)
	match := re.FindString(input)
	return match
}

func selectBestFormat(playInfo *bilibili.PlayInfo) common.MediaFormat {
	if len(playInfo.AcceptQuality) > 0 {
		return playInfo.AcceptQuality[0]
	}
	if playInfo.Dash != nil && len(playInfo.Dash.Video) > 0 {
		return playInfo.Dash.Video[0].ID
	}
	return 80 // Default to 1080P
}

// ==================== Display Helpers ====================

func printVideoInfo(info *bilibili.VideoInfo) {
	fmt.Printf("Title:    %s\n", info.Title)
	fmt.Printf("BVID:     %s\n", info.Bvid)
	fmt.Printf("Author:   %s\n", info.Owner.Name)
	fmt.Printf("Duration: %s\n", formatDuration(info.Duration))
	fmt.Printf("Views:    %d\n", info.Stat.View)
	fmt.Printf("Likes:    %d\n", info.Stat.Like)
	fmt.Printf("Coins:    %d\n", info.Stat.Coin)
	if info.Desc != "" {
		fmt.Printf("Desc:     %s\n", info.Desc)
	}
	if len(info.Pages) > 1 {
		fmt.Printf("\nPages (%d):\n", len(info.Pages))
		for _, p := range info.Pages {
			fmt.Printf("  P%d: %s (%s)\n", p.Page, p.Part, formatDuration(p.Duration))
		}
	}
}

func formatDuration(seconds int) string {
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func formatQualityName(format common.MediaFormat) string {
	names := map[common.MediaFormat]string{
		127: "8K Ultra HD",
		126: "Dolby Vision",
		125: "HDR True Color",
		120: "4K Ultra HD",
		116: "1080P 60fps",
		112: "1080P High",
		80:  "1080P",
		74:  "720P 60fps",
		64:  "720P",
		32:  "480P",
		16:  "360P",
		6:   "240P",
	}
	if name, ok := names[format]; ok {
		return fmt.Sprintf("%s (%d)", name, format)
	}
	return fmt.Sprintf("Unknown (%d)", format)
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func roundFloat(val float64, precision int) float64 {
	p := math.Pow(10, float64(precision))
	return math.Round(val*p) / p
}

// ==================== Output Helpers ====================

func printJSON(data any) {
	bs, err := json.Marshal(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "JSON marshal error: %v\n", err)
		return
	}
	fmt.Println(string(bs))
}

func outputResult(data map[string]any) {
	if flagJSON {
		data["success"] = true
		printJSON(data)
	} else {
		if msg, ok := data["message"]; ok {
			fmt.Println(msg)
		}
		if path, ok := data["path"]; ok {
			fmt.Printf("Saved to: %s\n", path)
		}
	}
}

func exitError(msg string) {
	if flagJSON {
		printJSON(map[string]any{
			"success": false,
			"error":   msg,
		})
	} else {
		fmt.Fprintln(os.Stderr, "Error: "+msg)
	}
	os.Exit(1)
}

func printError(msg string) {
	if flagJSON {
		printJSON(map[string]any{
			"success": false,
			"error":   msg,
		})
	} else {
		fmt.Fprintln(os.Stderr, "Error: "+msg)
	}
}

func printUsage() {
	fmt.Print(`bilidown-cli - Bilibili video downloader CLI

Usage:
  bilidown-cli <command> [flags] [arguments]

Commands:
  login       Login via QR code (scan with Bilibili app)
  logout      Clear saved login credentials
  status      Check current login status
  info        Get video information
  download    Download video or audio

Global Flags:
  --json            Output in JSON format (AI agent friendly)
  --data-dir DIR    Custom data directory for credentials and config

Download Flags:
  --audio-only      Download only audio (saves as .m4a)
  --output-dir DIR  Output directory (default: ./download)
  --format CODE     Video quality code (default: best available)
  --page N          Which page/part to download (default: 1)

Examples:
  bilidown-cli login
  bilidown-cli login --json
  bilidown-cli info BV1xx411c7mD
  bilidown-cli info --json https://www.bilibili.com/video/BV1xx411c7mD
  bilidown-cli download BV1xx411c7mD
  bilidown-cli download --audio-only BV1xx411c7mD
  bilidown-cli download --format 120 --page 2 BV1xx411c7mD
  bilidown-cli download --output-dir ./videos BV1xx411c7mD
  bilidown-cli download BV1xx411c7mD ./videos

Video Quality Codes:
  127  8K Ultra HD          126  Dolby Vision
  125  HDR True Color       120  4K Ultra HD
  116  1080P 60fps          112  1080P High
   80  1080P                 74  720P 60fps
   64  720P                  32  480P
   16  360P

Run 'bilidown-cli <command> --help' for more information on a command.
`)
}
