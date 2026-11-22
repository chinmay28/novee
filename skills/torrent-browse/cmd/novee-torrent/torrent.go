package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chinmay28/novee/skills/web-browse/page"
)

// Only .torrent files are ever fetched: a few kilobytes that describe a
// download, never the download itself. Nothing here starts a torrent client,
// follows a magnet link or fetches what a torrent points to, and what comes
// back from a site is kept only if it really is a small torrent file.

const maxTorrent = 5 << 20

// fetchJS downloads a file with the page's own sign-in and returns it as text.
const fetchJS = `(async (u) => {
  const r = await fetch(u, {credentials: 'include'});
  const b = new Uint8Array(await r.arrayBuffer());
  if (b.length > %d) return {status: r.status, big: b.length};
  let s = ''; for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode.apply(null, b.subarray(i, i + 0x8000));
  return {status: r.status, type: r.headers.get('content-type') || '', name: r.headers.get('content-disposition') || '', data: btoa(s)};
})(%q)`

var (
	dispName = regexp.MustCompile(`filename\*?=(?:UTF-8'')?"?([^";]+)`)
	badChars = regexp.MustCompile(`[^\p{L}\p{N} ._()\[\]+&',-]+`)
)

// isTorrent says whether data is a bencoded torrent file: a dictionary that
// has an "info" entry.
func isTorrent(data []byte) bool {
	return len(data) > 40 && data[0] == 'd' && data[len(data)-1] == 'e' && bytes.Contains(data, []byte("4:info"))
}

// torrentName reads the name the torrent gives itself ("4:name12:Some.Release").
func torrentName(data []byte) string {
	i := bytes.Index(data, []byte("4:name"))
	if i < 0 {
		return ""
	}
	rest := data[i+6:]
	colon := bytes.IndexByte(rest, ':')
	if colon < 1 || colon > 6 {
		return ""
	}
	n, err := strconv.Atoi(string(rest[:colon]))
	if err != nil || n < 1 || n > 400 || colon+1+n > len(rest) {
		return ""
	}
	return string(rest[colon+1 : colon+1+n])
}

func safeName(s string) string {
	s = strings.TrimSpace(badChars.ReplaceAllString(s, " "))
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 150 {
		s = s[:150]
	}
	return strings.Trim(s, ". ")
}

// fetchTorrent gets the .torrent file of a hit through the signed-in page and saves it in dir.
func fetchTorrent(ctx context.Context, p *page.Page, h hit, dir string) (string, error) {
	if h.Download == "" {
		if h.Magnet {
			return "", errors.New("the site gives only a magnet link for it, and magnet links are not followed")
		}
		return "", errors.New("its row has no link to a .torrent file")
	}
	var r struct {
		Status int
		Big    int
		Type   string
		Name   string
		Data   string
	}
	if err := p.Eval(ctx, fmt.Sprintf(fetchJS, maxTorrent, h.Download), &r); err != nil {
		return "", fmt.Errorf("the site did not hand over the file: %w", err)
	}
	if r.Big > 0 {
		return "", fmt.Errorf("refused: the site sent %d MB, far more than a .torrent file", r.Big>>20)
	}
	data, err := base64.StdEncoding.DecodeString(r.Data)
	if err != nil || !isTorrent(data) {
		what := "a page instead of a file"
		if r.Status >= 400 {
			what = fmt.Sprintf("error %d", r.Status)
		}
		return "", fmt.Errorf("the site answered with %s (not signed in, out of download slots, or a ratio rule); nothing was saved", what)
	}
	name := torrentName(data)
	if name == "" {
		if m := dispName.FindStringSubmatch(r.Name); m != nil {
			name = strings.TrimSuffix(m[1], ".torrent")
		}
	}
	if name = safeName(name); name == "" {
		name = safeName(h.Name)
	}
	if name == "" {
		name = "torrent-" + time.Now().Format("20060102-150405")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+".torrent")
	return path, os.WriteFile(path, data, 0o600)
}

// zipFiles puts files into one archive.
func zipFiles(out string, files []string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	w := zip.NewWriter(f)
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		e, err := w.Create(filepath.Base(path))
		if err != nil {
			return err
		}
		if _, err := e.Write(data); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	return f.Close()
}
