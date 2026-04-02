package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// Version is the application version, injected at build time via ldflags
var Version = "dev"

func main() {
	// Set the build version from the build info if not set by the build system
	if Version == "dev" || Version == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
				Version = bi.Main.Version
			}
		}
	}

	fmt.Println("ghrelgrab version:", Version)

	repo := flag.String("repo", "", "owner/repo for GitHub project (required)")
	version := flag.String("version", "", "release tag, e.g. v1.2.3 (mutually exclusive with --latest)")
	latest := flag.Bool("latest", false, "use the latest non-pre-release version from the GitHub API (mutually exclusive with --version)")
	filePattern := flag.String("file", "", "asset filename with {version} and/or {arch} tokens (required)")
	outDir := flag.String("out", ".", "output directory (will be created if missing)")
	name := flag.String("name", "", "override the output filename of the downloaded binary")
	debug := flag.Bool("debug", false, "enable debug output")

	// Operating System
	osFlag := flag.String("os", runtime.GOOS, "override OS used for {os} substitution")
	osMapFlag := flag.String("os-map", "", "comma-separated DETECTED=SUBSTITUTE pairs (e.g. 'linux=ubuntu,windows=win32')")

	// Arch handling
	arch := flag.String("arch", runtime.GOARCH, "override arch used for {arch} substitution")
	archMapFlag := flag.String("arch-map", "", "comma-separated DETECTED=SUBSTITUTE pairs (e.g. 'amd64=x86_64,arm64=aarch64')")

	// Optional token for authenticated downloads (private repos/assets)
	token := flag.String("token", os.Getenv("GH_TOKEN"), "GitHub token (defaults to GH_TOKEN env)")

	flag.Parse()

	if *repo == "" {
		fmt.Fprintln(os.Stderr, "--repo is required")
		os.Exit(2)
	}
	if *latest && *version != "" {
		fmt.Fprintln(os.Stderr, "--version and --latest are mutually exclusive")
		os.Exit(2)
	}
	if !*latest && *version == "" {
		fmt.Fprintln(os.Stderr, "--version is required (or use --latest)")
		os.Exit(2)
	}
	if *filePattern == "" {
		fmt.Fprintln(os.Stderr, "--file is required")
		os.Exit(2)
	}

	// Resolve the version: either use the provided flag or fetch the latest from GitHub
	resolvedVersion := *version
	if *latest {
		var err error
		resolvedVersion, err = fetchLatestVersion(*repo, *token)
		if err != nil {
			fatalf("fetch latest version: %v", err)
		}
	}

	// Print out the repo
	if *debug {
		fmt.Println("Repo:", *repo)
		fmt.Println("Version:", resolvedVersion)
		fmt.Println("OS:", *osFlag)
		fmt.Println("Architecture:", *arch)
	}

	// prepare OS substitution
	osSub := *osFlag
	m := parseSubstMap(*osMapFlag)
	if sub, ok := m[osSub]; ok {
		osSub = sub
	}

	if *debug && osSub != *osFlag {
		fmt.Println("OS Substituted:", osSub)
	}

	// prepare arch substitution
	archSub := *arch
	m = parseSubstMap(*archMapFlag)
	if sub, ok := m[archSub]; ok {
		archSub = sub
	}

	if *debug && archSub != *arch {
		fmt.Println("Architecture Substituted:", archSub)
	}

	// Build final filename and URL
	filename := strings.ReplaceAll(*filePattern, "{version}", resolvedVersion)
	filename = strings.ReplaceAll(filename, "{os}", osSub)
	filename = strings.ReplaceAll(filename, "{arch}", archSub)

	downloadURL := "https://github.com/" + *repo + "/releases/download/" + resolvedVersion + "/" + filename

	if *debug {
		fmt.Println("Download URL:", downloadURL)
	}

	// Ensure output dir
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatalf("create out dir: %v", err)
	}

	// Download
	tmp, err := fetch(downloadURL, *token)
	if err != nil {
		fatalf("download %s: %v", downloadURL, err)
	}
	defer os.Remove(tmp)

	// Extract or save as-is
	lower := strings.ToLower(filename)
	var produced []string
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		if *debug {
			fmt.Println("Unpacking tar.gz")
		}
		produced, err = extractTarGz(tmp, *outDir)
	case strings.HasSuffix(lower, ".zip"):
		if *debug {
			fmt.Println("Unpacking zip")
		}
		produced, err = extractZip(tmp, *outDir)
	default:
		baseName := filepath.Base(filename)
		if *name != "" {
			baseName = *name
		}
		dst := filepath.Join(*outDir, baseName)
		err = copyFile(tmp, dst, 0o644)
		if err == nil {
			produced = []string{dst}
		}
	}
	if err != nil {
		fatalf("extract: %v", err)
	}

	// When --name is set and an archive was unpacked, find the target binary and rename it
	if *name != "" && (strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".zip")) {
		var renameErr error
		produced, renameErr = findAndRename(produced, *outDir, *name)
		if renameErr != nil {
			fatalf("rename: %v", renameErr)
		}
	}

	// Print resulting file paths to stdout (one per line)
	for _, p := range produced {
		fmt.Println("Saved:", p)
	}
}

// parseSubstMap parses "a=b,c=d" into map{"a":"b","c":"d"}
func parseSubstMap(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	parts := strings.Split(s, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.TrimSpace(kv[0])
		v := strings.TrimSpace(kv[1])
		if k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

// fetchLatestVersion queries the GitHub API for the latest non-pre-release,
// non-draft release and returns its tag name.
func fetchLatestVersion(repo, token string) (string, error) {
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ghrelgrab")
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if release.TagName == "" {
		return "", fmt.Errorf("no tag_name in response")
	}
	return release.TagName, nil
}

// findAndRename locates the appropriate binary among the extracted files and
// renames it to name inside outDir. The selection priority is:
//  1. A file whose base name already matches name exactly.
//  2. The sole executable file (mode & 0o111 != 0).
//  3. Among multiple executables, the one whose name most closely matches name.
//  4. The first regular file as a last resort.
func findAndRename(files []string, outDir, name string) ([]string, error) {
	targetPath := filepath.Join(outDir, name)

	// Pass 1: exact base name match
	for i, f := range files {
		if filepath.Base(f) == name {
			if f != targetPath {
				if err := os.Rename(f, targetPath); err != nil {
					return nil, err
				}
				files[i] = targetPath
			}
			return files, nil
		}
	}

	// Pass 2: collect executables
	var execIdxs []int
	for i, f := range files {
		info, err := os.Stat(f)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Mode()&0o111 != 0 {
			execIdxs = append(execIdxs, i)
		}
	}

	pickIdx := -1
	switch len(execIdxs) {
	case 1:
		pickIdx = execIdxs[0]
	default:
		nameLower := strings.ToLower(name)
		for _, i := range execIdxs {
			base := strings.ToLower(filepath.Base(files[i]))
			if strings.Contains(base, nameLower) || strings.Contains(nameLower, base) {
				pickIdx = i
				break
			}
		}
		if pickIdx == -1 && len(execIdxs) > 0 {
			pickIdx = execIdxs[0]
		}
	}

	if pickIdx >= 0 {
		if files[pickIdx] != targetPath {
			if err := os.Rename(files[pickIdx], targetPath); err != nil {
				return nil, err
			}
			files[pickIdx] = targetPath
		}
		return files, nil
	}

	// Pass 3: fall back to first regular file
	for i, f := range files {
		info, err := os.Stat(f)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if f != targetPath {
			if err := os.Rename(f, targetPath); err != nil {
				return nil, err
			}
			files[i] = targetPath
		}
		return files, nil
	}

	return files, fmt.Errorf("no regular file found to rename to %q", name)
}

func fetch(url, token string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ghrelgrab")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	f, err := os.CreateTemp("", "ghrelgrab-*")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func extractZip(zipPath, outDir string) ([]string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	absOutDir, err := filepath.Abs(outDir)
	if err != nil {
		return nil, err
	}
	var produced []string
	for _, f := range r.File {
		fp, err := filepath.Abs(filepath.Join(outDir, f.Name))
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(fp, absOutDir+string(filepath.Separator)) {
			return nil, fmt.Errorf("illegal file path in archive: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(fp, 0o755); err != nil {
				return nil, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			return nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		w, err := os.OpenFile(fp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return nil, err
		}
		if _, err := io.Copy(w, rc); err != nil {
			rc.Close()
			w.Close()
			return nil, err
		}
		rc.Close()
		w.Close()
		produced = append(produced, fp)
	}
	return produced, nil
}

func extractTarGz(gzPath, outDir string) ([]string, error) {
	in, err := os.Open(gzPath)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	absOutDir, err := filepath.Abs(outDir)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	var produced []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		fp, err := filepath.Abs(filepath.Join(outDir, hdr.Name))
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(fp, absOutDir+string(filepath.Separator)) {
			return nil, fmt.Errorf("illegal file path in archive: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(fp, 0o755); err != nil {
				return nil, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
				return nil, err
			}
			w, err := os.OpenFile(fp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return nil, err
			}
			if _, err := io.Copy(w, tr); err != nil {
				w.Close()
				return nil, err
			}
			w.Close()
			produced = append(produced, fp)
		}
	}
	return produced, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"", a...)
	os.Exit(1)
}
