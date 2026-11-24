package standin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func get(t *testing.T, address string) (int, []byte) {
	t.Helper()

	response, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	return response.StatusCode, body
}

func releasesServer(t *testing.T, dir string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	NewReleases(dir).Routes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server
}

// With no folder, every address answers 404, as GitHub does for a
// repository with no release.
func TestReleasesWithNoFolderHaveNoRelease(t *testing.T) {
	server := releasesServer(t, "")
	for _, address := range []string{
		"/repos/vvilla-01/matty-plugin/releases/latest",
		"/vvilla-01/matty-plugin/releases/download/v4.9.0/ceo.villa.matty-4.9.0-linux-amd64.tar.gz",
		"/vvilla-01/matty-plugin/releases/tag/v4.9.0",
		"/fake/update/rsa.pem",
	} {
		if status, _ := get(t, server.URL+address); status != http.StatusNotFound {
			t.Errorf("%s: %d", address, status)
		}
	}
}

// The newest version among the folder's bundles is the newest release, with
// its bundles as its files; each bundle and both certificates are served,
// and nothing else in the folder.
func TestReleasesServeTheNewestBundleAndTheCertificates(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"ceo.villa.matty-4.9.0-linux-amd64.tar.gz":  "nine",
		"ceo.villa.matty-4.10.0-linux-amd64.tar.gz": "ten",
		"ceo.villa.matty-4.10.0-linux-arm64.tar.gz": "ten on arm",
		"rsa.pem":     "rsa certificate",
		"mldsa87.pem": "ml-dsa certificate",
		"secret.txt":  "not served",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	server := releasesServer(t, dir)

	status, body := get(t, server.URL+"/repos/vvilla-01/matty-plugin/releases/latest")
	if status != http.StatusOK {
		t.Fatalf("latest: %d", status)
	}
	var latest struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &latest); err != nil {
		t.Fatal(err)
	}
	if latest.Tag != "v4.10.0" || latest.Draft || latest.Prerelease || len(latest.Assets) != 2 || latest.Assets[0].Name != "ceo.villa.matty-4.10.0-linux-amd64.tar.gz" {
		t.Fatalf("latest: %s", body)
	}

	for address, want := range map[string]string{
		"/vvilla-01/matty-plugin/releases/download/v4.10.0/ceo.villa.matty-4.10.0-linux-amd64.tar.gz": "ten",
		"/vvilla-01/matty-plugin/releases/download/v4.9.0/ceo.villa.matty-4.9.0-linux-amd64.tar.gz":   "nine",
		"/fake/update/rsa.pem":     "rsa certificate",
		"/fake/update/mldsa87.pem": "ml-dsa certificate",
	} {
		if status, body := get(t, server.URL+address); status != http.StatusOK || string(body) != want {
			t.Errorf("%s: %d %q", address, status, body)
		}
	}
	for _, address := range []string{
		"/vvilla-01/matty-plugin/releases/download/v4.9.0/ceo.villa.matty-4.10.0-linux-amd64.tar.gz",
		"/vvilla-01/matty-plugin/releases/download/v4.10.0/secret.txt",
		"/vvilla-01/matty-plugin/releases/download/v4.10.0/../secret.txt",
		"/fake/update/secret.txt",
		"/vvilla-01/matty-plugin/releases/tag/v4.11.0",
	} {
		if status, _ := get(t, server.URL+address); status != http.StatusNotFound {
			t.Errorf("%s: %d", address, status)
		}
	}
	if status, _ := get(t, server.URL+"/vvilla-01/matty-plugin/releases/tag/v4.10.0"); status != http.StatusOK {
		t.Errorf("the release's page: %d", status)
	}
}

// Each request for a release is logged with its method, path and status
// and nothing else (no query, no header), so a run can read from the log
// what the plugin asked and when it asked nothing.
func TestReleasesLogEachRequest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ceo.villa.matty-4.9.0-linux-amd64.tar.gz"), []byte("nine"), 0o644); err != nil {
		t.Fatal(err)
	}
	var lines []string
	var lock sync.Mutex
	releases := NewReleases(dir)
	releases.logf = func(format string, args ...any) {
		lock.Lock()
		defer lock.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	mux := http.NewServeMux()
	releases.Routes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	get(t, server.URL+"/repos/vvilla-01/matty-plugin/releases/latest?secret=words")
	get(t, server.URL+"/vvilla-01/matty-plugin/releases/download/v4.9.0/ceo.villa.matty-4.9.0-linux-amd64.tar.gz")
	get(t, server.URL+"/vvilla-01/matty-plugin/releases/tag/v4.8.0")
	get(t, server.URL+"/fake/update/rsa.pem")
	request, err := http.NewRequest(http.MethodPost, server.URL+"/vvilla-01/matty-plugin/releases/download/a%0Ab", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	lock.Lock()
	defer lock.Unlock()
	want := []string{
		`release request: GET "/repos/vvilla-01/matty-plugin/releases/latest" 200`,
		`release request: GET "/vvilla-01/matty-plugin/releases/download/v4.9.0/ceo.villa.matty-4.9.0-linux-amd64.tar.gz" 200`,
		`release request: GET "/vvilla-01/matty-plugin/releases/tag/v4.8.0" 404`,
		`release request: GET "/fake/update/rsa.pem" 404`,
		`release request: POST "/vvilla-01/matty-plugin/releases/download/a\nb" 404`,
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("logged:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

// Nothing is logged while nothing is asked.
func TestReleasesLogNothingUnasked(t *testing.T) {
	releases := NewReleases("")
	logged := 0
	releases.logf = func(string, ...any) { logged++ }
	releases.Routes(http.NewServeMux())
	if logged != 0 {
		t.Fatalf("%d lines", logged)
	}
}

// The newest release of Matty is the version in the folder's app-latest,
// with its app as its one file; absent, or not a version, 404, as GitHub
// answers before the first release. Each request is logged as the others.
func TestReleasesServeTheNewestMattyFromAppLatest(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	var lock sync.Mutex
	releases := NewReleases(dir)
	releases.logf = func(format string, args ...any) {
		lock.Lock()
		defer lock.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	mux := http.NewServeMux()
	releases.Routes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	const address = "/repos/vvilla-01/matty/releases/latest"

	if status, _ := get(t, server.URL+address); status != http.StatusNotFound {
		t.Fatalf("no app-latest: %d", status)
	}

	if err := os.WriteFile(filepath.Join(dir, "app-latest"), []byte(" 0.1.33\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, body := get(t, server.URL+address)
	if status != http.StatusOK {
		t.Fatalf("app-latest: %d", status)
	}
	var answer struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.TagName != "v0.1.33" || answer.Draft || answer.Prerelease || len(answer.Assets) != 1 || answer.Assets[0].Name != "matty-0.1.33.apk" {
		t.Fatalf("answer: %s", body)
	}

	// The plugin's own releases are not the app's.
	if status, _ := get(t, server.URL+"/repos/vvilla-01/matty-plugin/releases/latest"); status != http.StatusNotFound {
		t.Fatalf("the plugin's latest: %d", status)
	}

	for _, content := range []string{"", "latest", "v0.1.33", "0.1", "0.1.33\n0.1.34\n"} {
		if err := os.WriteFile(filepath.Join(dir, "app-latest"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if status, _ := get(t, server.URL+address); status != http.StatusNotFound {
			t.Fatalf("app-latest %q: %d", content, status)
		}
	}

	lock.Lock()
	defer lock.Unlock()
	if len(lines) != 8 || lines[0] != `release request: GET "/repos/vvilla-01/matty/releases/latest" 404` || lines[1] != `release request: GET "/repos/vvilla-01/matty/releases/latest" 200` {
		t.Fatalf("logged:\n%s", strings.Join(lines, "\n"))
	}
}
