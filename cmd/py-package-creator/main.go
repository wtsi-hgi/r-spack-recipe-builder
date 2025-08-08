package main

import (
    "bufio"
    "bytes"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "os"
    "os/exec"
    "path/filepath"
    "regexp"
    "sort"
    "strings"
)

const spackBin = "spack"

// Global cache of existing spack package versions
var existingVersions map[string][]string

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
    // Handle: package; extra == "dev"
    reExtraDouble := regexp.MustCompile(`^([^;]+);\s*extra\s*==\s*"([^"]+)"(.*)$`)
    if m := reExtraDouble.FindStringSubmatch(depSpec); m != nil {
        packagePart := strings.TrimSpace(m[1])
        extraName := strings.TrimSpace(m[2])
        versionConstraint := strings.TrimSpace(m[3])

        rePkgVer := regexp.MustCompile(`^([^<>=!~]+)(.*)$`)
        var packageName string
        if mv := rePkgVer.FindStringSubmatch(packagePart); mv != nil {
            packageName = strings.TrimSpace(mv[1])
            versionConstraint = strings.TrimSpace(mv[2]) + versionConstraint
        } else {
            packageName = packagePart
        }
        return dependencyInfo{Package: packageName, Extras: []string{extraName}, Version: versionConstraint}
    }

    // Handle: package; extra == 'dev'
    reExtraSingle := regexp.MustCompile(`^([^;]+);\s*extra\s*==\s*'([^']+)'(.*)$`)
    if m := reExtraSingle.FindStringSubmatch(depSpec); m != nil {
        packagePart := strings.TrimSpace(m[1])
        extraName := strings.TrimSpace(m[2])
        versionConstraint := strings.TrimSpace(m[3])

        rePkgVer := regexp.MustCompile(`^([^<>=!~]+)(.*)$`)
        var packageName string
        if mv := rePkgVer.FindStringSubmatch(packagePart); mv != nil {
            packageName = strings.TrimSpace(mv[1])
            versionConstraint = strings.TrimSpace(mv[2]) + versionConstraint
        } else {
            packageName = packagePart
        }
        return dependencyInfo{Package: packageName, Extras: []string{extraName}, Version: versionConstraint}
    }

    // Handle extras: package[extra1,extra2]>=1.0
    reExtras := regexp.MustCompile(`^([^\[]+)\[([^\]]+)\](.*)$`)
    if m := reExtras.FindStringSubmatch(depSpec); m != nil {
        packageName := strings.TrimSpace(m[1])
        extras := strings.Split(m[2], ",")
        for i := range extras {
            extras[i] = strings.TrimSpace(extras[i])
        }
        versionConstraint := strings.TrimSpace(m[3])
        return dependencyInfo{Package: packageName, Extras: extras, Version: versionConstraint}
    }

    // Handle version constraints: package>=1.0
    rePkgVer := regexp.MustCompile(`^([^<>=!~]+)(.*)$`)
    if m := rePkgVer.FindStringSubmatch(depSpec); m != nil {
        return dependencyInfo{Package: strings.TrimSpace(m[1]), Extras: []string{}, Version: strings.TrimSpace(m[2])}
    }

    // No version constraint
    return dependencyInfo{Package: strings.TrimSpace(depSpec), Extras: []string{}, Version: ""}
}

func (dp *DependencyProcessor) transformVersionConstraint(versionConstraint string) string {
    versionConstraint = strings.TrimSpace(versionConstraint)
    if versionConstraint == "" {
        return ""
    }
    if strings.Contains(versionConstraint, ",") {
        parts := strings.Split(versionConstraint, ",")
        var out []string
        for _, p := range parts {
            if t := dp.transformSingleConstraint(strings.TrimSpace(p)); t != "" {
                out = append(out, t)
            }
        }
        return strings.Join(out, ",")
    }
    return dp.transformSingleConstraint(versionConstraint)
}

func (*DependencyProcessor) transformSingleConstraint(constraint string) string {
    constraint = strings.TrimSpace(constraint)
    if strings.Contains(constraint, ";") {
        segs := strings.SplitN(constraint, ";", 2)
        versionPart := strings.TrimSpace(segs[0])
        return transformVersionPart(versionPart)
    }
    return transformVersionPart(constraint)
}

func transformVersionPart(versionPart string) string {
    versionPart = strings.TrimSpace(versionPart)
    switch {
    case strings.Contains(versionPart, ">="):
        v := strings.TrimSpace(strings.ReplaceAll(versionPart, ">=", ""))
        return "@" + v + ":"
    case strings.Contains(versionPart, "<="):
        v := strings.TrimSpace(strings.ReplaceAll(versionPart, "<=", ""))
        return "@:" + v
    case strings.Contains(versionPart, "<"):
        v := strings.TrimSpace(strings.ReplaceAll(versionPart, "<", ""))
        return "@:" + v
    case strings.Contains(versionPart, "=="):
        v := strings.TrimSpace(strings.ReplaceAll(versionPart, "==", ""))
        return "@" + v
    case strings.Contains(versionPart, "~="):
        base := strings.TrimSpace(strings.ReplaceAll(versionPart, "~=", ""))
        parts := strings.Split(base, ".")
        if len(parts) >= 2 {
            major := parts[0]
            minor := parts[1]
            // increment minor
            nextMinor := minor
            if n, err := atoiSafe(minor); err == nil {
                nextMinor = fmt.Sprintf("%d", n+1)
            }
            return fmt.Sprintf("@%s:@%s.%s", base, major, nextMinor)
        }
        return "@" + base + ":"
    default:
        return ""
    }
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

    if versionConstraint == "" || strings.Contains(versionConstraint, ";") || strings.Contains(versionConstraint, "(") || strings.Contains(versionConstraint, "andextra") || strings.Contains(versionConstraint, "platform-") {
        return
    }

    if len(transformed.Extras) > 0 {
        for _, extra := range transformed.Extras {
            dp.variants[extra] = struct{}{}
            if _, ok := dp.variantDeps[extra]; !ok {
                dp.variantDeps[extra] = make(map[string]struct{})
            }
            depString := packageName
            if versionConstraint != "" {
                depString += versionConstraint
            }
            dp.variantDeps[extra][depString] = struct{}{}
        }
        return
    }

    depString := packageName
    if versionConstraint != "" {
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
    out = append(out, "\tdepends_on(\"py-setuptools\", type=(\"build\"))\n")

    // regular deps sorted
    var regs []string
    for dep := range dp.regularDeps {
        regs = append(regs, dep)
    }
    sort.Strings(regs)
    for _, dep := range regs {
        out = append(out, fmt.Sprintf("\tdepends_on(\"%s\", type=(\"build\", \"run\"))\n", dep))
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
            out = append(out, fmt.Sprintf("\tdepends_on(\"%s\", when=\"+%s\", type=(\"build\", \"run\"))\n", d, v))
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

func getExistingVersions() (map[string][]string, error) {
    _ = os.MkdirAll("packages", 0o755)
    _ = os.MkdirAll("libs", 0o755)
    fmt.Println("Fetching package versions, this could take a while...")
    cmd := exec.Command(spackBin, "list", "--format", "version_json", "py-*")
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        return nil, fmt.Errorf("spack list failed: %v: %s", err, stderr.String())
    }
    dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(stdout.Bytes())))
    var arr []spackListEntry
    if err := dec.Decode(&arr); err != nil {
        fmt.Println(stderr.String())
        return nil, err
    }
    fmt.Println("Versions successfully fetched!\n")
    res := make(map[string][]string)
    for _, e := range arr {
        res[e.Name] = e.Versions
    }
    return res, nil
}

// ---------------- PyPI & Libraries.io ----------------

type pypiInfo struct {
    RequiresPython string `json:"requires_python"`
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
    resp, err := http.Get(url)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return nil, fmt.Errorf("failed to retrieve package %s", packageName)
    }
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
    resp, err := http.Get(url)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return nil, fmt.Errorf("\t❌ Failed to retrieve package %s", packageName)
    }
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

func getClassname(packageName string) string {
    s := strings.NewReplacer("-", ".", "_", ".").Replace(packageName)
    parts := strings.Split(s, ".")
    for i := range parts {
        if parts[i] == "" { continue }
        parts[i] = strings.ToUpper(parts[i][:1]) + strings.ToLower(parts[i][1:])
    }
    return strings.Join(parts, "")
}

// getVersions mirrors Python getVersions
func getVersions(versionList map[string][]pypiRelease) ([]string, string, *DependencyProcessor, error) {
    var versions []string
    filename := ""
    depProcessor := NewDependencyProcessor()

    // sort keys for stable output
    var keys []string
    for k := range versionList {
        keys = append(keys, k)
    }
    sort.Strings(keys)

    wheelAnyRegex := regexp.MustCompile(`any\.whl$`)
    manylinuxRegex := regexp.MustCompile(`manylinux[^x]*_x86_64\.whl`)

    for _, ver := range keys {
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
                    // dependencies from wheel metadata
                    if err := addWheelDependencies(depProcessor, wheel.URL); err != nil {
                        // non-fatal
                    }
                } else {
                    pyVerClean := strings.NewReplacer("cp", "", "py", "", "pp", "").Replace(pyVer)
                    versionSuffix := "-py" + pyVerClean
                    versions = append(versions, fmt.Sprintf("\tversion(\"%s%s\", sha256=\"%s\", expand=False, url=\"%s\")\n", ver, versionSuffix, wheel.Digests.Sha256, wheel.URL))
                    filename = wheel.Filename
                    if err := addWheelDependencies(depProcessor, wheel.URL); err != nil {
                        // non-fatal
                    }
                }
            }
        } else if sdistInfo != nil {
            versions = append(versions, fmt.Sprintf("\tversion(\"%s\", sha256=\"%s\")\n", ver, sdistInfo.Digests.Sha256))
            filename = sdistInfo.Filename
        }
    }

    return versions, filename, depProcessor, nil
}

func addWheelDependencies(dp *DependencyProcessor, wheelURL string) error {
    cmd := exec.Command("pyPIMD/pypi", wheelURL)
    out, err := cmd.Output()
    if err != nil {
        return err
    }
    segs := strings.SplitN(string(out), "\n\n", 2)
    lines := strings.Split(segs[0], "\n")
    for _, line := range lines {
        if strings.HasPrefix(line, "Requires-Dist:") {
            dep := strings.TrimSpace(strings.TrimPrefix(line, "Requires-Dist:"))
            dep = strings.ReplaceAll(dep, " ", "")
            dp.ProcessDependency(dep)
        }
    }
    return nil
}

// ---------------- Recipe rendering ----------------

func writeRecipe(header, footer string, versions []string, depends []string, packageName string, variants []string) error {
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

    content := header + variantsSection + "\n\n" + strings.Join(versions, "") + "\n" + strings.Join(depends, "") + footer
    content = strings.ReplaceAll(content, "\t", "    ")

    outPath := filepath.Join(dir, "package.py")
    return os.WriteFile(outPath, []byte(content), 0o644)
}

func getDepends(dp *DependencyProcessor, pyDeps map[string]string) []string {
    dependsOn := []string{}

    if dp != nil {
        dependsOn = append(dependsOn, dp.GetSpackDependencies()...)
    } else {
        dependsOn = append(dependsOn, "\tdepends_on(\"py-setuptools\", type=(\"build\"))\n")
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
    return dependsOn
}

func getTemplate(mode, packageName, description, homepage, className, filename string) (string, string, error) {
    if mode == "+" {
        header := fmt.Sprintf(`# Copyright 2013-2023 Lawrence Livermore National Security, LLC and other
# Spack Project Developers. See the top-level COPYRIGHT file for details.
#
# SPDX-License-Identifier: (Apache-2.0 OR MIT)

from spack.package import *


class Py%s(PythonPackage):
    """%s"""
    
    homepage = "%s"
    pypi = "%s/%s" `, className, description, homepage, packageName, filename)
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
    if versions, ok := existingVersions[pyify(packageName)]; ok && !force && len(versions) >= 0 {
        fmt.Printf("\t✴️ %s already exists in spack\n", pyify(packageName))
        return nil
    }

    libs, err := getLibrariesIO(packageName, packageVersion)
    if err != nil {
        fmt.Println(err.Error())
        return nil
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

    // libraries.io deps
    for _, d := range libs.Dependencies {
        if strings.ToLower(d.Platform) == "pypi" && !d.Optional {
            depName := strings.ToLower(d.ProjectName)
            if d.LatestStable != "" {
                depProcessor.ProcessDependency(fmt.Sprintf("%s==%s", depName, d.LatestStable))
            } else {
                depProcessor.ProcessDependency(depName)
            }
            if recurse {
                _ = get(depName, d.LatestStable, true, false)
            }
        }
    }

    versions, filename, wheelDP, err := getVersions(pypiResp.Releases)
    if err != nil {
        return err
    }

    // Merge wheel dependencies into main
    for d := range wheelDP.regularDeps {
        depProcessor.ProcessDependency(strings.TrimPrefix(d, "py-"))
    }
    for _, deps := range wheelDP.variantDeps {
        for d := range deps {
            depProcessor.ProcessDependency(strings.TrimPrefix(d, "py-"))
        }
    }

    header, footer, err := getTemplate("+", packageName, libs.Description, libs.Homepage, getClassname(packageName), filename)
    if err != nil {
        return err
    }

    // Python version depends for specific wheel versions
    pythonDeps := make(map[string]string)
    for _, vline := range versions {
        // extract version("...") content
        re := regexp.MustCompile(`version\("([^"]+)"`)
        m := re.FindStringSubmatch(vline)
        if len(m) == 2 {
            v := m[1]
            if strings.Contains(v, "-py") {
                parts := strings.SplitN(v, "-py", 2)
                version := parts[0]
                pyVer := parts[1]
                if strings.Contains(pyVer, ".") {
                    pythonDeps[version] = pyVer
                } else if strings.HasPrefix(pyVer, "3") && len(pyVer) >= 2 {
                    pythonDeps[version] = "3." + pyVer[1:]
                } else if strings.HasPrefix(pyVer, "2") && len(pyVer) >= 2 {
                    pythonDeps[version] = "2." + pyVer[1:]
                }
            }
        }
    }

    depends := getDepends(depProcessor, pythonDeps)

    if err := writeRecipe(header, footer, versions, depends, packageName, depProcessor.GetVariants()); err != nil {
        return err
    }
    return nil
}

// ---------------- main ----------------

func main() {
    if len(os.Args) < 2 {
        fmt.Println("Usage: go run ./cmd/py-package-creator [-f] package_name [...package_name]")
        os.Exit(1)
    }

    var err error
    existingVersions, err = getExistingVersions()
    if err != nil {
        fmt.Println(err.Error())
        os.Exit(1)
    }

    force := false
    for i := 1; i < len(os.Args); i++ {
        arg := os.Args[i]
        if arg == "-f" {
            force = true
            fmt.Println("Forcing replacing of builtin Spack packages")
            continue
        }
        fmt.Printf("Building recipes for %s...\n", arg)
        if err := get(arg, "latest", true, force); err != nil {
            fmt.Println(err.Error())
        }
    }
}

