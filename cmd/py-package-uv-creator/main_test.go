package main

import (
    "archive/zip"
    "bytes"
    "encoding/json"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

// ---- test helpers ----

type roundTripFunc func(*http.Request) *http.Response

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
    return f(req), nil
}

func httpResponse(status int, contentType string, body []byte) *http.Response {
    return &http.Response{
        StatusCode: status,
        Header:     http.Header{"Content-Type": []string{contentType}},
        Body:       io.NopCloser(bytes.NewReader(body)),
    }
}

func withHTTPStub(t *testing.T, fn roundTripFunc) func() {
    t.Helper()
    old := httpClient
    httpClient = &http.Client{Transport: fn}
    return func() { httpClient = old }
}

func buildWheel(topLevels []string, includeTopLevelTxt bool) []byte {
    buf := &bytes.Buffer{}
    zw := zip.NewWriter(buf)
    if includeTopLevelTxt {
        f, _ := zw.Create("pkg-1.0.dist-info/top_level.txt")
        _, _ = f.Write([]byte(strings.Join(topLevels, "\n")))
    } else {
        for _, m := range topLevels {
            if strings.Contains(m, "/") {
                // allow raw names too
                _, _ = zw.Create(m)
            } else {
                _, _ = zw.Create(m + "/__init__.py")
            }
        }
        _, _ = zw.Create("tests/__init__.py")
    }
    _ = zw.Close()
    return buf.Bytes()
}

// ---- unit tests ----

func TestVersionLessComparator(t *testing.T) {
    if !versionLess("1.2.2", "1.2.10") { t.Fatalf("expected 1.2.2 < 1.2.10") }
    if !versionLess("1.1.9", "1.2") { t.Fatalf("expected 1.1.9 < 1.2") }
    if versionLess("2.0", "1.99.99") { t.Fatalf("expected 2.0 > 1.99.99") }
}

func TestChooseArtifactsDescendingAndSuffix(t *testing.T) {
    rels := map[string][]pypiRelease{
        "0.9.0": {{Yanked:false, Packagetype:"sdist", Filename:"demo-0.9.0.tar.gz", URL:"https://files/d0.9.tar.gz", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"sha09"}}},
        "1.0.0": {{Yanked:false, Packagetype:"sdist", Filename:"demo-1.0.0.tar.gz", URL:"https://files/d1.0.tar.gz", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"sha10"}}},
    }
    lines, suffix, err := chooseArtifacts(rels, "")
    if err != nil { t.Fatalf("chooseArtifacts error: %v", err) }
    if suffix != ".tar.gz" { t.Fatalf("unexpected suffix %q", suffix) }
    // Expect newest first
    got := strings.Join(lines, "")
    if !strings.Contains(got, "version(\"1.0.0\"") || !strings.Contains(got, "version(\"0.9.0\"") {
        t.Fatalf("missing versions in output: %s", got)
    }
    if !strings.Contains(got, "1.0.0\", sha256=\"sha10\"") || !strings.Contains(got, "0.9.0\", sha256=\"sha09\"") {
        t.Fatalf("sha mismatch: %s", got)
    }
    if strings.Index(got, "\"1.0.0\"") > strings.Index(got, "\"0.9.0\"") {
        t.Fatalf("expected 1.0.0 before 0.9.0: %s", got)
    }
}

func TestChooseArtifactsWheelFallback(t *testing.T) {
    rels := map[string][]pypiRelease{
        "1.0.0": {{Yanked:false, Packagetype:"bdist_wheel", Filename:"demo-1.0.0-py3-none-any.whl", URL:"https://files/d1.0.whl", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"wsha"}}},
    }
    lines, _, err := chooseArtifacts(rels, "")
    if err != nil { t.Fatalf("chooseArtifacts error: %v", err) }
    got := strings.Join(lines, "")
    if !strings.Contains(got, "expand=False") || !strings.Contains(got, "url=\"https://files/d1.0.whl\"") {
        t.Fatalf("expected wheel URL fallback, got: %s", got)
    }
}

func TestDownloadAndDiscoverTopLevelModules_TopLevelTxt(t *testing.T) {
    wheel := buildWheel([]string{"metro", "conductor"}, true)
    restore := withHTTPStub(t, roundTripFunc(func(req *http.Request) *http.Response {
        return httpResponse(200, "application/zip", wheel)
    }))
    defer restore()
    mods, err := downloadAndDiscoverTopLevelModules("https://files/metroapi-0.0.12.whl")
    if err != nil { t.Fatalf("err: %v", err) }
    if len(mods) == 0 { t.Fatalf("no modules discovered") }
    if mods[0] != "metro" && mods[1] != "metro" {
        t.Fatalf("expected to include metro, got %v", mods)
    }
}

func TestDownloadAndDiscoverTopLevelModules_Fallback(t *testing.T) {
    wheel := buildWheel([]string{"pkg", "example.py"}, false)
    restore := withHTTPStub(t, roundTripFunc(func(req *http.Request) *http.Response {
        return httpResponse(200, "application/zip", wheel)
    }))
    defer restore()
    mods, err := downloadAndDiscoverTopLevelModules("https://files/demo.whl")
    if err != nil { t.Fatalf("err: %v", err) }
    found := false
    for _, m := range mods { if m == "pkg" { found = true; break } }
    if !found { t.Fatalf("expected to infer pkg from tree, got %v", mods) }
}

func TestDiscoverModulesAndPrimarySelection(t *testing.T) {
    // Prepare PyPI JSON with a wheel URL to fetch
    type pypiResp struct {
        Info     pypiInfo                 `json:"info"`
        Releases map[string][]pypiRelease `json:"releases"`
    }
    resp := pypiResp{
        Info: pypiInfo{
            Name: "metroapi",
            HomePage: "https://github.com/ricardo-agz/metro",
            ProjectURLs: map[string]string{"Homepage":"https://github.com/ricardo-agz/metro"},
        },
        Releases: map[string][]pypiRelease{
            "0.0.12": {
                {Yanked:false, Packagetype:"bdist_wheel", Filename:"metroapi-0.0.12-py3-none-any.whl", URL:"https://files/metroapi-0.0.12.whl", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"wsha"}},
            },
        },
    }
    data, _ := json.Marshal(resp)
    wheel := buildWheel([]string{"metro", "conductor"}, true)
    restore := withHTTPStub(t, roundTripFunc(func(req *http.Request) *http.Response {
        if strings.Contains(req.URL.String(), "/pypi/metroapi/json") {
            return httpResponse(200, "application/json", data)
        }
        // Wheel fetch
        return httpResponse(200, "application/zip", wheel)
    }))
    defer restore()

    // Use getPyPI + discoverImportModules
    py, err := getPyPI("metroapi")
    if err != nil { t.Fatalf("getPyPI: %v", err) }
    mods := discoverImportModules(py, "")
    if len(mods) == 0 { t.Fatalf("expected modules discovered") }
    mod := choosePrimaryImportModule(mods, "metroapi", py.Info.Name, py.Info.HomePage)
    if mod != "metro" { t.Fatalf("expected primary module metro, got %s (mods=%v)", mod, mods) }
}

func TestWriteRecipe_EndToEnd(t *testing.T) {
    // PyPI JSON with sdist and wheel for 0.0.12 and sdist for 0.0.9
    resp := pypiResponse{
        Info: pypiInfo{
            Name: "metroapi",
            HomePage: "https://github.com/ricardo-agz/metro",
            ProjectURLs: map[string]string{"Homepage":"https://github.com/ricardo-agz/metro"},
        },
        Releases: map[string][]pypiRelease{
            "0.0.12": {
                {Yanked:false, Packagetype:"sdist", Filename:"metroapi-0.0.12.tar.gz", URL:"https://files/metroapi-0.0.12.tar.gz", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"sha12"}},
                {Yanked:false, Packagetype:"bdist_wheel", Filename:"metroapi-0.0.12-py3-none-any.whl", URL:"https://files/metroapi-0.0.12.whl", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"wsha12"}},
            },
            "0.0.9": {
                {Yanked:false, Packagetype:"sdist", Filename:"metroapi-0.0.9.tar.gz", URL:"https://files/metroapi-0.0.9.tar.gz", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"sha09"}},
            },
        },
    }
    data, _ := json.Marshal(resp)
    wheel := buildWheel([]string{"metro", "conductor"}, true)
    restore := withHTTPStub(t, roundTripFunc(func(req *http.Request) *http.Response {
        if strings.Contains(req.URL.String(), "/pypi/metroapi/json") {
            return httpResponse(200, "application/json", data)
        }
        return httpResponse(200, "application/zip", wheel)
    }))
    defer restore()

    // Work in a temp directory
    cwd, _ := os.Getwd()
    dir := t.TempDir()
    _ = os.Chdir(dir)
    defer os.Chdir(cwd)

    if err := writeRecipe("metroapi", ""); err != nil { t.Fatalf("writeRecipe: %v", err) }

    path := filepath.Join("packages", "py-metroapi", "package.py")
    b, err := os.ReadFile(path)
    if err != nil { t.Fatalf("reading recipe: %v", err) }
    s := string(b)
    // Check pypi path suffix and dependency
    if !strings.Contains(s, "pypi = \"metroapi/metroapi-@.tar.gz\"") {
        t.Fatalf("missing pypi helper path: %s", s)
    }
    if !strings.Contains(s, "depends_on(\"py-uv\"") {
        t.Fatalf("missing py-uv dependency: %s", s)
    }
    // Check descending versions
    i12 := strings.Index(s, "version(\"0.0.12\"")
    i09 := strings.Index(s, "version(\"0.0.9\"")
    if !(i12 >= 0 && i09 > i12) {
        t.Fatalf("expected 0.0.12 before 0.0.9: %d < %d\n%s", i12, i09, s)
    }
    // import_modules should include metro first, and smoke test should import metro
    if !strings.Contains(s, "import_modules = [\"metro\"") {
        t.Fatalf("expected metro in import_modules: %s", s)
    }
    if !strings.Contains(s, "python(\"-c\", 'import metro')") {
        t.Fatalf("expected smoke test 'import metro': %s", s)
    }
    // _pypi_package canonical name
    if !strings.Contains(s, "_pypi_package = \"metroapi\"") {
        t.Fatalf("missing _pypi_package: %s", s)
    }
    // UV_PYTHON is set
    if !strings.Contains(s, "UV_PYTHON") {
        t.Fatalf("expected UV_PYTHON to be set: %s", s)
    }
}

func TestFormatImportModules(t *testing.T) {
    got := formatImportModules([]string{"a", "b", "a", "c", "d", "e"}, "a")
    // Should include fallback first and limit to 4
    if !strings.HasPrefix(got, "\"a\",") {
        t.Fatalf("fallback not prioritized: %s", got)
    }
    if strings.Count(got, ",") > 3 { // 4 entries => 3 commas
        t.Fatalf("too many modules emitted: %s", got)
    }
}

func TestEscapePyStr(t *testing.T) {
    s := escapePyStr("a\n\"b\"")
    if strings.Contains(s, "\n") || !strings.Contains(s, "\\\"") {
        t.Fatalf("not escaped properly: %s", s)
    }
}

