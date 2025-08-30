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
    "runtime"
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
    // RequiresPython provides the PEP 440 requires-python string for this file
    // (e.g., ">=3.8,<3.12"). We use it to derive per-version Python
    // constraints so Spack can concretize a compatible interpreter.
    RequiresPython string `json:"requires_python"`
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

// artifactSel represents a chosen (version, artifact) pair among a release's files.
type artifactSel struct{
    version string
    file    *pypiRelease
}

// selectChosenArtifacts applies selection rules to choose one artifact per version
// in descending version order. Preference:
// - universal wheels (py3-none-any)
// - linux wheels for x86_64/aarch64
// - sdist as last resort
// Only versions matching the preferred pin (if any) are considered.
func selectChosenArtifacts(releases map[string][]pypiRelease, preferred string) ([]artifactSel, string, error) {
    // Return: version lines, pypi helper path suffix (if we can determine a stable suffix), error
    // Collect and sort versions ascending using numeric-dotted comparator, then emit descending
    var keys []string
    for v := range releases { keys = append(keys, v) }
    if len(keys) == 0 { return nil, "", errors.New("no releases found") }
    sort.Slice(keys, func(i, j int) bool { return versionLess(keys[i], keys[j]) })

    // If preferred is set, only emit that version (if present)
    filter := func(v string) bool { return preferred == "" || preferred == "latest" || v == preferred }

    // Determine a default pypi helper suffix from any sdist we find (legacy)
    pypiSuffix := ""
    sdistSuffixRe := regexp.MustCompile(`(?i)\.(tar\.gz|tar\.bz2|tar\.xz|tgz|zip)$`)

    // First pass: determine suffix and whether sdists present and their casings
    type chosen struct{ v string; sdist *pypiRelease; wheel *pypiRelease }
    chosenList := []chosen{}
    // Determine current platform arch tag for linux wheels
    archTag := ""
    if runtime.GOOS == "linux" {
        switch runtime.GOARCH {
        case "amd64":
            archTag = "x86_64"
        case "arm64":
            archTag = "aarch64"
        case "ppc64le":
            archTag = "ppc64le"
        }
    }

    for i := len(keys) - 1; i >= 0; i-- {
        v := keys[i]
        if !filter(v) { continue }
        arts := releases[v]
        if len(arts) == 0 { continue }
        var sdist *pypiRelease
        // select a preferred universal wheel or linux wheel matching current arch
        var wheelAny, wheelLinuxMatchingArch *pypiRelease
        for _, a := range arts {
            if a.Yanked { continue }
            if strings.EqualFold(a.Packagetype, "sdist") {
                if strings.TrimSpace(a.Digests.Sha256) != "" {
                    tmp := a
                    sdist = &tmp
                    // don't break; keep any for wheel fallback if needed
                }
            } else if strings.EqualFold(a.Packagetype, "bdist_wheel") {
                lname := strings.ToLower(a.Filename)
                // Skip wheels that are clearly Python 2-only (e.g., "-py2-" without py3)
                isPy2Only := strings.Contains(lname, "-py2-") && !strings.Contains(lname, "-py2.py3-")
                // Detect wheels that advertise Python 3 compatibility via tags
                isPy3Tagged := strings.Contains(lname, "-py3-") || strings.Contains(lname, "-py2.py3-") ||
                    strings.Contains(lname, "-cp3") || strings.Contains(lname, "-pp3") || strings.Contains(lname, "-abi3-")
                if isPy2Only || !isPy3Tagged {
                    // do not consider this wheel; try others or fall back to sdist
                    continue
                }
                // universal wheels (pure python) with py3 tags are ok everywhere
                if strings.Contains(lname, "-any.whl") && wheelAny == nil { tmp := a; wheelAny = &tmp; continue }
                // prefer manylinux/musllinux/linux wheels for current arch only
                if archTag != "" && (strings.Contains(lname, "manylinux") || strings.Contains(lname, "musllinux") || strings.Contains(lname, "linux_")) &&
                   strings.Contains(lname, archTag) && wheelLinuxMatchingArch == nil {
                    tmp := a; wheelLinuxMatchingArch = &tmp; continue
                }
            }
        }
        // Record suffix for legacy callers
        if sdist != nil && pypiSuffix == "" {
            if m := sdistSuffixRe.FindStringSubmatch(strings.ToLower(sdist.Filename)); len(m) > 1 { pypiSuffix = "." + m[1] } else { pypiSuffix = ".tar.gz" }
        }
        // choose wheel preference: universal first, then linux-specific matching current arch
        wheel := wheelAny
        if wheel == nil { wheel = wheelLinuxMatchingArch }
        chosenList = append(chosenList, chosen{v: v, sdist: sdist, wheel: wheel})
        if preferred != "" && preferred != "latest" { break }
    }

    if len(chosenList) == 0 {
        return nil, "", errors.New("no usable artifacts found")
    }

    // Emit artifact selections in order
    sels := []artifactSel{}
    for _, ch := range chosenList {
        if ch.wheel != nil {
            sels = append(sels, artifactSel{version: ch.v, file: ch.wheel})
            continue
        }
        if ch.sdist != nil {
            sels = append(sels, artifactSel{version: ch.v, file: ch.sdist})
            continue
        }
    }
    if pypiSuffix == "" { pypiSuffix = ".tar.gz" }
    return sels, pypiSuffix, nil
}

func chooseArtifacts(releases map[string][]pypiRelease, preferred string) ([]string, string, error) {
    sels, suffix, err := selectChosenArtifacts(releases, preferred)
    if err != nil { return nil, suffix, err }
    var versionLines []string
    for _, s := range sels {
        if s.file == nil { continue }
        if strings.EqualFold(s.file.Packagetype, "bdist_wheel") {
            versionLines = append(versionLines, fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\", expand=False, url=\"%s\")\n", s.version, s.file.Digests.Sha256, s.file.URL))
        } else {
            versionLines = append(versionLines, fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\", url=\"%s\")\n", s.version, s.file.Digests.Sha256, s.file.URL))
        }
    }
    return versionLines, suffix, nil
}

// parseRequiresPython converts a PEP 440 requires-python string (e.g., ">=3.8,<3.12")
// into a Spack version range like "3.8:3.11". Returns empty string if parsing fails
// or if the constraint cannot be reasonably represented.
func parseRequiresPython(req string) string {
    s := strings.TrimSpace(req)
    if s == "" { return "" }
    s = strings.ReplaceAll(s, " ", "")
    parts := strings.Split(s, ",")
    lower := ""
    upper := ""
    upperInclusive := false
    for _, p := range parts {
        if p == "" { continue }
        switch {
        case strings.HasPrefix(p, ">="):
            v := strings.TrimPrefix(p, ">=")
            v = cleanPepVersion(v)
            lower = v
        case strings.HasPrefix(p, ">"):
            // Approximate >X by using X.0.1 as lower bound; if that's not parseable, skip
            v := strings.TrimPrefix(p, ">")
            v = cleanPepVersion(v)
            lower = bumpPatch(v)
        case strings.HasPrefix(p, "<="):
            v := strings.TrimPrefix(p, "<=")
            v = cleanPepVersion(v)
            upper = v
            upperInclusive = true
        case strings.HasPrefix(p, "<"):
            v := strings.TrimPrefix(p, "<")
            v = cleanPepVersion(v)
            // Convert exclusive upper bound <A.B to inclusive previous minor A.(B-1)
            upper = decMinor(v)
            upperInclusive = true
        case strings.HasPrefix(p, "=="):
            v := strings.TrimPrefix(p, "==")
            // Handle "==3.11.*" -> lower=3.11, upper=3.11 (cleanPepVersion trims wildcards)
            v = cleanPepVersion(v)
            lower = v
            upper = v
            upperInclusive = true
        case strings.HasPrefix(p, "~="):
            // ~=3.8 -> >=3.8 and <4.0; we approximate with inclusive 4.0 upper bound
            v := strings.TrimPrefix(p, "~=")
            v = cleanPepVersion(v)
            lower = v
            upper = nextMajor(v)
            upperInclusive = true
        }
    }
    if lower == "" && upper == "" { return "" }
    // Compose a Spack range
    if lower == "" { lower = "0" }
    if upper == "" {
        return fmt.Sprintf("%s:", lower)
    }
    if !upperInclusive {
        // Convert exclusive upper into inclusive previous minor if possible
        u := decMinor(upper)
        if u != "" { upper = u }
    }
    // Guard against nonsensical ranges where upper < lower
    if versionLess(upper, lower) {
        return ""
    }
    return fmt.Sprintf("%s:%s", lower, upper)
}

// cleanPepVersion removes trailing wildcard segments like ".*" from a PEP 440
// version and trims any trailing dot remnants. Examples:
//   3.*      -> 3
//   3.5.*    -> 3.5
//   3.5      -> 3.5
func cleanPepVersion(v string) string {
    s := strings.TrimSpace(v)
    for strings.HasSuffix(s, ".*") {
        s = strings.TrimSuffix(s, ".*")
    }
    s = strings.TrimSuffix(s, ".")
    return s
}

func bumpPatch(v string) string {
    // Convert X.Y to X.Y.1 for rough > bounds
    if v == "" { return "" }
    if strings.Count(v, ".") == 0 { return v + ".0.1" }
    return v + ".1"
}

func decMinor(v string) string {
    // Decrement the minor component: A.B[.C] -> A.(B-1)
    if v == "" { return "" }
    parts := strings.Split(v, ".")
    if len(parts) == 0 { return "" }
    // ensure at least major.minor
    if len(parts) == 1 { parts = append(parts, "0") }
    // parse minor
    var maj, min int
    fmt.Sscanf(parts[0], "%d", &maj)
    fmt.Sscanf(parts[1], "%d", &min)
    if min == 0 {
        if maj == 0 { return "" }
        return fmt.Sprintf("%d", maj-1)
    }
    return fmt.Sprintf("%d.%d", maj, min-1)
}

func nextMajor(v string) string {
    if v == "" { return "" }
    parts := strings.Split(v, ".")
    var maj int
    fmt.Sscanf(parts[0], "%d", &maj)
    return fmt.Sprintf("%d.0", maj+1)
}

// parseWheelPythonSpecFromFilename derives a Spack-style Python version constraint
// from a wheel filename according to PEP 425 tags.
// Examples:
//   ...-cp310-cp310-...whl   -> 3.10:3.10
//   ...-cp39-abi3-...whl     -> 3.9:
//   ...-py3-none-any.whl     -> 3:
//   ...-cp39.cp310-...whl    -> 3.9:3.10
func parseWheelPythonSpecFromFilename(filename string) string {
    name := strings.ToLower(strings.TrimSpace(filename))
    if !strings.HasSuffix(name, ".whl") { return "" }
    // Split by '-' and take the last 3 fields: python-tag, abi-tag, platform-tag
    parts := strings.Split(strings.TrimSuffix(name, ".whl"), "-")
    if len(parts) < 2 { return "" }
    isPyTag := func(s string) bool { return strings.HasPrefix(s, "cp") || strings.HasPrefix(s, "py") || strings.HasPrefix(s, "pp") }
    pyTag := ""
    abiTag := ""
    if len(parts) >= 3 && isPyTag(parts[len(parts)-3]) {
        pyTag = parts[len(parts)-3]
        abiTag = parts[len(parts)-2]
    } else if len(parts) >= 2 && isPyTag(parts[len(parts)-2]) { // tolerate missing platform tag
        pyTag = parts[len(parts)-2]
        abiTag = parts[len(parts)-1]
    } else {
        return ""
    }

    // Helper to convert cp310 -> 3.10
    toPyVer := func(tag string) string {
        if !strings.HasPrefix(tag, "cp") || len(tag) < 4 { return "" }
        digits := tag[2:]
        // Accept forms like cp3, cp37, cp310, cp311
        if len(digits) == 1 {
            return fmt.Sprintf("%s.0", digits)
        }
        if len(digits) == 2 {
            return fmt.Sprintf("%s.%s", string(digits[0]), string(digits[1]))
        }
        // len >= 3, treat first as major and the rest as minor
        return fmt.Sprintf("%s.%s", string(digits[0]), digits[1:])
    }

    // Multiple python tags are separated by '.' e.g., cp39.cp310
    if strings.HasPrefix(pyTag, "cp") {
        tags := strings.Split(pyTag, ".")
        minV := ""
        maxV := ""
        for _, t := range tags {
            v := toPyVer(t)
            if v == "" { continue }
            if minV == "" || versionLess(v, minV) { minV = v }
            if maxV == "" || versionLess(maxV, v) { maxV = v }
        }
        if minV == "" { return "" }
        // If abi is abi3, assume forward compatibility from the minimum tag
        if strings.HasPrefix(abiTag, "abi3") {
            return fmt.Sprintf("%s:", minV)
        }
        // Non-abi3 cpXY wheels generally pin to that exact interpreter; if there are
        // multiple tags, constrain to the closed range [min, max]
        if maxV == "" { maxV = minV }
        return fmt.Sprintf("%s:%s", minV, maxV)
    }

    // Universal python 3 tag
    if pyTag == "py3" || strings.HasPrefix(pyTag, "py3.") {
        return "3:"
    }
    // PyPy-only wheels are not suitable for CPython; do not infer broader CP spec
    if strings.HasPrefix(pyTag, "pp") { return "" }
    return ""
}

// intersectSpackRanges returns the intersection of two Spack-style ranges (a and b):
//   "<lo>:<hi>" where <hi> may be empty for open-ended. Returns empty string if
// intersection cannot be represented or is empty.
func intersectSpackRanges(a, b string) string {
    normalize := func(s string) (string, string, bool) {
        s = strings.TrimSpace(s)
        if s == "" { return "", "", false }
        lo := ""
        hi := ""
        if strings.Contains(s, ":") {
            parts := strings.SplitN(s, ":", 2)
            lo = strings.TrimSpace(parts[0])
            hi = strings.TrimSpace(parts[1])
        } else {
            // Treat a bare version as exact pin
            lo = s
            hi = s
        }
        return lo, hi, true
    }
    loA, hiA, okA := normalize(a)
    loB, hiB, okB := normalize(b)
    if !okA && !okB { return "" }
    if !okA { return b }
    if !okB { return a }

    // Compute max lower bound
    lo := loA
    if lo == "" { lo = loB } else if loB != "" && versionLess(lo, loB) { lo = loB }
    // Compute min upper bound (empty means open)
    hi := hiA
    if hi == "" { hi = hiB } else if hiB != "" && versionLess(hiB, hi) { hi = hiB }

    if lo != "" && hi != "" && versionLess(hi, lo) {
        // Disjoint; cannot intersect
        return ""
    }
    if hi == "" { return fmt.Sprintf("%s:", lo) }
    return fmt.Sprintf("%s:%s", lo, hi)
}

// mergePythonSpecs combines requires-python derived spec with wheel-derived spec.
// Prefer the intersection when possible; if they conflict to empty, prefer the
// wheel-derived spec since the chosen artifact dictates the interpreter.
func mergePythonSpecs(requiresSpec, wheelSpec string) string {
    requiresSpec = strings.TrimSpace(requiresSpec)
    wheelSpec = strings.TrimSpace(wheelSpec)
    if requiresSpec == "" && wheelSpec == "" { return "" }
    if requiresSpec == "" { return wheelSpec }
    if wheelSpec == "" { return requiresSpec }
    inter := intersectSpackRanges(requiresSpec, wheelSpec)
    if inter != "" { return inter }
    return wheelSpec
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
    // Filter out common non-module entries frequently present in top_level.txt
    if len(topLevels) > 0 {
        junk := map[string]struct{}{
            "dev":{}, "build":{}, "example":{}, "examples":{}, "scripts":{}, "script":{},
            "docs":{}, "doc":{}, "presentations":{}, "presentation":{}, "conda":{},
            "conda_recipe":{}, "conda-recipe":{},
        }
        cleaned := make([]string, 0, len(topLevels))
        for _, m := range topLevels {
            mm := strings.ToLower(strings.TrimSpace(m))
            if _, drop := junk[mm]; drop { continue }
            cleaned = append(cleaned, m)
        }
        topLevels = cleaned
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
    if homepage != "" { want = append(want, sanitizeBaseName(lastPathSegment(homepage))) }
    // prefer exact package name, and its root namespace (first segment)
    sanitized := sanitizeBaseName(pkgName)
    want = append(want, sanitized)
    if idx := strings.Index(sanitized, "_"); idx > 0 { want = append(want, sanitized[:idx]) }
    if canonicalName != "" { want = append(want, sanitizeBaseName(canonicalName)) }

    // prefer exact matches to any of desired tokens
    for _, w := range want {
        if w == "" { continue }
        for _, m := range mods {
            if sanitizeBaseName(m) == w { return m }
        }
    }
    // deprioritize obvious non-primary names
    bad := map[string]struct{}{
        "tests":{}, "test":{}, "examples":{}, "example":{}, "docs":{}, "doc":{},
        "conductor":{}, "dev":{}, "build":{}, "scripts":{}, "script":{},
        "presentations":{}, "presentation":{}, "conda":{}, "conda_recipe":{}, "conda-recipe":{},
    }
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
    sels, _, err := selectChosenArtifacts(resp.Releases, versionPin)
    if err != nil { return err }
    // Construct version lines from selections
    var versionLines []string
    for _, s := range sels {
        if s.file == nil { continue }
        if strings.EqualFold(s.file.Packagetype, "bdist_wheel") {
            versionLines = append(versionLines, fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\", expand=False, url=\"%s\")\n", s.version, s.file.Digests.Sha256, s.file.URL))
        } else {
            versionLines = append(versionLines, fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\", url=\"%s\")\n", s.version, s.file.Digests.Sha256, s.file.URL))
        }
    }

    // Metadata
    homepage := selectHomepage(resp.Info, packageName)
    // Discover import modules from a wheel when possible and choose a primary
    importMods := discoverImportModules(resp, versionPin)
    canonicalName := strings.TrimSpace(resp.Info.Name)
    if canonicalName == "" { canonicalName = packageName }
    module := choosePrimaryImportModule(importMods, packageName, canonicalName, homepage)
    className := classNameFromPackage(packageName)
    // Canonical PyPI project name for installation spec

    // Paths
    outDir := filepath.Join("packages", pyify(packageName))
    if err := os.MkdirAll(outDir, 0o755); err != nil { return err }
    outPath := filepath.Join(outDir, "package.py")

    // Render recipe
    // We purposely keep this minimal: homepage, explicit URLs per version, uv-based install, smoke test

    header := fmt.Sprintf(`# Copyright 2013-2023 Lawrence Livermore National Security, LLC and other
# Spack Project Developers. See the top-level COPYRIGHT file for details.
#
# SPDX-License-Identifier: (Apache-2.0 OR MIT)

from spack.package import *

class Py%s(UvPackage):
    homepage = "%s"
    import_modules = [%s]
    _pypi_package = "%s"

`, className, escapePyStr(homepage), formatImportModules(importMods, module), canonicalName)

    // Versions block
    body := strings.Join(versionLines, "") + "\n"

    // Dependencies and install/test
    // Add per-version Python constraints if available, so Spack can choose a compatible interpreter.
    depLines := ""
    for _, s := range sels {
        if s.file == nil { continue }
        reqSpec := parseRequiresPython(s.file.RequiresPython)
        wheelSpec := parseWheelPythonSpecFromFilename(s.file.Filename)
        pySpec := mergePythonSpecs(reqSpec, wheelSpec)
        if strings.TrimSpace(pySpec) == "" { continue }
        depLines += fmt.Sprintf("\n    depends_on(\"python@%s\", type=(\"build\", \"run\"), when=\"@%s\")\n", pySpec, s.version)
    }

    tail := depLines + `
    @run_after("install")
    def install_test(self):
        with working_dir("spack-test", create=True):
            # Ensure Python can see the uv-installed site-packages under this prefix
            python("-c", "import %s")
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

// selectHomepage picks the most appropriate homepage URL from the PyPI metadata.
// Priority: Info.HomePage -> Info.ProjectURL -> ProjectURLs keys (homepage/home/source/repository/code/github) -> PyPI project page.
func selectHomepage(info pypiInfo, pkg string) string {
    if strings.TrimSpace(info.HomePage) != "" {
        return info.HomePage
    }
    if strings.TrimSpace(info.ProjectURL) != "" {
        return info.ProjectURL
    }
    // Search case-insensitively through common keys
    if info.ProjectURLs != nil {
        // exact homepage-like keys first
        keys := []string{"homepage", "home"}
        for _, k := range keys {
            for key, val := range info.ProjectURLs {
                if strings.EqualFold(strings.TrimSpace(key), k) && strings.TrimSpace(val) != "" {
                    return val
                }
            }
        }
        // repository-like keys next
        repoKeys := []string{"repository", "source", "source code", "code", "github"}
        for _, k := range repoKeys {
            for key, val := range info.ProjectURLs {
                if strings.EqualFold(strings.TrimSpace(key), k) && strings.TrimSpace(val) != "" {
                    return val
                }
            }
        }
        // fallback: first non-empty URL value
        for _, val := range info.ProjectURLs {
            if strings.TrimSpace(val) != "" {
                return val
            }
        }
    }
    return fmt.Sprintf("https://pypi.org/project/%s/", pkg)
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
