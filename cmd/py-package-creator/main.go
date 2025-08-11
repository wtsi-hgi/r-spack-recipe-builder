package main

import (
    "bytes"
    "encoding/json"
    "errors"
    "fmt"
    "context"
    "net/http"
    "os"
    "os/exec"
    "path/filepath"
    "regexp"
    "sort"
    "strings"
    "time"
    "runtime"
    "sync"
)

const spackBin = "spack"

// Timeouts to avoid hanging operations
var httpClient = &http.Client{Timeout: 30 * time.Second}
const spackCmdTimeout = 60 * time.Second
const pimdCmdTimeout = 120 * time.Second
// Max number of parallel Spack existence checks during recursion
var spackExistenceConcurrency = func() int {
    n := runtime.NumCPU()
    if n < 2 { return 2 }
    if n > 16 { return 16 }
    return n
}()

// Global cache/guards for processing across this run
var existingVersions map[string][]string
var processingPackages = map[string]struct{}{}
var processedPackages = map[string]struct{}{}
// Cache to avoid repeated cycle checks between two packages
var cycleCheckCache = map[string]bool{}

// Debug logging
var debugEnabled bool
var debugLogPath string
// lastPIMDExcerpt stores a short excerpt of the last pyPIMD METADATA block
// to embed into generated recipes for easier debugging of dependency parsing.
var lastPIMDExcerpt string

func initDebug() {
    // Enable when env var set or explicit flag parsed in main
    if os.Getenv("PY_PKG_CREATOR_DEBUG") == "1" || os.Getenv("PY_PKG_CREATOR_DEBUG") == "true" {
        debugEnabled = true
    }
    // Ensure logs directory exists
    _ = os.MkdirAll("logs", 0o755)
    if debugEnabled {
        // Timestamped session log
        ts := time.Now().Format("20060102-150405")
        debugLogPath = filepath.Join("logs", fmt.Sprintf("py-package-creator_%s.log", ts))
        // Touch file
        _ = os.WriteFile(debugLogPath, []byte(""), 0o644)
    }
}

func logDebugf(format string, args ...any) {
    if !debugEnabled {
        return
    }
    msg := fmt.Sprintf(format, args...)
    // Mirror to stderr for immediate visibility
    fmt.Fprintln(os.Stderr, msg)
    if debugLogPath == "" {
        return
    }
    // Append to session log; best-effort only
    f, err := os.OpenFile(debugLogPath, os.O_APPEND|os.O_WRONLY, 0o644)
    if err != nil {
        return
    }
    defer f.Close()
    _, _ = f.WriteString(time.Now().Format(time.RFC3339) + " " + msg + "\n")
}

// findLatestSpackInstallLog returns the newest logs/spack-install_*.log if present
func findLatestSpackInstallLog() string {
    entries, err := os.ReadDir("logs")
    if err != nil {
        return ""
    }
    var latestPath string
    var latestMod time.Time
    for _, e := range entries {
        name := e.Name()
        if !strings.HasPrefix(name, "spack-install_") || !strings.HasSuffix(name, ".log") {
            continue
        }
        info, err := e.Info()
        if err != nil {
            continue
        }
        if latestPath == "" || info.ModTime().After(latestMod) {
            latestPath = filepath.Join("logs", name)
            latestMod = info.ModTime()
        }
    }
    return latestPath
}

// ---------------- Dependency Processing ----------------

type dependencyInfo struct {
    Package string
    Extras  []string
    Version string
}

type DependencyProcessor struct {
    variants    map[string]struct{}
    variantDeps map[string]map[string]struct{}
    regularDeps map[string]struct{}
}

func NewDependencyProcessor() *DependencyProcessor {
    return &DependencyProcessor{
        variants:    make(map[string]struct{}),
        variantDeps: make(map[string]map[string]struct{}),
        regularDeps: make(map[string]struct{}),
    }
}

func (dp *DependencyProcessor) Extract(depSpec string) dependencyInfo {
    raw := strings.TrimSpace(depSpec)

    // Collect extras from "; extra == 'name'" conditions and remove them
    extras := []string{}
    reExtraCondD := regexp.MustCompile(`;\s*extra\s*==\s*"([^"]+)"`)
    for {
        m := reExtraCondD.FindStringSubmatch(raw)
        if m == nil { break }
        extras = append(extras, strings.TrimSpace(m[1]))
        raw = strings.Replace(raw, m[0], "", 1)
    }
    reExtraCondS := regexp.MustCompile(`;\s*extra\s*==\s*'([^']+)'`)
    for {
        m := reExtraCondS.FindStringSubmatch(raw)
        if m == nil { break }
        extras = append(extras, strings.TrimSpace(m[1]))
        raw = strings.Replace(raw, m[0], "", 1)
    }

    // Parse name, optional [extras], optional (constraints)
    reNameExtrasConstraints := regexp.MustCompile(`^([^\s\[\(]+)\s*(?:\[([^\]]+)\])?\s*(?:\(([^\)]+)\))?\s*$`)
    if m := reNameExtrasConstraints.FindStringSubmatch(raw); m != nil {
        name := strings.TrimSpace(m[1])
        if m[2] != "" {
            parts := strings.Split(m[2], ",")
            for _, e := range parts {
                e = strings.TrimSpace(e)
                if e != "" { extras = append(extras, e) }
            }
        }
        versionConstraint := strings.TrimSpace(m[3])
        versionConstraint = strings.Trim(versionConstraint, "() ")
        return dependencyInfo{Package: name, Extras: extras, Version: versionConstraint}
    }

    // Fallback: extras style like package[extra1,extra2]>=1.0
    reExtras := regexp.MustCompile(`^([^\[]+)\[([^\]]+)\](.*)$`)
    if m := reExtras.FindStringSubmatch(raw); m != nil {
        packageName := strings.TrimSpace(m[1])
        ex := strings.Split(m[2], ",")
        for i := range ex {
            ex[i] = strings.TrimSpace(ex[i])
        }
        versionConstraint := strings.TrimSpace(m[3])
        versionConstraint = strings.Trim(versionConstraint, "() ")
        return dependencyInfo{Package: packageName, Extras: append(extras, ex...), Version: versionConstraint}
    }

    // Fallback: name followed by operators without parentheses: package>=1.0
    rePkgVer := regexp.MustCompile(`^([^<>=!~\s]+)\s*(.*)$`)
    if m := rePkgVer.FindStringSubmatch(raw); m != nil {
        vc := strings.TrimSpace(m[2])
        vc = strings.Trim(vc, "() ")
        return dependencyInfo{Package: strings.TrimSpace(m[1]), Extras: extras, Version: vc}
    }

    return dependencyInfo{Package: strings.TrimSpace(raw), Extras: extras, Version: ""}
}

func (dp *DependencyProcessor) transformVersionConstraint(versionConstraint string) string {
    versionConstraint = strings.TrimSpace(versionConstraint)
    if versionConstraint == "" {
        return ""
    }
    // accept comma separated constraints, possibly with spaces
    parts := strings.Split(versionConstraint, ",")
    var minVer, maxVer string
    for _, p := range parts {
        p = strings.TrimSpace(p)
        if p == "" { continue }
        // strip any trailing/leading parentheses just in case
        p = strings.TrimPrefix(p, "(")
        p = strings.TrimSuffix(p, ")")
        // ignore environment markers after ';'
        if idx := strings.Index(p, ";"); idx >= 0 {
            p = strings.TrimSpace(p[:idx])
        }
        switch {
        case strings.HasPrefix(p, ">="):
            v := strings.TrimSpace(strings.TrimPrefix(p, ">="))
            if minVer == "" { minVer = v }
        case strings.HasPrefix(p, ">"):
            v := strings.TrimSpace(strings.TrimPrefix(p, ">"))
            // approximate >v as >=v
            if minVer == "" { minVer = v }
        case strings.HasPrefix(p, "<="):
            v := strings.TrimSpace(strings.TrimPrefix(p, "<="))
            if maxVer == "" { maxVer = v }
        case strings.HasPrefix(p, "<"):
            v := strings.TrimSpace(strings.TrimPrefix(p, "<"))
            if maxVer == "" { maxVer = v }
        case strings.HasPrefix(p, "=="):
            v := strings.TrimSpace(strings.TrimPrefix(p, "=="))
            // avoid strict pin; treat as lower bound only, do not set max
            if minVer == "" { minVer = v }
        case strings.HasPrefix(p, "!="):
            // skip not-equal constraints to avoid fragmentation
            continue
        case strings.HasPrefix(p, "~="):
            base := strings.TrimSpace(strings.TrimPrefix(p, "~="))
            parts := strings.Split(base, ".")
            if len(parts) >= 2 {
                major := parts[0]
                minor := parts[1]
                nextMinor := minor
                if n, err := atoiSafe(minor); err == nil {
                    nextMinor = fmt.Sprintf("%d", n+1)
                }
                if minVer == "" { minVer = base }
                if maxVer == "" { maxVer = fmt.Sprintf("%s.%s", major, nextMinor) }
            } else {
                if minVer == "" { minVer = base }
            }
        default:
            // no operator case
        }
    }
    if minVer != "" && maxVer != "" {
        // Spack ranges use a single '@' followed by 'min:max'
        return fmt.Sprintf("@%s:%s", minVer, maxVer)
    }
    if minVer != "" {
        return fmt.Sprintf("@%s:", minVer)
    }
    if maxVer != "" {
        return fmt.Sprintf("@:%s", maxVer)
    }
    return ""
}

func atoiSafe(s string) (int, error) {
    var n int
    for _, ch := range s {
        if ch < '0' || ch > '9' {
            return 0, errors.New("not a number")
        }
        n = n*10 + int(ch-'0')
    }
    return n, nil
}

func (dp *DependencyProcessor) Transform(dep dependencyInfo) dependencyInfo {
    packageName := pyify(dep.Package)
    version := dp.transformVersionConstraint(dep.Version)
    return dependencyInfo{Package: packageName, Extras: dep.Extras, Version: version}
}

func (dp *DependencyProcessor) Load(transformed dependencyInfo) {
    packageName := transformed.Package
    versionConstraint := transformed.Version

    if strings.Contains(versionConstraint, ";") || strings.Contains(versionConstraint, "(") || strings.Contains(versionConstraint, "andextra") || strings.Contains(versionConstraint, "platform-") {
        return
    }

    if len(transformed.Extras) > 0 {
        for _, extra := range transformed.Extras {
            // Spack variant names must be lowercase and use underscores; sanitize extras
            extra = sanitizeVariantName(extra)
            dp.variants[extra] = struct{}{}
            if _, ok := dp.variantDeps[extra]; !ok {
                dp.variantDeps[extra] = make(map[string]struct{})
            }
            depString := packageName
            if versionConstraint != "" {
                // never pin exact version; ensure lower-bound form has trailing colon
                if strings.HasPrefix(versionConstraint, "@") && !strings.Contains(versionConstraint, ":") && !strings.Contains(versionConstraint, ",") {
                    versionConstraint = versionConstraint + ":"
                }
                depString += versionConstraint
            }
            dp.variantDeps[extra][depString] = struct{}{}
        }
        return
    }

    depString := packageName
    if versionConstraint != "" {
        if strings.HasPrefix(versionConstraint, "@") && !strings.Contains(versionConstraint, ":") && !strings.Contains(versionConstraint, ",") {
            versionConstraint = versionConstraint + ":"
        }
        depString += versionConstraint
    }

    // Skip if present in any variant
    for _, set := range dp.variantDeps {
        if _, ok := set[depString]; ok {
            return
        }
    }

    // crude duplicate handling: if any existing dep for same package with a version range exists, skip
    // We treat a dep as for same package if it starts with packageName+"@"
    for existing := range dp.regularDeps {
        if strings.HasPrefix(existing, packageName+"@") {
            // already have a version constraint for this package
            return
        }
    }
    dp.regularDeps[depString] = struct{}{}
}

func (dp *DependencyProcessor) ProcessDependency(depSpec string) {
    extracted := dp.Extract(depSpec)
    transformed := dp.Transform(extracted)
    dp.Load(transformed)
}

func (dp *DependencyProcessor) GetSpackDependencies() []string {
    var out []string

    // regular deps sorted
    var regs []string
    for dep := range dp.regularDeps {
        regs = append(regs, dep)
    }
    sort.Strings(regs)
    for _, dep := range regs {
        out = append(out, fmt.Sprintf("\tdepends_on(\"%s\", type=(\"build\", \"run\"))\n", normalizeConstraint(dep)))
    }

    // variant deps sorted by variant, then dep
    var variants []string
    for v := range dp.variantDeps {
        variants = append(variants, v)
    }
    sort.Strings(variants)
    for _, v := range variants {
        var deps []string
        for d := range dp.variantDeps[v] {
            deps = append(deps, d)
        }
        sort.Strings(deps)
        for _, d := range deps {
            out = append(out, fmt.Sprintf("\tdepends_on(\"%s\", when=\"+%s\", type=(\"build\", \"run\"))\n", normalizeConstraint(d), v))
        }
    }
    return out
}

func (dp *DependencyProcessor) GetVariants() []string {
    var out []string
    for v := range dp.variants {
        out = append(out, v)
    }
    sort.Strings(out)
    return out
}

// ---------------- Spack discovery ----------------

type spackListEntry struct {
    Name     string   `json:"name"`
    Versions []string `json:"versions"`
}

// Fast existence check: rely on plaintext 'spack list <pkg>' output to contain the name
func spackPackageExists(pyName string) bool {
    ctx, cancel := context.WithTimeout(context.Background(), spackCmdTimeout)
    defer cancel()
    cmd := exec.CommandContext(ctx, spackBin, "list", pyName)
    var stdout bytes.Buffer
    cmd.Stdout = &stdout
    // ignore stderr content
    if err := cmd.Run(); err != nil {
        return false
    }
    out := strings.ToLower(stdout.String())
    return strings.Contains(out, strings.ToLower(pyName))
}

// ---------------- PyPI & Libraries.io ----------------

type pypiInfo struct {
    RequiresPython string `json:"requires_python"`
    Summary        string `json:"summary"`
    HomePage       string `json:"home_page"`
    ProjectURL     string `json:"project_url"`
    ProjectURLs    map[string]string `json:"project_urls"`
}

type pypiRelease struct {
    Yanked        bool   `json:"yanked"`
    Packagetype   string `json:"packagetype"`
    Filename      string `json:"filename"`
    PythonVersion string `json:"python_version"`
    URL           string `json:"url"`
    Digests       struct {
        Sha256 string `json:"sha256"`
    } `json:"digests"`
}

type pypiResponse struct {
    Info     pypiInfo                     `json:"info"`
    Releases map[string][]pypiRelease     `json:"releases"`
}

func getPyPI(packageName string) (*pypiResponse, error) {
    url := fmt.Sprintf("https://pypi.org/pypi/%s/json", packageName)
    resp, err := httpClient.Get(url)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return nil, fmt.Errorf("failed to retrieve package %s", packageName)
    }
    logDebugf("PyPI GET %s -> %d", url, resp.StatusCode)
    var out pypiResponse
    if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
        return nil, err
    }
    return &out, nil
}

type librariesDep struct {
    Platform      string `json:"platform"`
    Optional      bool   `json:"optional"`
    ProjectName   string `json:"project_name"`
    LatestStable  string `json:"latest_stable"`
}

type librariesResponse struct {
    Description string         `json:"description"`
    Homepage    string         `json:"homepage"`
    Dependencies []librariesDep `json:"dependencies"`
}

func getLibrariesIO(packageName, packageVersion string) (*librariesResponse, error) {
    url := fmt.Sprintf("https://libraries.io/api/pypi/%s/%s/dependencies?api_key=%s", packageName, packageVersion, "ebb39aed4c41baa4c4e8a384a8775cd9")
    resp, err := httpClient.Get(url)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return nil, fmt.Errorf("\t❌ Failed to retrieve package %s (HTTP %d)", packageName, resp.StatusCode)
    }
    logDebugf("Libraries.io GET %s -> %d", url, resp.StatusCode)
    var out librariesResponse
    if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
        return nil, err
    }
    return &out, nil
}

// ---------------- Helpers ----------------

func pyify(packageName string) string {
    if packageName == "python" || strings.HasPrefix(packageName, "python@") {
        return packageName
    }
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

// sanitizePackageName removes extras and version operators, returning bare PyPI name
func sanitizePackageName(raw string) string {
    name := raw
    // Trim at whitespace or brackets which often introduce extras/markers
    if idx := strings.IndexAny(name, " [("); idx >= 0 {
        name = name[:idx]
    }
    // Trim at first version/operator character
    if idx := strings.IndexAny(name, "<>=!~@"); idx >= 0 {
        name = name[:idx]
    }
    name = strings.TrimSpace(strings.ToLower(name))
    name = strings.TrimRight(name, ",;")
    return name
}

// causesDirectCycle returns true if 'dep' has a non-optional dependency back to 'root' (PyPI base names)
func causesDirectCycle(rootBase, depBase string) bool {
    if rootBase == "" || depBase == "" { return false }
    key := rootBase + "<-" + depBase
    if v, ok := cycleCheckCache[key]; ok { return v }
    // Query a limited libraries.io record for the dep's latest stable only
    resp, err := getLibrariesIO(depBase, "latest")
    if err != nil || resp == nil {
        cycleCheckCache[key] = false
        return false
    }
    for _, d := range resp.Dependencies {
        if strings.ToLower(d.Platform) == "pypi" && !d.Optional {
            if sanitizePackageName(strings.ToLower(d.ProjectName)) == rootBase {
                cycleCheckCache[key] = true
                return true
            }
        }
    }
    cycleCheckCache[key] = false
    return false
}

func getClassname(packageName string) string {
    s := strings.NewReplacer("-", ".", "_", ".").Replace(packageName)
    parts := strings.Split(s, ".")
    for i := range parts {
        if parts[i] == "" { continue }
        parts[i] = strings.ToUpper(parts[i][:1]) + strings.ToLower(parts[i][1:])
    }
    return strings.Join(parts, "")
}

// normalizeConstraint ensures that a single-bound like @1.2 becomes @1.2:
func normalizeConstraint(dep string) string {
    // If already spack-style with '@', ensure single-bound has trailing ':'
    if strings.Contains(dep, "@") {
        parts := strings.SplitN(dep, "@", 2)
        pkg := parts[0]
        ver := parts[1]
        if ver == "" {
            return dep
        }
        if strings.Contains(ver, ":") || strings.Contains(ver, ",") {
            return dep
        }
        return pkg + "@" + ver + ":"
    }
    // Convert pep operators to spack '@' ranges if present
    re := regexp.MustCompile(`^([^<>=!~\s]+)\s*([<>=!~].+)$`)
    if m := re.FindStringSubmatch(dep); m != nil {
        pkg := m[1]
        vc := strings.TrimSpace(m[2])
        // fix any hyphenated numeric versions like 0-24-2 back to dotted
        vc = strings.ReplaceAll(vc, "-", ".")
        // reuse version constraint transformer
        sp := NewDependencyProcessor().transformVersionConstraint(vc)
        if sp != "" {
            return pkgify(pkg) + sp
        }
    }
    return dep
}

// sanitizeVariantName converts an extras name into a Spack-safe variant name:
// - lowercases letters
// - replaces '-', '.', and spaces with '_'
// - drops any other non-alphanumeric/underscore characters
// - falls back to "extra" if the result is empty
func sanitizeVariantName(name string) string {
    lower := strings.ToLower(strings.TrimSpace(name))
    // Fast path: if already safe, return
    safe := true
    for _, r := range lower {
        if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
            continue
        }
        safe = false
        break
    }
    if safe && lower != "" {
        return lower
    }
    var b []rune
    for _, r := range lower {
        switch {
        case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_':
            b = append(b, r)
        case r == '-' || r == '.' || r == ' ':
            b = append(b, '_')
        default:
            // drop
        }
    }
    if len(b) == 0 {
        return "extra"
    }
    // Preserve existing underscores as-is to avoid changing semantics
    return string(b)
}

// fixPepVersionSyntax converts accidental hyphenated numeric versions in strings (e.g., '0-24-2') to '0.24.2'
func fixPepVersionSyntax(s string) string {
    // replace sequences of digit-hyphen-digit with digits separated by dots
    re := regexp.MustCompile(`(\d)-(\d)`) // simple pass; called after spack '@' mapping
    for re.MatchString(s) {
        s = re.ReplaceAllString(s, `$1.$2`)
    }
    return s
}
// pkgify ensures name is spack py- prefixed and normalized, without touching version segment
func pkgify(name string) string {
    if strings.HasPrefix(name, "py-") {
        return name
    }
    return pyify(name)
}

// getVersions mirrors Python getVersions
type VersionEntry struct {
    Version string
    URL     string
}

// choose a single version: prefer latest stable x.y.z; fallback to overall latest available
func getSingleVersion(versionList map[string][]pypiRelease, preferred string) ([]string, string, *VersionEntry, error) {
    // Helper to decide the best artifact for a given release
    // Prefer sdists for Spack builds, but also try to return a wheel URL for dependency
    // metadata extraction (pyPIMD) when available.
    chooseForRelease := func(ver string, releases []pypiRelease) (string, string, *VersionEntry) {
        wheelAnyRegex := regexp.MustCompile(`any\.whl$`)
        manylinuxRegex := regexp.MustCompile(`manylinux[^x]*_x86_64\.whl`)

        pythonVersionWheels := make(map[string][]pypiRelease)
        var sdistInfo *pypiRelease
        for _, rel := range releases {
            if rel.Yanked { continue }
            if rel.Packagetype == "bdist_wheel" && (wheelAnyRegex.MatchString(rel.Filename) || manylinuxRegex.MatchString(rel.Filename)) {
                pyVer := rel.PythonVersion
                pythonVersionWheels[pyVer] = append(pythonVersionWheels[pyVer], rel)
            } else if rel.Packagetype == "sdist" {
                tmp := rel
                sdistInfo = &tmp
            }
        }

        // Prefer sdist for building with Spack's PythonPackage
        if sdistInfo != nil {
            // Try to also capture a suitable wheel URL solely for dependency metadata
            // extraction via pyPIMD
            var wheelForMeta *pypiRelease
            if wheels, ok := pythonVersionWheels["any"]; ok && len(wheels) > 0 {
                wheelForMeta = &wheels[0]
            } else if wheels, ok := pythonVersionWheels["py3"]; ok && len(wheels) > 0 {
                wheelForMeta = &wheels[0]
            } else if wheels, ok := pythonVersionWheels["py2.py3"]; ok && len(wheels) > 0 {
                wheelForMeta = &wheels[0]
            } else {
                // fallback to any specific python tag
                for _, wheels := range pythonVersionWheels {
                    if len(wheels) == 0 { continue }
                    w := wheels[0]
                    wheelForMeta = &w
                    break
                }
            }
            line := fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\")\n", ver, sdistInfo.Digests.Sha256)
            if wheelForMeta != nil {
                return line, sdistInfo.Filename, &VersionEntry{Version: ver, URL: wheelForMeta.URL}
            }
            return line, sdistInfo.Filename, &VersionEntry{Version: ver, URL: ""}
        }

        // No sdist available; fallback to wheel (may not always be buildable in Spack)
        if len(pythonVersionWheels) > 0 {
            if wheels, ok := pythonVersionWheels["any"]; ok && len(wheels) > 0 {
                w := wheels[0]
                line := fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\", expand=False, url=\"%s\")\n", ver, w.Digests.Sha256, w.URL)
                return line, w.Filename, &VersionEntry{Version: ver, URL: w.URL}
            }
            if wheels, ok := pythonVersionWheels["py3"]; ok && len(wheels) > 0 {
                w := wheels[0]
                line := fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\", expand=False, url=\"%s\")\n", ver, w.Digests.Sha256, w.URL)
                return line, w.Filename, &VersionEntry{Version: ver, URL: w.URL}
            }
            if wheels, ok := pythonVersionWheels["py2.py3"]; ok && len(wheels) > 0 {
                w := wheels[0]
                line := fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\", expand=False, url=\"%s\")\n", ver, w.Digests.Sha256, w.URL)
                return line, w.Filename, &VersionEntry{Version: ver, URL: w.URL}
            }
            // Fallback to a specific python tag
            for pyVer, wheels := range pythonVersionWheels {
                if len(wheels) == 0 { continue }
                w := wheels[0]
                pyVerClean := strings.NewReplacer("cp", "", "py", "", "pp", "").Replace(pyVer)
                spackVer := ver + "-py" + pyVerClean
                line := fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\", expand=False, url=\"%s\")\n", spackVer, w.Digests.Sha256, w.URL)
                return line, w.Filename, &VersionEntry{Version: spackVer, URL: w.URL}
            }
        }
        return "", "", nil
    }

    // Candidate selection: iterate releases, choose first matching preferred; else best stable x.y.z; else best overall
    isRc := func(v string) bool { return strings.Contains(strings.ToLower(v), "rc") }
    isStableTriple := func(v string) bool { matched, _ := regexp.MatchString(`^\d+\.\d+\.\d+$`, v); return matched }

    // get all keys
    var keys []string
    for k := range versionList { keys = append(keys, k) }
    // Custom comparator for numeric-dotted versions
    parseSeg := func(seg string) (int, string) {
        // take leading digits as integer, return rest as suffix
        n := 0
        i := 0
        for i < len(seg) && seg[i] >= '0' && seg[i] <= '9' {
            n = n*10 + int(seg[i]-'0')
            i++
        }
        return n, seg[i:]
    }
    versionLess := func(a, b string) bool {
        // return true if a < b
        as := strings.Split(a, ".")
        bs := strings.Split(b, ".")
        maxLen := len(as)
        if len(bs) > maxLen { maxLen = len(bs) }
        for i := 0; i < maxLen; i++ {
            av := 0; ar := ""
            bv := 0; br := ""
            if i < len(as) { av, ar = parseSeg(as[i]) }
            if i < len(bs) { bv, br = parseSeg(bs[i]) }
            if av != bv { return av < bv }
            if ar != br { return ar < br }
        }
        return len(as) < len(bs)
    }
    sort.Slice(keys, func(i, j int) bool { return versionLess(keys[i], keys[j]) })

    // If preferred specified, try exact match first
    if preferred != "" && preferred != "latest" {
        for i := len(keys) - 1; i >= 0; i-- {
            if keys[i] != preferred { continue }
            if isRc(keys[i]) { continue }
            if line, filename, entry := chooseForRelease(keys[i], versionList[keys[i]]); entry != nil {
                return []string{line}, filename, entry, nil
            }
        }
    }
    // Choose best stable x.y.z
    // Find best stable x.y.z using numeric comparison
    bestStable := ""
    for _, v := range keys {
        if isRc(v) { continue }
        if !isStableTriple(v) { continue }
        if bestStable == "" || versionLess(bestStable, v) {
            bestStable = v
        }
    }
    if bestStable != "" {
        if line, filename, entry := chooseForRelease(bestStable, versionList[bestStable]); entry != nil {
            return []string{line}, filename, entry, nil
        }
    }
    // Fallback: any highest non-rc with artifacts
    bestAny := ""
    for _, v := range keys {
        if isRc(v) { continue }
        if bestAny == "" || versionLess(bestAny, v) {
            bestAny = v
        }
    }
    if bestAny != "" {
        if line, filename, entry := chooseForRelease(bestAny, versionList[bestAny]); entry != nil {
            return []string{line}, filename, entry, nil
        }
    }
    return nil, "", nil, fmt.Errorf("no suitable releases found (preferred=%q keys=%d)", preferred, len(keys))
}

func getVersions(versionList map[string][]pypiRelease) ([]string, string, []VersionEntry, error) {
    var versions []string
    filename := ""
    var entries []VersionEntry

    // sort keys descending so newest versions come first in the recipe
    var keys []string
    for k := range versionList {
        keys = append(keys, k)
    }
    sort.Strings(keys)

    wheelAnyRegex := regexp.MustCompile(`any\.whl$`)
    manylinuxRegex := regexp.MustCompile(`manylinux[^x]*_x86_64\.whl`)

    for i := len(keys) - 1; i >= 0; i-- {
        ver := keys[i]
        releases := versionList[ver]
        if len(releases) == 0 {
            continue
        }

        // skip release candidates
        if strings.Contains(strings.ToLower(ver), "rc") {
            continue
        }

        pythonVersionWheels := make(map[string][]pypiRelease)
        var sdistInfo *pypiRelease

        for _, rel := range releases {
            if rel.Yanked {
                continue
            }
            if rel.Packagetype == "bdist_wheel" && (wheelAnyRegex.MatchString(rel.Filename) || manylinuxRegex.MatchString(rel.Filename)) {
                pyVer := rel.PythonVersion
                pythonVersionWheels[pyVer] = append(pythonVersionWheels[pyVer], rel)
            } else if rel.Packagetype == "sdist" {
                tmp := rel
                sdistInfo = &tmp
            }
        }

        if len(pythonVersionWheels) > 0 {
            // process wheels by python version
            var pyVers []string
            for py := range pythonVersionWheels {
                pyVers = append(pyVers, py)
            }
            sort.Strings(pyVers)
            for _, pyVer := range pyVers {
                wheels := pythonVersionWheels[pyVer]
                wheel := wheels[0]
                if pyVer == "any" || pyVer == "py3" || pyVer == "py2.py3" {
                    versions = append(versions, fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\", expand=False, url=\"%s\")\n", ver, wheel.Digests.Sha256, wheel.URL))
                    filename = wheel.Filename
                    entries = append(entries, VersionEntry{Version: ver, URL: wheel.URL})
                } else {
                    pyVerClean := strings.NewReplacer("cp", "", "py", "", "pp", "").Replace(pyVer)
                    versionSuffix := "-py" + pyVerClean
                    spackVer := ver + versionSuffix
                    versions = append(versions, fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\", expand=False, url=\"%s\")\n", spackVer, wheel.Digests.Sha256, wheel.URL))
                    filename = wheel.Filename
                    entries = append(entries, VersionEntry{Version: spackVer, URL: wheel.URL})
                }
            }
        } else if sdistInfo != nil {
            versions = append(versions, fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\")\n", ver, sdistInfo.Digests.Sha256))
            filename = sdistInfo.Filename
            entries = append(entries, VersionEntry{Version: ver, URL: ""})
        }
    }

    return versions, filename, entries, nil
}

func addWheelDependencies(dp *DependencyProcessor, wheelURL string) error {
    ctx, cancel := context.WithTimeout(context.Background(), pimdCmdTimeout)
    defer cancel()
    cmd := exec.CommandContext(ctx, "pyPIMD/pypi", wheelURL)
    out, err := cmd.CombinedOutput()
    if err != nil {
        // Include a sample of output for debugging bad metadata or network issues
        sample := string(out)
        if len(sample) > 800 {
            sample = sample[:800]
        }
        return fmt.Errorf("pyPIMD failed for %s: %v; output: %s", wheelURL, err, sample)
    }
    // Keep a small excerpt of the METADATA block for embedding
    {
        text := string(out)
        if len(text) > 4096 { text = text[:4096] }
        // Only store the header section (before the first blank line)
        if segIdx := strings.Index(text, "\n\n"); segIdx >= 0 {
            text = text[:segIdx]
        }
        // Trim to the last 25 lines to keep it compact
        lines := strings.Split(text, "\n")
        if len(lines) > 25 {
            lines = lines[len(lines)-25:]
        }
        // Only keep Requires-Dist and Name/Version style fields
        filtered := []string{}
        for _, line := range lines {
            if strings.HasPrefix(line, "Requires-Dist:") || strings.HasPrefix(line, "Name:") || strings.HasPrefix(line, "Version:") {
                filtered = append(filtered, line)
            }
        }
        lastPIMDExcerpt = strings.Join(filtered, "\n")
    }
    segs := strings.SplitN(string(out), "\n\n", 2)
    lines := strings.Split(segs[0], "\n")
    for _, line := range lines {
        if strings.HasPrefix(line, "Requires-Dist:") {
            dep := strings.TrimSpace(strings.TrimPrefix(line, "Requires-Dist:"))
            // Normalize inline constraints of form "pkg (>=1.2)" → "pkg(>=1.2)"
            dep = strings.ReplaceAll(dep, " )", ")")
            dep = strings.ReplaceAll(dep, "( ", "(")
            // Drop environment markers we don't parse well (e.g., ; python_version < "3.8")
            if idx := strings.Index(dep, ";"); idx >= 0 {
                // retain extras marker only
                marker := dep[idx:]
                if strings.Contains(marker, "extra ==") {
                    dep = dep[:idx] + marker
                } else {
                    dep = dep[:idx]
                }
            }
            dp.ProcessDependency(dep)
        }
    }
    return nil
}

// ---------------- Recipe rendering ----------------

func writeRecipe(header, footer string, versions []string, depends []string, packageName string, variants []string, debugComment string) error {
    dir := filepath.Join("packages", pyify(packageName))
    if err := os.MkdirAll(dir, 0o755); err != nil {
        return err
    }

    variantsSection := ""
    if len(variants) > 0 {
        variantsSection = "\n    # Variants for extras\n"
        for _, v := range variants {
            variantsSection += fmt.Sprintf("    variant(\"%s\", default=False, description=\"Enable %s extra\")\n", v, v)
        }
    }

    // Compose a concise banner as a comment block. Avoid embedding verbose debug content
    // such as session debug paths or external Spack log tails in the generated recipe.
    debugBanner := fmt.Sprintf("# Generated by py-package-creator on %s\n", time.Now().Format(time.RFC3339))
    // Provide a simple hint for reproducing the install locally.
    debugBanner += fmt.Sprintf("# To reproduce install locally: spack install %s\n", pyify(packageName))
    if strings.TrimSpace(debugComment) != "" {
        // Ensure the debug comment itself is prefixed as Python comments
        dbgLines := []string{}
        for _, line := range strings.Split(debugComment, "\n") {
            if strings.TrimSpace(line) == "" { continue }
            dbgLines = append(dbgLines, "# "+line)
        }
        if len(dbgLines) > 0 {
            debugBanner += strings.Join(dbgLines, "\n") + "\n\n"
        } else {
            debugBanner += "\n"
        }
    } else {
        debugBanner += "\n"
    }
    // Indent the debug banner to live inside the class body for better readability/structure
    indentBlock := func(s, indent string) string {
        if s == "" { return s }
        lines := strings.Split(s, "\n")
        for i, ln := range lines {
            if strings.TrimSpace(ln) == "" { continue }
            lines[i] = indent + ln
        }
        return strings.Join(lines, "\n")
    }
    debugBannerIndented := indentBlock(debugBanner, "    ")

    // Add a simple smoke test after install
    moduleName := moduleImportName(packageName)
    smokeCmd := fmt.Sprintf("import %s", moduleName)
    if moduleName == "numpy" {
        smokeCmd = "import numpy; numpy.test(\"full\", verbose=2)"
    }
    testSection := fmt.Sprintf(`

    @run_after("install")
    def install_test(self):
        with working_dir("spack-test", create=True):
            python("-c", '%s')
`, smokeCmd)

    content := header + variantsSection + "\n\n" + debugBannerIndented + strings.Join(versions, "") + "\n" + strings.Join(depends, "") + testSection + footer
    content = strings.ReplaceAll(content, "\t", "    ")

    outPath := filepath.Join(dir, "package.py")
    if err := os.WriteFile(outPath, []byte(content), 0o644); err != nil {
        return err
    }
    logDebugf("Wrote recipe for %s at %s (versions=%d, deps=%d, variants=%d)", packageName, outPath, len(versions), len(depends), len(variants))
    return nil
}

func extractPkgName(dep string) string {
    if idx := strings.Index(dep, "@"); idx >= 0 {
        return dep[:idx]
    }
    return dep
}

func chooseBetterDep(existing, candidate string) string {
    // Prefer entry with version constraint '@', tie-break by longer string
    hasVerExisting := strings.Contains(existing, "@")
    hasVerCandidate := strings.Contains(candidate, "@")
    if hasVerExisting && !hasVerCandidate {
        return existing
    }
    if hasVerCandidate && !hasVerExisting {
        return candidate
    }
    if len(candidate) > len(existing) {
        return candidate
    }
    return existing
}

func getDepends(dp *DependencyProcessor, perVersion map[string]map[string]struct{}, perVersionVariants map[string]map[string]map[string]struct{}, pyDeps map[string]string, rootSpack string) []string {
    dependsOn := []string{}

    // Always include setuptools build dep
    // Spack expects a tuple for type; single element tuples need a trailing comma
    dependsOn = append(dependsOn, "\tdepends_on(\"py-setuptools\", type=(\"build\",))\n")

    // Build per-version maps and sets first (preferred over global to avoid duplicates)
    perVersionBestByVersion := map[string]map[string]string{}
    perVersionBestAll := map[string]string{}
    if perVersion != nil {
        for v, set := range perVersion {
            if _, ok := perVersionBestByVersion[v]; !ok {
                perVersionBestByVersion[v] = map[string]string{}
            }
            for d := range set {
                pkg := extractPkgName(d)
                // best per version
                if best, ok := perVersionBestByVersion[v][pkg]; ok {
                    perVersionBestByVersion[v][pkg] = chooseBetterDep(best, d)
                } else {
                    perVersionBestByVersion[v][pkg] = d
                }
                // track best overall across versions
                if bestAll, ok := perVersionBestAll[pkg]; ok {
                    perVersionBestAll[pkg] = chooseBetterDep(bestAll, d)
                } else {
                    perVersionBestAll[pkg] = d
                }
            }
        }
    }
    perVersionVariantBest := map[string]map[string]string{}
    if perVersionVariants != nil {
        for ver, extras := range perVersionVariants {
            if _, ok := perVersionVariantBest[ver]; !ok { perVersionVariantBest[ver] = map[string]string{} }
            for _, set := range extras {
                for d := range set {
                    pkg := extractPkgName(d)
                    if best, ok := perVersionVariantBest[ver][pkg]; ok {
                        perVersionVariantBest[ver][pkg] = chooseBetterDep(best, d)
                    } else {
                        perVersionVariantBest[ver][pkg] = d
                    }
                }
            }
        }
    }

    // Now emit global deps excluding any packages that are specified per-version, and deduplicate
    if dp != nil {
        bestGlobal := map[string]string{}
        for dep := range dp.regularDeps {
            if strings.HasPrefix(dep, "python@") { continue }
            pkg := extractPkgName(dep)
            if _, excluded := perVersionBestAll[pkg]; excluded { continue }
            // exclude if any perVersionVariant has this pkg
            excludedVariant := false
            for _, m := range perVersionVariantBest { if _, ok := m[pkg]; ok { excludedVariant = true; break } }
            if excludedVariant { continue }
            if best, ok := bestGlobal[pkg]; ok {
                bestGlobal[pkg] = chooseBetterDep(best, dep)
            } else {
                bestGlobal[pkg] = dep
            }
        }
        // Emit global best
        var pkgs []string
        for p := range bestGlobal { pkgs = append(pkgs, p) }
        sort.Strings(pkgs)
        for _, p := range pkgs {
            dependsOn = append(dependsOn, fmt.Sprintf("\tdepends_on(\"%s\", type=(\"build\", \"run\"))\n", fixPepVersionSyntax(normalizeConstraint(bestGlobal[p]))))
        }
    }

    // Per-version non-python deps
    if perVersion != nil {
        // stable order by version
        var vers []string
        for v := range perVersionBestByVersion { vers = append(vers, v) }
        sort.Strings(vers)
        for _, v := range vers {
            var pkgs []string
            for pkg := range perVersionBestByVersion[v] { pkgs = append(pkgs, pkg) }
            sort.Strings(pkgs)
            for _, pkg := range pkgs {
                d := perVersionBestByVersion[v][pkg]
                dependsOn = append(dependsOn, fmt.Sprintf("\tdepends_on(\"%s\", when=\"@%s\", type=(\"build\", \"run\"))\n", fixPepVersionSyntax(normalizeConstraint(d)), v))
            }
        }
    }

    // Per-version variant deps: when="@<version> +<extra>"
    if perVersionVariants != nil {
        var vers []string
        for v := range perVersionVariants { vers = append(vers, v) }
        sort.Strings(vers)
        for _, v := range vers {
            var extras []string
            for e := range perVersionVariants[v] { extras = append(extras, e) }
            sort.Strings(extras)
            for _, e := range extras {
                // build best per package for this version+extra
                best := map[string]string{}
                for d := range perVersionVariants[v][e] {
                    pkg := extractPkgName(d)
                    if cur, ok := best[pkg]; ok {
                        best[pkg] = chooseBetterDep(cur, d)
                    } else {
                        best[pkg] = d
                    }
                }
                var pkgs []string
                for p := range best { pkgs = append(pkgs, p) }
                sort.Strings(pkgs)
                for _, p := range pkgs {
                    dependsOn = append(dependsOn, fmt.Sprintf("\tdepends_on(\"%s\", when=\"@%s +%s\", type=(\"build\", \"run\"))\n", fixPepVersionSyntax(normalizeConstraint(best[p])), v, e))
                }
            }
        }
    }

    if pyDeps != nil {
        // stable order
        var versions []string
        for v := range pyDeps {
            versions = append(versions, v)
        }
        sort.Strings(versions)
        for _, v := range versions {
            dependsOn = append(dependsOn, fmt.Sprintf("\tdepends_on(\"python@%s\", when=\"@%s\", type=(\"build\", \"run\"))\n", pyDeps[v], v))
        }
    }

    // If no per-version python mapping was created, try to emit a global python constraint
    // derived from Requires-Python (stored in dp.regularDeps as python@X) so that recipes
    // always depend on python explicitly.
    if (pyDeps == nil || len(pyDeps) == 0) && dp != nil {
        bestPython := ""
        for d := range dp.regularDeps {
            if strings.HasPrefix(d, "python@") {
                if bestPython == "" || len(d) > len(bestPython) {
                    bestPython = d
                }
            }
        }
        if bestPython != "" {
            dependsOn = append(dependsOn, fmt.Sprintf("\tdepends_on(\"%s\", type=(\"build\", \"run\"))\n", bestPython))
        }
    }
    // Ensure some python dep exists if none derived
    hasPython := false
    if pyDeps != nil && len(pyDeps) > 0 {
        hasPython = true
    } else if dp != nil {
        for d := range dp.regularDeps {
            if strings.HasPrefix(d, "python") { hasPython = true; break }
        }
    }
    if !hasPython {
        dependsOn = append(dependsOn, "\tdepends_on(\"python\", type=(\"build\", \"run\"))\n")
    }
    // Final safeguard: remove any dependency that would create a 2-node cycle back to rootSpack
    if strings.TrimSpace(rootSpack) != "" {
        filtered := make([]string, 0, len(dependsOn))
        reDep := regexp.MustCompile(`depends_on\("([^"@]+)`) // capture spack name before any '@'
        for _, line := range dependsOn {
            m := reDep.FindStringSubmatch(line)
            if len(m) >= 2 {
                depSpack := m[1]
                if depSpack == rootSpack {
                    // never include self-dependency
                    fmt.Printf("\t⤺ skipping %s (self) to avoid cycle\n", depSpack)
                    continue
                }
                if localRecipeDependsOn(depSpack, rootSpack) {
                    fmt.Printf("\t⤺ skipping %s to avoid direct cycle with %s\n", depSpack, rootSpack)
                    logDebugf("Filtered cycle dep %s <- %s during emit", depSpack, rootSpack)
                    continue
                }
            }
            filtered = append(filtered, line)
        }
        dependsOn = filtered
    }
    return dependsOn
}

// localRecipeDependsOn returns true if a locally generated recipe for depSpack
// (e.g., "py-foo") exists and contains a depends_on on rootSpack (e.g., "py-bar").
func localRecipeDependsOn(depSpack, rootSpack string) bool {
    path := filepath.Join("packages", depSpack, "package.py")
    data, err := os.ReadFile(path)
    if err != nil {
        return false
    }
    content := string(data)
    // Match either depends_on("py-foo") or depends_on("py-foo@...")
    // by searching for the prefix without requiring the closing quote immediately.
    needlePrefix := fmt.Sprintf("depends_on(\"%s", rootSpack)
    return strings.Contains(content, needlePrefix)
}

func getTemplate(mode, packageName, description, homepage, className, filename string, selectedVersion string) (string, string, error) {
    if mode == "+" {
        // Normalize PyPI project name path and use '@' placeholder per Spack convention
        pypiName := strings.ToLower(packageName)
        pypiName = strings.ReplaceAll(pypiName, "_", "-")
        pypiName = strings.ReplaceAll(pypiName, " ", "-")

        // Safely quote strings for Python source
        pyQuote := func(s string) string {
            // Use double-quoted literal; escape backslashes and quotes; collapse newlines
            s = strings.ReplaceAll(s, "\\", "\\\\")
            s = strings.ReplaceAll(s, "\"", "\\\"")
            s = strings.ReplaceAll(s, "\r\n", "\n")
            s = strings.ReplaceAll(s, "\r", "\n")
            s = strings.ReplaceAll(s, "\n", `\\n`)
            return s
        }

        // Decide archive suffix for the pypi helper path based on the selected artifact filename.
        // Default to .tar.gz but support common alternatives like .zip, .tar.bz2, .tar.xz, .tgz.
        detectPypiSuffix := func(name string) string {
            n := strings.ToLower(strings.TrimSpace(name))
            // Handle the most common multi-dot suffixes first
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
        suffix := detectPypiSuffix(filename)

        // If we know the concrete version and it looks like a plain version (no wheel py-tag suffix),
        // bake it directly into the pypi path for maximum compatibility with older Spack versions
        // that don't support the '@' placeholder in the pypi helper.
        // Otherwise, fall back to the '@' placeholder.
        pypiVersionToken := "@"
        if v := strings.TrimSpace(selectedVersion); v != "" {
            // treat as plain if it matches digits and dots only (e.g., 0.40.7)
            if matched, _ := regexp.MatchString(`^\d+(?:\.\d+)*$`, v); matched {
                pypiVersionToken = v
            }
        }

        // Render description as indented comments to avoid Python syntax errors from unescaped quotes
        descLines := []string{}
        for _, ln := range strings.Split(strings.TrimSpace(description), "\n") {
            ln = strings.TrimSpace(ln)
            if ln == "" { continue }
            if len(ln) > 400 { ln = ln[:400] + " …" }
            descLines = append(descLines, "    # "+ln)
        }
        descBlock := strings.Join(descLines, "\n")

         header := fmt.Sprintf(`# Copyright 2013-2023 Lawrence Livermore National Security, LLC and other
# Spack Project Developers. See the top-level COPYRIGHT file for details.
#
# SPDX-License-Identifier: (Apache-2.0 OR MIT)

from spack.package import *

        
        class Py%s(PythonPackage):
%s
            homepage = "%s"
            pypi = "%s/%s-%s%s"
            import_modules = ["%s"]
         `, className, descBlock, pyQuote(homepage), pypiName, pypiName, pypiVersionToken, suffix, moduleImportName(packageName))
        footer := ""
        return header, footer, nil
    }

    // Non-creation mode: extract header/footer from existing file
    path := filepath.Join("packages", pyify(packageName), "package.py")
    data, err := os.ReadFile(path)
    if err != nil {
        return "", "", err
    }
    lines := strings.Split(string(data), "\n")
    firstline := 0
    for i, line := range lines {
        if strings.Contains(strings.ReplaceAll(line, "    ", "\t"), "\tversion(") || strings.Contains(strings.ReplaceAll(line, "    ", "\t"), "\turl =") || strings.Contains(strings.ReplaceAll(line, "    ", "\t"), "\turls =") {
            firstline = i
            break
        }
    }
    lastline := len(lines)
    for i, line := range lines {
        if strings.Contains(strings.ReplaceAll(line, "    ", "\t"), "\tdepends_on(") {
            lastline = i
            break
        }
    }
    header := strings.TrimSpace(strings.Join(lines[:firstline], "\n"))
    footer := strings.Join(lines[lastline:], "\n")
    return header, footer, nil
}

func get(packageName, packageVersion string, recurse, force bool) error {
    logDebugf("BEGIN get name=%q version=%q recurse=%v force=%v", packageName, packageVersion, recurse, force)
    nameLower := strings.ToLower(packageName)
    // Avoid cycles: if currently processing, skip
    if _, inProg := processingPackages[nameLower]; inProg {
        fmt.Printf("\t🔁 cycle detected on %s; skipping nested generation\n", packageName)
        return nil
    }
    // If already processed in this run and not forcing, skip
    if _, done := processedPackages[nameLower]; done && !force {
        return nil
    }
    processingPackages[nameLower] = struct{}{}
    defer delete(processingPackages, nameLower)
    if shouldSkipExisting(packageName, force) {
        fmt.Printf("\t✴️ %s already exists in spack\n", pyify(packageName))
        return nil
    }

    libs, err := getLibrariesIO(packageName, packageVersion)
    // Do not abort generation if Libraries.io is unavailable; fall back to PyPI-only
    if err != nil || libs == nil {
        if err != nil {
            fmt.Println(err.Error())
            logDebugf("Libraries.io error for %s@%s: %v", packageName, packageVersion, err)
        }
        libs = &librariesResponse{}
    }

    pypiResp, err := getPyPI(packageName)
    if err != nil {
        return err
    }

    depProcessor := NewDependencyProcessor()

    // Python requires_python
    pythonVersion := strings.TrimSpace(pypiResp.Info.RequiresPython)
    if pythonVersion != "" {
        depProcessor.ProcessDependency("python" + pythonVersion)
    }

    // Accumulate dependencies to potentially recurse into, keyed by dep name with a preferred version hint
    depRecurseTargets := map[string]string{}

    // libraries.io deps (record for recursion only; do not add as unconditional build deps)
    for _, d := range libs.Dependencies {
        if strings.ToLower(d.Platform) == "pypi" && !d.Optional {
            depName := strings.ToLower(d.ProjectName)
            if recurse {
                // Prefer the libraries.io latest stable version when available
                depRecurseTargets[sanitizePackageName(depName)] = d.LatestStable
            }
        }
    }

    // Determine a single target version to include in the recipe
    versions, filename, selectedEntry, err := getSingleVersion(pypiResp.Releases, packageVersion)
    if err != nil {
        logDebugf("Version selection failed for %s@%s: %v", packageName, packageVersion, err)
        return err
    }
    if selectedEntry != nil {
        fmt.Printf("\t✔ Selected version %s (artifact: %s)\n", selectedEntry.Version, filename)
        logDebugf("Selected version=%s filename=%s wheelURL=%s", selectedEntry.Version, filename, selectedEntry.URL)
    }

    // Parse PIMD dependencies for the selected version only
    versionToPython := map[string]string{}
    perVersionDeps := map[string]map[string]struct{}{}
    perVersionVariantDeps := map[string]map[string]map[string]struct{}{}

    var vdp *DependencyProcessor
    // Preserve root package pyPIMD excerpt across recursion for accurate embedding
    var rootPIMDExcerpt string
    if selectedEntry != nil && selectedEntry.URL != "" {
        vdp = NewDependencyProcessor()
        if err := addWheelDependencies(vdp, selectedEntry.URL); err != nil {
            // Continue generation but surface the error for debugging
            fmt.Printf("\t⚠️ failed to extract wheel dependencies from %s: %v\n", selectedEntry.URL, err)
            logDebugf("Wheel dependency extraction failed for %s: %v", selectedEntry.URL, err)
        }
        // snapshot excerpt for root before recursion overwrites it
        rootPIMDExcerpt = lastPIMDExcerpt
        // merge vdp into depProcessor to keep global deps relaxed
        for d := range vdp.regularDeps {
            // Keep as-is: vdp already uses py- prefix; avoid double prefixing in output later
            // Avoid circular linking back to the root package
            if extractPkgName(d) == pyify(packageName) { continue }
            // Skip if introducing a direct 2-node cycle root<->dep
            base := sanitizePackageName(strings.TrimPrefix(extractPkgName(d), "py-"))
            if causesDirectCycle(sanitizePackageName(packageName), base) { continue }
            depProcessor.regularDeps[d] = struct{}{}
        }
        logDebugf("Wheel deps regular=%d variantGroups=%d", len(vdp.regularDeps), len(vdp.variantDeps))
    } else {
        // Fallback: no wheel URL available to parse METADATA.
        // Use libraries.io dependency list as a best-effort set of runtime deps
        // so the generated recipe includes likely requirements and triggers
        // recursive generation. Only include required PyPI deps.
        for _, d := range libs.Dependencies {
            if strings.ToLower(d.Platform) != "pypi" || d.Optional {
                continue
            }
            depName := sanitizePackageName(strings.ToLower(d.ProjectName))
            if depName == "" || depName == strings.ToLower(packageName) {
                continue
            }
            // avoid simple direct cycles
            if causesDirectCycle(sanitizePackageName(packageName), depName) {
                continue
            }
            depProcessor.ProcessDependency(depName)
        }
    }

    if vdp != nil {
        for extra, set := range vdp.variantDeps {
            for d := range set {
                depPkg := d
                ver := ""
                if idx := strings.Index(d, "@"); idx >= 0 {
                    depPkg = d[:idx]
                    ver = d[idx:]
                }
                base := sanitizePackageName(strings.TrimPrefix(depPkg, "py-"))
                // If introducing a direct 2-node cycle, skip adding as an immediate dependency
                // but still allow recursive generation to ensure local recipe exists.
                if !causesDirectCycle(sanitizePackageName(packageName), base) {
                    depProcessor.Load(dependencyInfo{Package: depPkg, Extras: []string{extra}, Version: ver})
                }
                if recurse {
                    name := base
                    if name != "python" {
                        if _, exists := depRecurseTargets[name]; !exists {
                            depRecurseTargets[name] = "latest"
                        }
                    }
                }
                if _, ok := perVersionVariantDeps[selectedEntry.Version]; !ok {
                    perVersionVariantDeps[selectedEntry.Version] = make(map[string]map[string]struct{})
                }
                if _, ok := perVersionVariantDeps[selectedEntry.Version][extra]; !ok {
                    perVersionVariantDeps[selectedEntry.Version][extra] = make(map[string]struct{})
                }
                perVersionVariantDeps[selectedEntry.Version][extra][d] = struct{}{}
            }
        }
        for d := range vdp.regularDeps {
            if strings.HasPrefix(d, "python@") {
                versionToPython[selectedEntry.Version] = strings.TrimPrefix(d, "python@")
            }
        }
        for d := range vdp.regularDeps {
            if strings.HasPrefix(d, "python@") { continue }
            if _, ok := perVersionDeps[selectedEntry.Version]; !ok {
                perVersionDeps[selectedEntry.Version] = make(map[string]struct{})
            }
            perVersionDeps[selectedEntry.Version][d] = struct{}{}
            if recurse {
                base := sanitizePackageName(strings.TrimPrefix(d, "py-"))
                if idx := strings.Index(base, "@"); idx >= 0 { base = base[:idx] }
                if base != "python" {
                    if _, exists := depRecurseTargets[base]; !exists {
                        depRecurseTargets[base] = "latest"
                    }
                }
            }
        }
    }

    // Perform recursion first so we can detect immediate two-node cycles via local recipes
    if recurse {
        var depNames []string
        for name := range depRecurseTargets { depNames = append(depNames, name) }
        sort.Strings(depNames)
        if len(depNames) > 0 {
            msg := fmt.Sprintf("\t↪ generating %d dependent recipe(s): %s", len(depNames), strings.Join(depNames, ", "))
            fmt.Println(msg)
            logDebugf(strings.TrimSpace(msg))
        }
        pkgLower := strings.ToLower(packageName)

        // First pass: decide which names need local generation immediately (no local recipe present)
        // and which names should be checked for existence in Spack.
        type depReq struct{ name, ver string }
        var needImmediate []depReq
        var toCheck []depReq
        for _, name := range depNames {
            if name == pkgLower { continue }
            ver := depRecurseTargets[name]
            if strings.TrimSpace(ver) == "" { ver = "latest" }
            localPath := filepath.Join("packages", pyify(name), "package.py")
            if _, err := os.Stat(localPath); os.IsNotExist(err) {
                needImmediate = append(needImmediate, depReq{name: name, ver: ver})
            } else {
                toCheck = append(toCheck, depReq{name: name, ver: ver})
            }
        }

        // Generate immediate ones sequentially (generation mutates shared state and writes files).
        for _, r := range needImmediate {
            if err := get(r.name, r.ver, true, false); err != nil {
                fmt.Printf("\t⚠️ failed to generate dependency %s@%s: %v\n", r.name, r.ver, err)
                logDebugf("Dependency generation failed for %s@%s: %v", r.name, r.ver, err)
            }
        }

        // Parallelize Spack existence checks for remaining ones to reduce wall time.
        exists := map[string]bool{}
        var mu sync.Mutex
        var wg sync.WaitGroup
        jobs := make(chan depReq, len(toCheck))
        worker := func() {
            defer wg.Done()
            for r := range jobs {
                present := spackPackageExists(pyify(r.name))
                mu.Lock()
                exists[r.name] = present
                mu.Unlock()
            }
        }
        // Start workers
        n := spackExistenceConcurrency
        if len(toCheck) < n { n = len(toCheck) }
        if n < 1 { n = 1 }
        wg.Add(n)
        for i := 0; i < n; i++ { go worker() }
        for _, r := range toCheck { jobs <- r }
        close(jobs)
        wg.Wait()

        // Generate any that are not present in Spack
        for _, r := range toCheck {
            if present, ok := exists[r.name]; ok && !present {
                if err := get(r.name, r.ver, true, false); err != nil {
                    fmt.Printf("\t⚠️ failed to generate dependency %s@%s: %v\n", r.name, r.ver, err)
                    logDebugf("Dependency generation failed for %s@%s: %v", r.name, r.ver, err)
                }
            }
        }
    }

    // After recursion, filter out any direct two-node cycles using local recipes
    rootSpack := pyify(packageName)
    // Filter regular deps
    for d := range depProcessor.regularDeps {
        depPkg := extractPkgName(d)
        if localRecipeDependsOn(depPkg, rootSpack) {
            delete(depProcessor.regularDeps, d)
            fmt.Printf("\t⤺ skipping %s to avoid direct cycle with %s\n", depPkg, rootSpack)
        }
    }
    // Filter variant deps
    for extra, set := range depProcessor.variantDeps {
        for d := range set {
            depPkg := extractPkgName(d)
            if localRecipeDependsOn(depPkg, rootSpack) {
                delete(depProcessor.variantDeps[extra], d)
                fmt.Printf("\t⤺ skipping %s (+%s) to avoid direct cycle with %s\n", depPkg, extra, rootSpack)
            }
        }
        if len(depProcessor.variantDeps[extra]) == 0 {
            delete(depProcessor.variantDeps, extra)
            delete(depProcessor.variants, extra)
        }
    }
    // Filter per-version maps
    for ver, set := range perVersionDeps {
        for d := range set {
            depPkg := extractPkgName(d)
            if localRecipeDependsOn(depPkg, rootSpack) {
                delete(perVersionDeps[ver], d)
            }
        }
        if len(perVersionDeps[ver]) == 0 {
            delete(perVersionDeps, ver)
        }
    }
    for ver, extras := range perVersionVariantDeps {
        for extra, set := range extras {
            for d := range set {
                depPkg := extractPkgName(d)
                if localRecipeDependsOn(depPkg, rootSpack) {
                    delete(perVersionVariantDeps[ver][extra], d)
                }
            }
            if len(perVersionVariantDeps[ver][extra]) == 0 {
                delete(perVersionVariantDeps[ver], extra)
            }
        }
        if len(perVersionVariantDeps[ver]) == 0 {
            delete(perVersionVariantDeps, ver)
        }
    }

    homepage := libs.Homepage
    if homepage == "" {
        if pypiResp.Info.HomePage != "" {
            homepage = pypiResp.Info.HomePage
        } else if pypiResp.Info.ProjectURL != "" {
            homepage = pypiResp.Info.ProjectURL
        } else if url, ok := pypiResp.Info.ProjectURLs["Homepage"]; ok {
            homepage = url
        }
    }
    desc := libs.Description
    if desc == "" {
        desc = pypiResp.Info.Summary
    }
    selectedVerValue := ""
    if selectedEntry != nil {
        // Use the underlying PEP 440 version, not Spack-suffixed variants
        // The filename we chose belongs to the base version key when sdist was used
        baseVer := selectedEntry.Version
        if idx := strings.Index(baseVer, "-py"); idx >= 0 {
            baseVer = baseVer[:idx]
        }
        selectedVerValue = baseVer
    }
    header, footer, err := getTemplate("+", packageName, desc, homepage, getClassname(packageName), filename, selectedVerValue)
    if err != nil {
        return err
    }

    // Python version depends for specific wheel versions
    depends := getDepends(depProcessor, perVersionDeps, perVersionVariantDeps, versionToPython, rootSpack)
    fmt.Printf("\tDependencies: %d regular, %d variant group(s)\n", len(depProcessor.regularDeps), len(depProcessor.variantDeps))
    logDebugf("Resolved deps regular=%d variantGroups=%d perVersion=%d perVersionVariants=%d pyDeps=%d", len(depProcessor.regularDeps), len(depProcessor.variantDeps), len(perVersionDeps), len(perVersionVariantDeps), len(versionToPython))

    // Compose an inline debug comment block to embed into the generated recipe for easier troubleshooting
    debugLines := []string{}
    debugLines = append(debugLines, fmt.Sprintf("package=%s", packageName))
    if selectedEntry != nil {
        debugLines = append(debugLines, fmt.Sprintf("selected_version=%s", selectedEntry.Version))
    }
    if filename != "" {
        debugLines = append(debugLines, fmt.Sprintf("artifact=%s", filename))
    }
    if selectedEntry != nil && selectedEntry.URL != "" {
        debugLines = append(debugLines, fmt.Sprintf("wheel_url=%s", selectedEntry.URL))
    }
    // summarize regular deps
    if len(depProcessor.regularDeps) > 0 {
        var deps []string
        for d := range depProcessor.regularDeps { deps = append(deps, d) }
        sort.Strings(deps)
        debugLines = append(debugLines, fmt.Sprintf("regular_deps=%d => %s", len(deps), strings.Join(deps, ", ")))
    } else {
        debugLines = append(debugLines, "regular_deps=0")
    }
    // variants
    if len(depProcessor.variantDeps) > 0 {
        var extras []string
        for e := range depProcessor.variantDeps { extras = append(extras, e) }
        sort.Strings(extras)
        for _, e := range extras {
            var ds []string
            for d := range depProcessor.variantDeps[e] { ds = append(ds, d) }
            sort.Strings(ds)
            debugLines = append(debugLines, fmt.Sprintf("variant[%s]=%d => %s", e, len(ds), strings.Join(ds, ", ")))
        }
        // also summarize variant names concisely for quick scanning
        debugLines = append(debugLines, fmt.Sprintf("variant_names=%s", strings.Join(extras, ", ")))
    } else {
        debugLines = append(debugLines, "variants=0")
    }
    // per-version deps
    if len(perVersionDeps) > 0 {
        var vers []string
        for v := range perVersionDeps { vers = append(vers, v) }
        sort.Strings(vers)
        for _, v := range vers {
            var ds []string
            for d := range perVersionDeps[v] { ds = append(ds, d) }
            sort.Strings(ds)
            debugLines = append(debugLines, fmt.Sprintf("per_version[%s]=%d => %s", v, len(ds), strings.Join(ds, ", ")))
        }
    }
    if len(perVersionVariantDeps) > 0 {
        var vers []string
        for v := range perVersionVariantDeps { vers = append(vers, v) }
        sort.Strings(vers)
        for _, v := range vers {
            var extras []string
            for e := range perVersionVariantDeps[v] { extras = append(extras, e) }
            sort.Strings(extras)
            for _, e := range extras {
                var ds []string
                for d := range perVersionVariantDeps[v][e] { ds = append(ds, d) }
                sort.Strings(ds)
                debugLines = append(debugLines, fmt.Sprintf("per_version_variant[%s][%s]=%d => %s", v, e, len(ds), strings.Join(ds, ", ")))
            }
        }
    }
    if len(versionToPython) > 0 {
        var vers []string
        for v := range versionToPython { vers = append(vers, v) }
        sort.Strings(vers)
        for _, v := range vers {
            debugLines = append(debugLines, fmt.Sprintf("python_requires[%s]=%s", v, versionToPython[v]))
        }
    }
    if len(depRecurseTargets) > 0 {
        var names []string
        for n := range depRecurseTargets { names = append(names, n) }
        sort.Strings(names)
        debugLines = append(debugLines, fmt.Sprintf("recursed=%d => %s", len(names), strings.Join(names, ", ")))
    }
    debugComment := strings.Join(debugLines, "\n")
    if strings.TrimSpace(rootPIMDExcerpt) != "" {
        debugComment += "\npyPIMD_excerpt:\n" + rootPIMDExcerpt
    }

    if err := writeRecipe(header, footer, versions, depends, packageName, depProcessor.GetVariants(), debugComment); err != nil {
        return err
    }
    fmt.Printf("\t📦 Wrote recipe to %s\n", filepath.Join("packages", pyify(packageName), "package.py"))
    logDebugf("END get name=%q", packageName)
    // Mark as processed for this run
    processedPackages[nameLower] = struct{}{}

    return nil
}

// ---------------- main ----------------

func shouldSkipExisting(packageName string, force bool) bool {
    if force {
        return false
    }
    // Check in Spack repos if package exists; if yes, skip recreation
    if spackPackageExists(pyify(packageName)) {
        return true
    }
    return false
}

func main() {
    initDebug()
    if len(os.Args) < 2 {
        fmt.Println("Usage: py-package-creator [-f] package_name[==version] [...]")
        os.Exit(1)
    }

    // no upfront global fetch; we'll lazily query spack per package

    force := false
    // Collect package requests (name, optional version)
    type req struct{ name, version string }
    var reqs []req
    i := 1
    for i < len(os.Args) {
        arg := os.Args[i]
        if arg == "-f" { force = true; fmt.Println("Forcing replacing of builtin Spack packages"); i++; continue }
        if arg == "-debug" { debugEnabled = true; if debugLogPath == "" { initDebug() }; fmt.Println("Debug logging enabled"); i++; continue }
        name := arg
        ver := "latest"
        // support name==version syntax
        if strings.Contains(name, "==") {
            parts := strings.SplitN(name, "==", 2)
            name = parts[0]
            if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" { ver = strings.TrimSpace(parts[1]) }
        } else if i+1 < len(os.Args) {
            // if next token looks like a version (digits and dots), treat it as version
            next := os.Args[i+1]
            if matched, _ := regexp.MatchString(`^\d+(?:\.\d+)*.*$`, next); matched {
                ver = next
                i++
            }
        }
        reqs = append(reqs, req{name: name, version: ver})
        i++
    }
    for _, r := range reqs {
        fmt.Printf("Building recipes for %s%s...\n", r.name, func() string { if r.version != "latest" { return "==" + r.version }; return "" }())
        if err := get(r.name, r.version, true, force); err != nil {
            fmt.Println(err.Error())
        }
    }
}

