package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func tarball(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write(body)
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

// fakeGitHub serves a release API answer and release files.
func fakeGitHub(t *testing.T, files map[string][]byte, apiStatus int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" {
			w.WriteHeader(apiStatus)
			w.Write([]byte(`{"tag_name":"v0.3.0"}`))
			return
		}
		b, ok := files[strings.TrimPrefix(r.URL.Path, "/dl/v0.3.0/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	oldAPI, oldDL := releaseAPI, releaseDownload
	releaseAPI, releaseDownload = srv.URL+"/api", srv.URL+"/dl/"
	t.Cleanup(func() { releaseAPI, releaseDownload = oldAPI, oldDL })
}

func TestLatestRelease(t *testing.T) {
	fakeGitHub(t, nil, http.StatusOK)
	if tag, err := latestRelease(context.Background()); err != nil || tag != "v0.3.0" {
		t.Fatalf("got %q, %v", tag, err)
	}
	fakeGitHub(t, nil, http.StatusNotFound)
	if _, err := latestRelease(context.Background()); err == nil || !strings.Contains(err.Error(), "no bx release") {
		t.Fatalf("404: got %v", err)
	}
}

func TestFetchBinary(t *testing.T) {
	bin := []byte("#!fake bx binary")
	tgz := tarball(t, "bx", bin)
	sum := sha256.Sum256(tgz)
	asset := "bx_darwin_arm64.tar.gz"
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n")

	fakeGitHub(t, map[string][]byte{asset: tgz, "checksums.txt": sums}, http.StatusOK)
	got, err := fetchBinary(context.Background(), "v0.3.0", asset)
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("good release: got %q, %v", got, err)
	}

	// A tampered archive must be refused.
	bad := append(append([]byte{}, tgz...), 0)
	fakeGitHub(t, map[string][]byte{asset: bad, "checksums.txt": sums}, http.StatusOK)
	if _, err := fetchBinary(context.Background(), "v0.3.0", asset); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("tampered: got %v", err)
	}

	// No checksum line for the asset: refuse.
	fakeGitHub(t, map[string][]byte{asset: tgz, "checksums.txt": []byte("abc  other.tar.gz\n")}, http.StatusOK)
	if _, err := fetchBinary(context.Background(), "v0.3.0", asset); err == nil {
		t.Fatal("missing checksum: want an error")
	}
}

func TestUntarBinaryNeedsBx(t *testing.T) {
	if _, err := untarBinary(tarball(t, "other", []byte("x"))); err == nil {
		t.Fatal("an archive without bx must fail")
	}
}
