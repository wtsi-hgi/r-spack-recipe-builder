package main

import (
    "archive/zip"
    "bytes"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "regexp"
    "sort"
    "strings"
    "time"
)

// Extremely small utility to generate Spack PythonPackage recipes that install
// via uv. Dependencies are not modeled in Spack; uv resolves them at install.
//
// Usage:
//   py-package-uv-creator <pypi_pkg>[==<version>] [...]
// If no version is provided, all available versions from PyPI will be emitted.

type pypiRelease struct {
    Yanked      bool   `json:"yanked"`
    Packagetype string `json:"packagetype"`
    Filename    string `json:"filename"`
    URL         string `json:"url"`
    Digests     struct {
        Sha256 string `json:"sha256"`
    } `json:"digests"`
}

type pypiInfo struct {
    Name       string            `json:"name"`
    Summary   string            `json:"summary"`
    HomePage  string            `json:"home_page"`
    ProjectURL string           `json:"project_url"`
    ProjectURLs map[string]string `json:"project_urls"`
}

type pypiResponse struct {
    Info     pypiInfo                    `json:"info"`
    Releases map[string][]pypiRelease    `json:"releases"`
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

func getPyPI(packageName string) (*pypiResponse, error) {
    url := fmt.Sprintf("https://pypi.org/pypi/%s/json", packageName)
    resp, err := httpClient.Get(url)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return nil, fmt.Errorf("failed to retrieve package %s (HTTP %d)", packageName, resp.StatusCode)
    }
    var out pypiResponse
    if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
        return nil, err
    }
    return &out, nil
}

func pyify(packageName string) string {
    lowered := strings.ToLower(packageName)
    lowered = strings.ReplaceAll(lowered, ".", "-")
    lowered = strings.ReplaceAll(lowered, "_", "-")
    lowered = strings.ReplaceAll(lowered, " ", "-")
    if idx := strings.Index(lowered, "["); idx >= 0 {
        lowered = lowered[:idx]
    }
    return "py-" + lowered
}

func moduleImportName(packageName string) string {
    lowered := strings.ToLower(packageName)
    lowered = strings.ReplaceAll(lowered, "-", "_")
    lowered = strings.ReplaceAll(lowered, ".", "_")
    lowered = strings.ReplaceAll(lowered, " ", "_")
    if idx := strings.Index(lowered, "["); idx >= 0 {
        lowered = lowered[:idx]
    }
    return lowered
}

func classNameFromPackage(packageName string) string {
    s := strings.NewReplacer("-", ".", "_", ".").Replace(packageName)
    parts := strings.Split(s, ".")
    for i := range parts {
        if parts[i] == "" { continue }
        parts[i] = strings.ToUpper(parts[i][:1]) + strings.ToLower(parts[i][1:])
    }
    return strings.Join(parts, "")
}

func detectPypiSuffix(filename string) string {
    n := strings.ToLower(strings.TrimSpace(filename))
    switch {
    case strings.HasSuffix(n, ".tar.gz"):
        return ".tar.gz"
    case strings.HasSuffix(n, ".tar.bz2"):
        return ".tar.bz2"
    case strings.HasSuffix(n, ".tar.xz"):
        return ".tar.xz"
    case strings.HasSuffix(n, ".tgz"):
        return ".tgz"
    case strings.HasSuffix(n, ".zip"):
        return ".zip"
    default:
        return ".tar.gz"
    }
}

func chooseArtifacts(releases map[string][]pypiRelease, preferred string) ([]string, string, error) {
    // Return: version lines, pypi helper path suffix (if we can determine a stable suffix), error
    var versionLines []string
    // Collect and sort versions ascending using numeric-dotted comparator, then emit descending
    var keys []string
    for v := range releases { keys = append(keys, v) }
    if len(keys) == 0 { return nil, "", errors.New("no releases found") }
    sort.Slice(keys, func(i, j int) bool { return versionLess(keys[i], keys[j]) })

    // If preferred is set, only emit that version (if present)
    filter := func(v string) bool { return preferred == "" || preferred == "latest" || v == preferred }

    // Determine a default pypi helper suffix from any sdist we find
    pypiSuffix := ""
    // Track sdist name casing (prefix before version and suffix). If multiple
    // casings are seen across releases, we'll avoid relying on the pypi helper
    // and emit explicit URLs even for sdists, since filenames are case-sensitive.
    sdistNameCases := map[string]struct{}{}
    sdistSuffixRe := regexp.MustCompile(`(?i)\.(tar\.gz|tar\.bz2|tar\.xz|tgz|zip)$`)

    // First pass: determine suffix and whether sdists present and their casings
    type chosen struct{ v string; sdist, any, wheel *pypiRelease }
    chosenList := []chosen{}
    for i := len(keys) - 1; i >= 0; i-- {
        v := keys[i]
        if !filter(v) { continue }
        arts := releases[v]
        if len(arts) == 0 { continue }
        var sdist *pypiRelease
        var any *pypiRelease
        // select a preferred wheel
        var wheelFirst, wheelAny, wheelPy3, wheelPy2py3 *pypiRelease
        for _, a := range arts {
            if a.Yanked { continue }
            if any == nil { any = &a }
            if strings.EqualFold(a.Packagetype, "sdist") {
                if strings.TrimSpace(a.Digests.Sha256) != "" {
                    tmp := a
                    sdist = &tmp
                    // don't break; keep any for wheel fallback if needed
                }
            } else if strings.EqualFold(a.Packagetype, "bdist_wheel") {
                if wheelFirst == nil { tmp := a; wheelFirst = &tmp }
                lname := strings.ToLower(a.Filename)
                if strings.Contains(lname, "-any.whl") && wheelAny == nil { tmp := a; wheelAny = &tmp }
                if strings.Contains(lname, "-py3-") && wheelPy3 == nil { tmp := a; wheelPy3 = &tmp }
                if strings.Contains(lname, "-py2.py3-") && wheelPy2py3 == nil { tmp := a; wheelPy2py3 = &tmp }
            }
        }
        if sdist != nil {
            if pypiSuffix == "" {
                if m := sdistSuffixRe.FindStringSubmatch(strings.ToLower(sdist.Filename)); len(m) > 1 {
                    pypiSuffix = "." + m[1]
                } else {
                    pypiSuffix = ".tar.gz"
                }
            }
            base := sdist.Filename
            base = sdistSuffixRe.ReplaceAllString(base, "")
            if idx := strings.LastIndex(base, "-"); idx > 0 { base = base[:idx] }
            if base != "" { sdistNameCases[base] = struct{}{} }
        }
        // choose wheel preference
        wheel := wheelAny
        if wheel == nil { wheel = wheelPy3 }
        if wheel == nil { wheel = wheelPy2py3 }
        if wheel == nil { wheel = wheelFirst }
        chosenList = append(chosenList, chosen{v: v, sdist: sdist, any: any, wheel: wheel})
        if preferred != "" && preferred != "latest" { break }
    }

    if len(chosenList) == 0 {
        return nil, "", errors.New("no usable artifacts found")
    }

    // Determine if mixed-case sdist names are present
    mixedCase := len(sdistNameCases) > 1

    // Second pass: emit version lines using our decision criteria
    versionLines = nil
    for idx, ch := range chosenList {
        if ch.sdist != nil {
            // Prefer wheel for the newest version if available to maximize install success
            if idx == 0 && ch.wheel != nil {
                versionLines = append(versionLines, fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\", expand=False, url=\"%s\")\n", ch.v, ch.wheel.Digests.Sha256, ch.wheel.URL))
            } else if mixedCase {
                // Use explicit sdist URL but allow Spack to expand the archive
                versionLines = append(versionLines, fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\", url=\"%s\")\n", ch.v, ch.sdist.Digests.Sha256, ch.sdist.URL))
            } else {
                versionLines = append(versionLines, fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\")\n", ch.v, ch.sdist.Digests.Sha256))
            }
        } else if ch.any != nil {
            // If the fallback artifact is a wheel, disable expansion; otherwise allow default expansion
            isWheel := strings.HasSuffix(strings.ToLower(ch.any.Filename), ".whl") || strings.EqualFold(ch.any.Packagetype, "bdist_wheel")
            if isWheel {
                versionLines = append(versionLines, fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\", expand=False, url=\"%s\")\n", ch.v, ch.any.Digests.Sha256, ch.any.URL))
            } else {
                versionLines = append(versionLines, fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\", url=\"%s\")\n", ch.v, ch.any.Digests.Sha256, ch.any.URL))
            }
        }
    }

    // Without knowing the canonicalized name's first-letter folder, Spack's pypi helper accepts
    // the compact form "name/name-@.suffix" just like we set in recipes elsewhere.
    // We'll emit pypi path using the requested package name as provided by the user; this works
    // for the vast majority of packages.
    if pypiSuffix == "" {
        pypiSuffix = ".tar.gz"
    }
    return versionLines, pypiSuffix, nil
}

func selectIntrospectionVersion(releases map[string][]pypiRelease, preferred string) string {
    var keys []string
    for v := range releases { keys = append(keys, v) }
    sort.Strings(keys)
    isRc := func(v string) bool { return strings.Contains(strings.ToLower(v), "rc") }
    if preferred != "" && preferred != "latest" {
        // Use preferred if present
        for _, v := range keys { if v == preferred { return v } }
    }
    // Newest stable x.y.z if possible
    bestStable := ""
    for _, v := range keys {
        if isRc(v) { continue }
        if matched, _ := regexp.MatchString(`^\d+(?:\.\d+)*$`, v); matched {
            if bestStable == "" || versionLess(bestStable, v) { bestStable = v }
        }
    }
    if bestStable != "" { return bestStable }
    // Fallback: newest non-rc
    bestAny := ""
    for _, v := range keys {
        if isRc(v) { continue }
        if bestAny == "" || versionLess(bestAny, v) { bestAny = v }
    }
    if bestAny != "" { return bestAny }
    // Last resort: lexicographically last
    if len(keys) > 0 { return keys[len(keys)-1] }
    return ""
}

func versionLess(a, b string) bool {
    // Simple dotted comparator; not full PEP440 but good enough for ordering
    as := strings.Split(a, ".")
    bs := strings.Split(b, ".")
    maxLen := len(as)
    if len(bs) > maxLen { maxLen = len(bs) }
    parseSeg := func(seg string) (int, string) {
        n := 0
        i := 0
        for i < len(seg) && seg[i] >= '0' && seg[i] <= '9' { n = n*10 + int(seg[i]-'0'); i++ }
        return n, seg[i:]
    }
    for i := 0; i < maxLen; i++ {
        av, ar := 0, ""; bv, br := 0, ""
        if i < len(as) { av, ar = parseSeg(as[i]) }
        if i < len(bs) { bv, br = parseSeg(bs[i]) }
        if av != bv { return av < bv }
        if ar != br { return ar < br }
    }
    return len(as) < len(bs)
}

func findWheelURLForVersion(arts []pypiRelease) string {
    // Prefer universal/py3 wheels; else any wheel
    // Collect by Python tag order: any, py3, py2.py3, else first
    var anyURL, py3URL, py2py3URL, firstWheel string
    for _, a := range arts {
        if a.Yanked { continue }
        if !strings.EqualFold(a.Packagetype, "bdist_wheel") { continue }
        if firstWheel == "" { firstWheel = a.URL }
        name := strings.ToLower(a.Filename)
        switch {
        case strings.Contains(name, "-any.whl"):
            if anyURL == "" { anyURL = a.URL }
        case strings.Contains(name, "-py3-"):
            if py3URL == "" { py3URL = a.URL }
        case strings.Contains(name, "-py2.py3-"):
            if py2py3URL == "" { py2py3URL = a.URL }
        }
    }
    if anyURL != "" { return anyURL }
    if py3URL != "" { return py3URL }
    if py2py3URL != "" { return py2py3URL }
    return firstWheel
}

func downloadAndDiscoverTopLevelModules(wheelURL string) ([]string, error) {
    if strings.TrimSpace(wheelURL) == "" { return nil, errors.New("empty wheel URL") }
    resp, err := httpClient.Get(wheelURL)
    if err != nil { return nil, err }
    defer resp.Body.Close()
    if resp.StatusCode != 200 { return nil, fmt.Errorf("download failed: HTTP %d", resp.StatusCode) }
    data, err := io.ReadAll(resp.Body)
    if err != nil { return nil, err }
    zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
    if err != nil { return nil, err }
    // Try dist-info/top_level.txt first
    var topLevels []string
    for _, f := range zr.File {
        if !strings.HasSuffix(strings.ToLower(f.Name), "dist-info/top_level.txt") { continue }
        rc, err := f.Open(); if err != nil { continue }
        b, _ := io.ReadAll(rc); rc.Close()
        for _, ln := range strings.Split(string(b), "\n") {
            ln = strings.TrimSpace(ln)
            if ln == "" { continue }
            topLevels = append(topLevels, ln)
        }
        break
    }
    if len(topLevels) == 0 {
        // Fallback: infer from first path segment of files
        seen := map[string]struct{}{}
        for _, f := range zr.File {
            name := f.Name
            if strings.Contains(name, "/") {
                name = strings.SplitN(name, "/", 2)[0]
            }
            name = strings.TrimSpace(name)
            if name == "" { continue }
            if strings.HasSuffix(strings.ToLower(name), ".dist-info") { continue }
            // ignore metadata folders/files
            if strings.HasPrefix(strings.ToLower(name), "tests") { continue }
            if strings.HasPrefix(strings.ToLower(name), "test") { continue }
            // derive module name by stripping .py
            name = strings.TrimSuffix(name, ".py")
            // must be a valid identifier
            if matched, _ := regexp.MatchString(`^[A-Za-z_][A-Za-z0-9_]*$`, name); !matched { continue }
            seen[name] = struct{}{}
        }
        for k := range seen { topLevels = append(topLevels, k) }
        sort.Strings(topLevels)
    }
    // Dedup and sanitize
    out := []string{}
    seen := map[string]struct{}{}
    for _, m := range topLevels {
        m = strings.TrimSpace(m)
        if m == "" { continue }
        if _, ok := seen[m]; ok { continue }
        if matched, _ := regexp.MatchString(`^[A-Za-z_][A-Za-z0-9_]*$`, m); !matched { continue }
        seen[m] = struct{}{}
        out = append(out, m)
    }
    return out, nil
}

func discoverImportModules(resp *pypiResponse, versionPin string) []string {
    // Try to pick a version (pinned or best) with a wheel and inspect it
    v := selectIntrospectionVersion(resp.Releases, versionPin)
    if v == "" { return nil }
    wheelURL := findWheelURLForVersion(resp.Releases[v])
    if wheelURL == "" {
        // Search other versions for a wheel
        var keys []string
        for k := range resp.Releases { keys = append(keys, k) }
        sort.Strings(keys)
        for i := len(keys) - 1; i >= 0; i-- {
            wheelURL = findWheelURLForVersion(resp.Releases[keys[i]])
            if wheelURL != "" { break }
        }
    }
    if wheelURL == "" { return nil }
    mods, err := downloadAndDiscoverTopLevelModules(wheelURL)
    if err != nil { return nil }
    return mods
}

func sanitizeBaseName(name string) string {
    n := strings.ToLower(strings.TrimSpace(name))
    n = strings.ReplaceAll(n, "-", "_")
    n = strings.ReplaceAll(n, ".", "_")
    n = strings.ReplaceAll(n, " ", "_")
    // drop common suffixes/prefixes
    for _, t := range []string{"python_", "py_", "_python", "_py", "_api", "api", "_pkg", "pkg", "_lib", "lib"} {
        n = strings.TrimPrefix(n, t)
        n = strings.TrimSuffix(n, t)
    }
    return n
}

func lastPathSegment(url string) string {
    s := strings.TrimSpace(url)
    if s == "" { return "" }
    // crude parse: split by '/'
    parts := strings.Split(s, "/")
    for i := len(parts) - 1; i >= 0; i-- {
        seg := strings.TrimSpace(parts[i])
        if seg != "" { return seg }
    }
    return ""
}

func choosePrimaryImportModule(mods []string, pkgName, canonicalName, homepage string) string {
    if len(mods) == 0 { return moduleImportName(pkgName) }
    // candidates derived from names/homepage
    want := []string{}
    want = append(want, sanitizeBaseName(pkgName))
    if canonicalName != "" { want = append(want, sanitizeBaseName(canonicalName)) }
    if homepage != "" { want = append(want, sanitizeBaseName(lastPathSegment(homepage))) }

    // prefer exact matches to any of desired tokens
    for _, w := range want {
        if w == "" { continue }
        for _, m := range mods {
            if sanitizeBaseName(m) == w { return m }
        }
    }
    // deprioritize obvious non-primary names
    bad := map[string]struct{}{"tests":{}, "test":{}, "examples":{}, "example":{}, "docs":{}, "conductor":{}}
    // choose shortest acceptable module
    best := ""
    for _, m := range mods {
        if _, skip := bad[strings.ToLower(m)]; skip { continue }
        if best == "" || len(m) < len(best) { best = m }
    }
    if best != "" { return best }
    return mods[0]
}

func writeRecipe(packageName, versionPin string) error {
    resp, err := getPyPI(packageName)
    if err != nil { return err }

    // Choose versions
    versionLines, suffix, err := chooseArtifacts(resp.Releases, versionPin)
    if err != nil { return err }

    // Metadata
    homepage := resp.Info.HomePage
    if homepage == "" {
        if resp.Info.ProjectURL != "" { homepage = resp.Info.ProjectURL }
    }
    if homepage == "" {
        if url, ok := resp.Info.ProjectURLs["Homepage"]; ok { homepage = url }
    }
    if homepage == "" {
        homepage = fmt.Sprintf("https://pypi.org/project/%s/", packageName)
    }
    // Discover import modules from a wheel when possible and choose a primary
    importMods := discoverImportModules(resp, versionPin)
    canonicalName := strings.TrimSpace(resp.Info.Name)
    if canonicalName == "" { canonicalName = packageName }
    module := choosePrimaryImportModule(importMods, packageName, canonicalName, resp.Info.HomePage)
    className := classNameFromPackage(packageName)
    // Canonical PyPI project name for installation spec

    // Paths
    outDir := filepath.Join("packages", pyify(packageName))
    if err := os.MkdirAll(outDir, 0o755); err != nil { return err }
    outPath := filepath.Join(outDir, "package.py")

    // Render recipe
    // We purposely keep this minimal: homepage, pypi helper, versions, uv-based install, smoke test
    // Use the canonical PyPI project name for the filename portion to preserve case
    // (e.g., adjustText/adjustText-@.tar.gz), while normalizing the directory to lowercase.
    pypiPath := fmt.Sprintf("%s/%s-@%s", strings.ToLower(canonicalName), canonicalName, suffix)

    header := fmt.Sprintf(`# Copyright 2013-2023 Lawrence Livermore National Security, LLC and other
# Spack Project Developers. See the top-level COPYRIGHT file for details.
#
# SPDX-License-Identifier: (Apache-2.0 OR MIT)

from spack.package import *

class Py%s(UvPackage):
    homepage = "%s"
    pypi = "%s"
    import_modules = [%s]
    _pypi_package = "%s"

`, className, escapePyStr(homepage), pypiPath, formatImportModules(importMods, module), canonicalName)

    // Versions block
    body := strings.Join(versionLines, "") + "\n"

    // Dependencies and install/test
    tail := `
    @run_after("install")
    def install_test(self):
        with working_dir("spack-test", create=True):
            python("-c", 'import %s')
`
    tail = fmt.Sprintf(tail, module)

    content := header + body + tail
    content = strings.ReplaceAll(content, "\t", "    ")

    if err := os.WriteFile(outPath, []byte(content), 0o644); err != nil {
        return err
    }
    fmt.Printf("Wrote recipe for %s at %s (versions=%d)\n", packageName, outPath, len(versionLines))
    return nil
}

func escapePyStr(s string) string {
    s = strings.ReplaceAll(s, "\\", "\\\\")
    s = strings.ReplaceAll(s, "\"", "\\\"")
    s = strings.ReplaceAll(s, "\n", " ")
    return s
}

func formatImportModules(mods []string, fallback string) string {
    if len(mods) == 0 {
        return fmt.Sprintf("\"%s\"", fallback)
    }
    uniq := map[string]struct{}{}
    out := []string{}
    add := func(m string) {
        m = strings.TrimSpace(m)
        if m == "" { return }
        if _, ok := uniq[m]; ok { return }
        uniq[m] = struct{}{}
        out = append(out, fmt.Sprintf("\"%s\"", m))
    }
    addedFallback := false
    for _, m := range mods {
        if m == fallback { add(m); addedFallback = true; break }
    }
    if !addedFallback { add(fallback) }
    for _, m := range mods { if m != fallback { add(m) } }
    if len(out) > 4 { out = out[:4] }
    return strings.Join(out, ", ")
}

// runCLI parses args and writes recipes. Supports optional "-f" which just
// indicates that the next argument(s) are package names (compat with scripts).
func runCLI(args []string) error {
    if len(args) == 0 {
        return errors.New("no packages provided")
    }
    // Collect (name, ver) pairs
    type pair struct{ name, ver string }
    var todo []pair
    for i := 0; i < len(args); i++ {
        arg := strings.TrimSpace(args[i])
        if arg == "" {
            continue
        }
        if arg == "-f" || arg == "--from" {
            // consume next as the actual package argument, if present
            if i+1 < len(args) {
                i++
                arg = strings.TrimSpace(args[i])
            } else {
                break
            }
        }
        if arg == "" { continue }
        name := arg
        ver := ""
        if strings.Contains(arg, "==") {
            parts := strings.SplitN(arg, "==", 2)
            name = parts[0]
            if len(parts) > 1 { ver = strings.TrimSpace(parts[1]) }
        }
        todo = append(todo, pair{name: name, ver: ver})
    }
    if len(todo) == 0 {
        return errors.New("no packages parsed from args")
    }
    var retErr error
    for _, p := range todo {
        if err := writeRecipe(p.name, p.ver); err != nil {
            fmt.Fprintf(os.Stderr, "error: %v\n", err)
            retErr = err
        }
    }
    return retErr
}

func main() {
    if len(os.Args) < 2 {
        fmt.Println("Usage: py-package-uv-creator [-f] <pypi_pkg>[==<version>] [...]")
        os.Exit(1)
    }
    _ = runCLI(os.Args[1:])
}
