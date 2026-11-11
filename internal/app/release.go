package app

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const releaseRepo = "Facets-cloud/bx"

// Variables so tests can point them at a local server.
var (
	releaseAPI      = "https://api.github.com/repos/" + releaseRepo + "/releases/latest"
	releaseDownload = "https://github.com/" + releaseRepo + "/releases/download/"
)

// maxBinary bounds what an update downloads and unpacks.
const maxBinary = 200 << 20

func httpGet(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	// GitHub's API wants a User-Agent; a generic one names nobody.
	req.Header.Set("User-Agent", "bx-cli")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBinary+1))
	if err == nil && len(body) > maxBinary {
		err = errors.New("response too large")
	}
	return body, resp.StatusCode, err
}

// latestRelease returns the newest release tag, such as v0.2.0.
func latestRelease(ctx context.Context) (string, error) {
	body, status, err := httpGet(ctx, releaseAPI)
	switch {
	case err != nil:
		return "", fmt.Errorf("look up the latest bx release: %w", err)
	case status == http.StatusNotFound:
		return "", errors.New("no bx release has been published yet")
	case status != http.StatusOK:
		return "", fmt.Errorf("look up the latest bx release: GitHub answered %d", status)
	}
	var r struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.Tag == "" {
		return "", errors.New("look up the latest bx release: unexpected answer from GitHub")
	}
	return r.Tag, nil
}

// fetchBinary downloads the release tarball for asset, checks it against
// the release's checksums.txt, and returns the bx binary inside it.
func fetchBinary(ctx context.Context, tag, asset string) ([]byte, error) {
	sums, err := download(ctx, tag, "checksums.txt")
	if err != nil {
		return nil, err
	}
	want, err := checksumFor(sums, asset)
	if err != nil {
		return nil, err
	}
	tgz, err := download(ctx, tag, asset)
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(tgz)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("%s does not match its checksum; not installing it", asset)
	}
	return untarBinary(tgz)
}

func download(ctx context.Context, tag, name string) ([]byte, error) {
	body, status, err := httpGet(ctx, releaseDownload+tag+"/"+name)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", name, err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("download %s: GitHub answered %d", name, status)
	}
	return body, nil
}

// checksumFor finds asset's SHA-256 in shasum output ("<hex>  <name>").
func checksumFor(sums []byte, asset string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt lists no %s", asset)
}

// untarBinary returns the regular file named "bx" from a .tar.gz.
func untarBinary(tgz []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, fmt.Errorf("unpack the release: %w", err)
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("the release archive has no bx binary")
		}
		if err != nil {
			return nil, fmt.Errorf("unpack the release: %w", err)
		}
		if h.Name != "bx" || h.Typeflag != tar.TypeReg {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxBinary+1))
		if err != nil || len(b) > maxBinary {
			return nil, errors.New("unpack the release: bx binary unreadable or too large")
		}
		return b, nil
	}
}
