package standin

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Releases stands in for what Matty Plugin asks GitHub for its own updates,
// and for the two certificates a stand-in bundle is checked against:
//
//	GET /repos/vvilla-01/matty-plugin/releases/latest                the newest release, as GitHub's API answers
//	GET /vvilla-01/matty-plugin/releases/download/v<version>/<file>  a bundle
//	GET /vvilla-01/matty-plugin/releases/tag/v<version>               the release's page, a line of text
//	GET /fake/update/rsa.pem and /fake/update/mldsa87.pem             the certificates
//
// It serves them from a folder (fakebb -releases DIR) as
// build/make-standin-update.sh leaves one: bundles named
// ceo.villa.matty-<version>-<platform>.tar.gz, and rsa.pem and mldsa87.pem
// beside them. The newest version among the bundles is the newest release,
// with each of its bundles as a file of it. With no folder, or none in it,
// every one of these answers 404, as GitHub answers for a repository with no
// release yet. Only the stand-in build of the plugin (server/standin.go)
// asks here.
//
// Each request to one of these is logged with its method, path and status,
// and nothing else, so a run can read from the log what was asked and see
// that nothing was asked while the plugin's Look for updates was off.
type Releases struct {
	dir string

	// logf writes a line to the log: log.Printf, which tests replace.
	logf func(format string, args ...any)
}

// NewReleases serves the folder dir, or nothing when it is "".
func NewReleases(dir string) *Releases {
	return &Releases{dir: dir, logf: log.Printf}
}

const releasesRepo = "/vvilla-01/matty-plugin"

var standInBundleName = regexp.MustCompile(`^ceo\.villa\.matty-((?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8}))-([a-z0-9]+-[a-z0-9]+)\.tar\.gz$`)

// Routes puts it on mux.
func (s *Releases) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/repos"+releasesRepo+"/releases/latest", s.logged(s.latest))
	mux.HandleFunc(releasesRepo+"/releases/download/", s.logged(s.download))
	mux.HandleFunc(releasesRepo+"/releases/tag/", s.logged(s.page))
	mux.HandleFunc("/fake/update/", s.logged(s.certificate))
}

// logged is handler, with one line in the log for each request once it is
// answered: "release request: GET "/repos/vvilla-01/matty-plugin/releases/latest" 404".
// The path is quoted, so nothing in it can start a line of its own.
func (s *Releases) logged(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := &statusWriter{ResponseWriter: w}
		handler(status, r)
		if status.code == 0 {
			status.code = http.StatusOK
		}
		s.logf("release request: %s %q %d", r.Method, r.URL.Path, status.code)
	}
}

// statusWriter keeps the status a handler answered with.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// bundles is the folder's bundles by version.
func (s *Releases) bundles() map[string][]string {
	found := map[string][]string{}
	if s.dir == "" {
		return found
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return found
	}
	for _, entry := range entries {
		if match := standInBundleName.FindStringSubmatch(entry.Name()); match != nil && entry.Type().IsRegular() {
			found[match[1]] = append(found[match[1]], entry.Name())
		}
	}

	return found
}

// newest is the highest version, part by part.
func newest(versions map[string][]string) string {
	best := ""
	for version := range versions {
		if best == "" || later(version, best) {
			best = version
		}
	}

	return best
}

func later(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range 3 {
		x, _ := strconv.Atoi(as[i])
		y, _ := strconv.Atoi(bs[i])
		if x != y {
			return x > y
		}
	}

	return false
}

func (s *Releases) latest(w http.ResponseWriter, r *http.Request) {
	bundles := s.bundles()
	version := newest(bundles)
	if r.Method != http.MethodGet || version == "" {
		http.NotFound(w, r)
		return
	}

	files := bundles[version]
	sort.Strings(files)
	assets := make([]map[string]any, 0, len(files))
	for _, name := range files {
		info, err := os.Stat(filepath.Join(s.dir, name))
		if err != nil {
			continue
		}
		assets = append(assets, map[string]any{
			"name":                 name,
			"size":                 info.Size(),
			"browser_download_url": "http://" + r.Host + releasesRepo + "/releases/download/v" + version + "/" + name,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"tag_name":   "v" + version,
		"name":       "Matty Plugin " + version,
		"draft":      false,
		"prerelease": false,
		"assets":     assets,
	})
}

func (s *Releases) download(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, releasesRepo+"/releases/download/")
	tag, name, ok := strings.Cut(rest, "/")
	match := standInBundleName.FindStringSubmatch(name)
	if r.Method != http.MethodGet || !ok || match == nil || tag != "v"+match[1] {
		http.NotFound(w, r)
		return
	}
	for _, file := range s.bundles()[match[1]] {
		if file == name {
			w.Header().Set("Content-Type", "application/octet-stream")
			http.ServeFile(w, r, filepath.Join(s.dir, name))
			return
		}
	}

	http.NotFound(w, r)
}

func (s *Releases) page(w http.ResponseWriter, r *http.Request) {
	tag := strings.TrimPrefix(r.URL.Path, releasesRepo+"/releases/tag/")
	if _, ok := s.bundles()[strings.TrimPrefix(tag, "v")]; !ok || !strings.HasPrefix(tag, "v") {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "The stand-in's release %s of Matty Plugin, signed with throwaway keys for this machine only.\n", tag)
}

func (s *Releases) certificate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/fake/update/")
	if s.dir == "" || r.Method != http.MethodGet || (name != "rsa.pem" && name != "mldsa87.pem") {
		http.NotFound(w, r)
		return
	}
	raw, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/x-pem-file")
	_, _ = w.Write(raw)
}
