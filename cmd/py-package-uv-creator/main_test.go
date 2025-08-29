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

func TestChooseArtifactsMixedCaseSdistForcesURL(t *testing.T) {
    rels := map[string][]pypiRelease{
        "1.0.0": {{Yanked:false, Packagetype:"sdist", Filename:"Demo-1.0.0.tar.gz", URL:"https://files/Demo-1.0.0.tar.gz", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"sha10"}}},
        "0.9.0": {{Yanked:false, Packagetype:"sdist", Filename:"demo-0.9.0.tar.gz", URL:"https://files/demo-0.9.0.tar.gz", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"sha09"}}},
    }
    lines, _, err := chooseArtifacts(rels, "")
    if err != nil { t.Fatalf("chooseArtifacts error: %v", err) }
    got := strings.Join(lines, "")
    if strings.Contains(got, "expand=False") {
        t.Fatalf("did not expect expand=False for sdist URLs: %s", got)
    }
    if !strings.Contains(got, "url=\"https://files/Demo-1.0.0.tar.gz\"") || !strings.Contains(got, "url=\"https://files/demo-0.9.0.tar.gz\"") {
        t.Fatalf("expected explicit URLs for mixed-case sdists, got: %s", got)
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
    // Ensure explicit URLs are present for versions
    if !strings.Contains(s, "version(\"0.0.12\", sha256=\"wsha12\", expand=False, url=\"https://files/metroapi-0.0.12.whl\")") {
        t.Fatalf("expected wheel URL for 0.0.12: %s", s)
    }
    if !strings.Contains(s, "version(\"0.0.9\", sha256=\"sha09\", url=\"https://files/metroapi-0.0.9.tar.gz\")") {
        t.Fatalf("expected sdist URL for 0.0.9: %s", s)
    }
    // Check descending versions
    i12 := strings.Index(s, "version(\"0.0.12\"")
    i09 := strings.Index(s, "version(\"0.0.9\"")
    if !(i12 >= 0 && i09 > i12) {
        t.Fatalf("expected 0.0.12 before 0.0.9: %d < %d\n%s", i12, i09, s)
    }
    // import_modules should include metro first, and install_test should import metro
    if !strings.Contains(s, "import_modules = [\"metro\"") {
        t.Fatalf("expected metro in import_modules: %s", s)
    }
    if !(strings.Contains(s, "def install_test") && strings.Contains(s, "import metro")) {
        t.Fatalf("expected install_test importing metro: %s", s)
    }
    // _pypi_package canonical name
    if !strings.Contains(s, "_pypi_package = \"metroapi\"") {
        t.Fatalf("missing _pypi_package: %s", s)
    }
    // No explicit uv dependency or env vars in recipe; handled by custom builder
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

func TestRecipeOmitsPypiHelperAndUsesExplicitURLs(t *testing.T) {
    // Simulate project with canonical name having capital letters (e.g., adjustText)
    resp := pypiResponse{
        Info: pypiInfo{
            Name: "adjustText",
            HomePage: "https://github.com/Phlya/adjustText",
        },
        Releases: map[string][]pypiRelease{
            "1.3.0": {{Yanked:false, Packagetype:"sdist", Filename:"adjustText-1.3.0.tar.gz", URL:"https://files/adjustText-1.3.0.tar.gz", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"sha"}}},
        },
    }
    data, _ := json.Marshal(resp)
    restore := withHTTPStub(t, roundTripFunc(func(req *http.Request) *http.Response {
        return httpResponse(200, "application/json", data)
    }))
    defer restore()

    cwd, _ := os.Getwd()
    dir := t.TempDir()
    _ = os.Chdir(dir)
    defer os.Chdir(cwd)

    if err := writeRecipe("adjusttext", ""); err != nil { t.Fatalf("writeRecipe: %v", err) }
    path := filepath.Join("packages", "py-adjusttext", "package.py")
    b, err := os.ReadFile(path)
    if err != nil { t.Fatalf("reading recipe: %v", err) }
    s := string(b)
    if strings.Contains(s, "pypi = ") {
        t.Fatalf("did not expect pypi helper path, got: %s", s)
    }
    if !strings.Contains(s, "url=\"https://files/adjustText-1.3.0.tar.gz\"") {
        t.Fatalf("expected explicit sdist URL, got: %s", s)
    }
}

func TestRunCLI_WithFlagFParsesNextArg(t *testing.T) {
    // Minimal PyPI JSON for package 'demopkg' with one sdist; also provide a wheel for import discovery
    resp := pypiResponse{
        Info: pypiInfo{
            Name:     "demopkg",
            HomePage: "https://example.com/demopkg",
        },
        Releases: map[string][]pypiRelease{
            "1.0.0": {
                {Yanked:false, Packagetype:"sdist", Filename:"demopkg-1.0.0.tar.gz", URL:"https://files/demopkg-1.0.0.tar.gz", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"sdistsha"}},
                {Yanked:false, Packagetype:"bdist_wheel", Filename:"demopkg-1.0.0-py3-none-any.whl", URL:"https://files/demopkg-1.0.0.whl", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"wheelsha"}},
            },
        },
    }
    data, _ := json.Marshal(resp)
    wheel := buildWheel([]string{"demopkg"}, true)
    restore := withHTTPStub(t, roundTripFunc(func(req *http.Request) *http.Response {
        if strings.Contains(req.URL.String(), "/pypi/demopkg/json") {
            return httpResponse(200, "application/json", data)
        }
        return httpResponse(200, "application/zip", wheel)
    }))
    defer restore()

    cwd, _ := os.Getwd()
    dir := t.TempDir()
    _ = os.Chdir(dir)
    defer os.Chdir(cwd)

    if err := runCLI([]string{"-f", "demopkg==1.0.0"}); err != nil {
        t.Fatalf("runCLI failed: %v", err)
    }
    if _, err := os.Stat(filepath.Join("packages", "py-demopkg", "package.py")); err != nil {
        t.Fatalf("expected recipe file to be created: %v", err)
    }
}

func TestSelectArtifactsPrefersLinuxWheelMatchingArch(t *testing.T) {
    // Provide both aarch64 and x86_64 linux wheels and ensure x86_64 is chosen on amd64 hosts.
    rels := map[string][]pypiRelease{
        "1.0.0": {
            {Yanked:false, Packagetype:"bdist_wheel", Filename:"demo-1.0.0-cp310-cp310-manylinux_2_17_aarch64.manylinux2014_aarch64.whl", URL:"https://files/demo-1.0.0-aarch64.whl", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"wsha_aarch64"}},
            {Yanked:false, Packagetype:"bdist_wheel", Filename:"demo-1.0.0-cp310-cp310-manylinux_2_17_x86_64.manylinux2014_x86_64.whl", URL:"https://files/demo-1.0.0-x86_64.whl", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"wsha_x86_64"}},
        },
    }
    lines, _, err := chooseArtifacts(rels, "")
    if err != nil { t.Fatalf("chooseArtifacts error: %v", err) }
    got := strings.Join(lines, "")
    if !strings.Contains(got, "https://files/demo-1.0.0-x86_64.whl") {
        t.Fatalf("expected x86_64 wheel to be selected, got: %s", got)
    }
    if strings.Contains(got, "aarch64.whl") {
        t.Fatalf("did not expect aarch64 wheel on x86_64 host: %s", got)
    }
}

func TestSelectArtifactsFallsBackToSdistIfOnlyOtherArchWheel(t *testing.T) {
    // Only aarch64 wheel available; should fall back to sdist on x86_64 hosts.
    rels := map[string][]pypiRelease{
        "1.0.0": {
            {Yanked:false, Packagetype:"sdist", Filename:"demo-1.0.0.tar.gz", URL:"https://files/demo-1.0.0.tar.gz", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"sdistsha"}},
            {Yanked:false, Packagetype:"bdist_wheel", Filename:"demo-1.0.0-cp310-cp310-manylinux_2_17_aarch64.manylinux2014_aarch64.whl", URL:"https://files/demo-1.0.0-aarch64.whl", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"wsha_aarch64"}},
        },
    }
    lines, _, err := chooseArtifacts(rels, "")
    if err != nil { t.Fatalf("chooseArtifacts error: %v", err) }
    got := strings.Join(lines, "")
    if !strings.Contains(got, "url=\"https://files/demo-1.0.0.tar.gz\"") || strings.Contains(got, "aarch64.whl") {
        t.Fatalf("expected fallback to sdist when wheel is wrong arch, got: %s", got)
    }
}

func TestWriteRecipe_EmitsPythonConstraints(t *testing.T) {
    // Simulate project with two versions and per-file requires_python metadata
    resp := pypiResponse{
        Info: pypiInfo{
            Name:     "requirespy",
            HomePage: "https://example.com/requirespy",
        },
        Releases: map[string][]pypiRelease{
            "1.2.0": {
                {Yanked:false, Packagetype:"bdist_wheel", Filename:"requirespy-1.2.0-py3-none-any.whl", URL:"https://files/requirespy-1.2.0.whl", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"wsha120"}, RequiresPython: ">=3.11"},
            },
            "1.1.0": {
                {Yanked:false, Packagetype:"bdist_wheel", Filename:"requirespy-1.1.0-py3-none-any.whl", URL:"https://files/requirespy-1.1.0.whl", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"wsha110"}, RequiresPython: ">=3.8,<3.12"},
            },
        },
    }
    data, _ := json.Marshal(resp)
    // Make the wheel fetch responder (not used for constraints, but discovery may call it)
    wheel := buildWheel([]string{"requirespy"}, true)
    restore := withHTTPStub(t, roundTripFunc(func(req *http.Request) *http.Response {
        if strings.Contains(req.URL.String(), "/pypi/requirespy/json") {
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

    if err := writeRecipe("requirespy", ""); err != nil {
        t.Fatalf("writeRecipe: %v", err)
    }

    path := filepath.Join("packages", "py-requirespy", "package.py")
    b, err := os.ReadFile(path)
    if err != nil { t.Fatalf("reading recipe: %v", err) }
    s := string(b)
    // Should contain per-version python constraints derived from requires_python
    if !strings.Contains(s, "depends_on(\"python@3.11:\"") || !strings.Contains(s, "when=\"@1.2.0\"") {
        t.Fatalf("missing python>=3.11 constraint for 1.2.0: %s", s)
    }
    if !strings.Contains(s, "depends_on(\"python@3.8:3.11\"") || !strings.Contains(s, "when=\"@1.1.0\"") {
        t.Fatalf("missing python>=3.8,<3.12 constraint mapped for 1.1.0: %s", s)
    }
}

func TestParseRequiresPython(t *testing.T) {
    cases := map[string]string{
        ">=3.11":         "3.11:",
        ">=3.8,<3.12":    "3.8:3.11",
        "==3.9.*":        "3.9:3.9",
        "<=3.10":         "0:3.10",
        "~=3.8":          "3.8:4.0",
    }
    for in, want := range cases {
        got := parseRequiresPython(in)
        if got != want {
            t.Fatalf("parseRequiresPython(%q) => %q, want %q", in, got, want)
        }
    }
}

func TestHomepageSelectionAndModuleChoiceFromProjectURLs(t *testing.T) {
    // HomePage field is empty; ProjectURLs includes a repository URL.
    resp := pypiResponse{
        Info: pypiInfo{
            Name:        "examplepkg",
            HomePage:    "",
            ProjectURL:  "",
            ProjectURLs: map[string]string{"Repository": "https://github.com/foo/bar"},
        },
        Releases: map[string][]pypiRelease{
            "1.0.0": {
                {Yanked:false, Packagetype:"bdist_wheel", Filename:"examplepkg-1.0.0-py3-none-any.whl", URL:"https://files/examplepkg-1.0.0.whl", Digests: struct{Sha256 string `json:"sha256"`}{Sha256:"wsha"}},
            },
        },
    }
    data, _ := json.Marshal(resp)
    // Wheel exposes modules: bar (from repo URL) and examplepkg
    wheel := buildWheel([]string{"bar", "examplepkg"}, true)
    restore := withHTTPStub(t, roundTripFunc(func(req *http.Request) *http.Response {
        if strings.Contains(req.URL.String(), "/pypi/examplepkg/json") {
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

    if err := writeRecipe("examplepkg", ""); err != nil {
        t.Fatalf("writeRecipe: %v", err)
    }

    path := filepath.Join("packages", "py-examplepkg", "package.py")
    b, err := os.ReadFile(path)
    if err != nil { t.Fatalf("reading recipe: %v", err) }
    s := string(b)
    // Homepage should be taken from ProjectURLs[Repository]
    if !strings.Contains(s, "homepage = \"https://github.com/foo/bar\"") {
        t.Fatalf("expected homepage from ProjectURLs[Repository]: %s", s)
    }
    // Primary import module should be 'bar' derived from homepage last segment
    if !strings.Contains(s, "import_modules = [\"bar\"") {
        t.Fatalf("expected primary module 'bar' chosen from homepage: %s", s)
    }
}
