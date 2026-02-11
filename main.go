package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
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
	version := flag.String("version", "", "release tag, e.g. v1.2.3 (required)")
	filePattern := flag.String("file", "", "asset filename with {version} and/or {arch} tokens (required)")
	outDir := flag.String("out", ".", "output directory (will be created if missing)")
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

	if *repo == "" || *version == "" || *filePattern == "" {
		fmt.Fprintln(os.Stderr, "--repo, --version, and --file are required")
		os.Exit(2)
	}

	// Print out the repo
	if *debug {
		fmt.Println("Repo:", *repo)
		fmt.Println("Version:", *version)
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
	filename := strings.ReplaceAll(*filePattern, "{version}", *version)
	filename = strings.ReplaceAll(filename, "{os}", osSub)
	filename = strings.ReplaceAll(filename, "{arch}", archSub)

	downloadURL := "https://github.com/" + *repo + "/releases/download/" + *version + "/" + filename

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
		dst := filepath.Join(*outDir, filepath.Base(filename))
		err = copyFile(tmp, dst, 0o644)
		if err == nil {
			produced = []string{dst}
		}
	}
	if err != nil {
		fatalf("extract: %v", err)
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
	var produced []string
	for _, f := range r.File {
		fp := filepath.Join(outDir, f.Name)
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
		fp := filepath.Join(outDir, hdr.Name)
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
