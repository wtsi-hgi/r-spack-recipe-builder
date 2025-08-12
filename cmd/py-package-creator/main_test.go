package main

import (
    "os"
    "strings"
    "testing"
)

func TestTransformVersionPart(t *testing.T) {
    dp := NewDependencyProcessor()
    cases := []struct{ in, want string }{
        {">=1.2.3", "@1.2.3:"},
        {"<=2.0", "@:2.0"},
        {"<2.1", "@:2.1"},
        {"==0.9", "@0.9:"}, // treat == as lower bound
        {"!=1.0", ""},     // ignore not-equal
        // For ~=1.2.3 choose range >=1.2.3, <1.3 according to pep440
        {"~=1.2.3", "@1.2.3:1.3"},
        {"~=1", "@1:"},
        {">=1.2, <2.0", "@1.2:2.0"},
        {"(>=3.7)", "@3.7:"},
        {"", ""},
    }
    for _, c := range cases {
        if got := dp.transformVersionConstraint(c.in); got != c.want {
            t.Fatalf("transformVersionConstraint(%q) = %q, want %q", c.in, got, c.want)
        }
    }
}

func TestDependencyProcessorExtract(t *testing.T) {
    dp := NewDependencyProcessor()
    dep := dp.Extract("pkg[dev,extras](>=1.0)")
    if dep.Package != "pkg" || dep.Version != ">=1.0" || len(dep.Extras) != 2 {
        t.Fatalf("unexpected extract: %+v", dep)
    }
    dep2 := dp.Extract("pkg (>=1.0) ; extra == 'foo'")
    if dep2.Package != "pkg" || dep2.Version != ">=1.0" || len(dep2.Extras) != 1 || dep2.Extras[0] != "foo" {
        t.Fatalf("unexpected extract 2: %+v", dep2)
    }
}

func TestNormalizeConstraint(t *testing.T) {
    cases := map[string]string{
        "py-foo@1.2":   "py-foo@1.2:",
        "py-foo@1.2:2": "py-foo@1.2:2",
        "py-foo":       "py-foo",
        // ensure accidental hyphens in numeric versions are normalized
        "py-foo>=0-24-2": "py-foo@0.24.2:",
    }
    for in, want := range cases {
        if got := normalizeConstraint(in); got != want {
            t.Fatalf("normalizeConstraint(%q)=%q want %q", in, got, want)
        }
    }
}

func TestGetDependsCycleFilterWithLocalRecipe(t *testing.T) {
    // Create a local package that depends back on root to simulate a two-node cycle
    root := "py-root"
    dep := "py-dep"
    // local dep recipe that depends on root
    dir := "packages/" + dep
    _ = os.MkdirAll(dir, 0o755)
    content := "from spack.package import *\n\nclass PyDep(PythonPackage):\n    pass\n\n    depends_on(\"" + root + "@1.0:\", type=(\"build\", \"run\"))\n"
    if err := os.WriteFile(dir+"/package.py", []byte(content), 0o644); err != nil {
        t.Fatalf("failed to write local dep recipe: %v", err)
    }

    dp := NewDependencyProcessor()
    // add a regular dep on dep
    dp.regularDeps[dep] = struct{}{}
    lines := getDepends(dp, nil, nil, nil, root)
    joined := strings.Join(lines, "")
    if strings.Contains(joined, "\""+dep+"\"") {
        t.Fatalf("expected cycle-filter to drop %s when local recipe depends on %s, got: %s", dep, root, joined)
    }
}

func TestShouldSkipExisting(t *testing.T) {
    // The existence check uses spack; this test only verifies force logic and that
    // forcing disables skip regardless of existence in Spack.
    if shouldSkipExisting("foo", true) {
        t.Fatal("expected not skip when forced")
    }
}

func TestGetDependsAddsDefaultPython(t *testing.T) {
    dp := NewDependencyProcessor()
    got := strings.Join(getDepends(dp, nil, nil, nil, "py-foo"), "")
    if !strings.Contains(got, "depends_on(\"py-setuptools\"") {
        t.Fatalf("expected setuptools dependency, got: %s", got)
    }
    if !strings.Contains(got, "type=(\"build\",)") {
        t.Fatalf("expected setuptools type single-element tuple with trailing comma, got: %s", got)
    }
    if !strings.Contains(got, "depends_on(\"python\"") {
        t.Fatalf("expected default python dependency when none discovered, got: %s", got)
    }
}

func TestWriteRecipeDoesNotRewritePythonMultipart(t *testing.T) {
    header := "from spack.package import *\n\nclass PyFoo(PythonPackage):\n    pass\n"
    footer := ""
    versions := []string{"\tversion(\"1.0\", sha256=\"deadbeef\")\n"}
    deps := []string{
        "\tdepends_on(\"py-python-multipart@0.0.20:\", type=(\"build\", \"run\"))\n",
        "\tdepends_on(\"py-python-dotenv@0.19.0:\", type=(\"build\", \"run\"))\n",
        "\tdepends_on(\"py-python@3.9:\", type=(\"build\", \"run\"))\n",
    }
    _ = os.RemoveAll("packages/py-foo")
    if err := writeRecipe(header, footer, versions, deps, "foo", nil, ""); err != nil {
        t.Fatalf("writeRecipe error: %v", err)
    }
    data, err := os.ReadFile("packages/py-foo/package.py")
    if err != nil { t.Fatalf("failed reading generated recipe: %v", err) }
    content := string(data)
    if !strings.Contains(content, "depends_on(\"py-python-multipart@0.0.20:") {
        t.Fatalf("expected python-multipart to remain prefixed as py-python-multipart, got: %s", content)
    }
    if !strings.Contains(content, "depends_on(\"py-python-dotenv@0.19.0:") {
        t.Fatalf("expected python-dotenv to remain prefixed as py-python-dotenv, got: %s", content)
    }
    if !strings.Contains(content, "depends_on(\"python@3.9:") {
        t.Fatalf("expected core python to be normalized to python@, got: %s", content)
    }
}

func TestTemplateHasImportModulesAndNormalizedPypi(t *testing.T) {
    header, _, err := getTemplate("+", "Peppy", "desc", "https://example.com", "Peppy", "ignored.tar.gz", "")
    if err != nil {
        t.Fatalf("getTemplate error: %v", err)
    }
    if !strings.Contains(header, "import_modules = [\"peppy\"]") {
        t.Fatalf("expected import_modules with normalized module name, got header: %s", header)
    }
    if !strings.Contains(header, "pypi = \"peppy/peppy-@.tar.gz\"") {
        t.Fatalf("expected normalized pypi path with '@' placeholder, got header: %s", header)
    }
}

func TestTemplateDescriptionRenderedAsCommentsNotDocstring(t *testing.T) {
    // Description with quotes and newlines should be rendered as comment lines, not a Python string literal
    desc := "A \"quoted\" description with\nnewlines"
    header, _, err := getTemplate("+", "pkg-name", desc, "https://h.example/\"hp\"", "PkgName", "ignored.tar.gz", "")
    if err != nil { t.Fatalf("getTemplate error: %v", err) }
    if strings.Contains(header, "\"\"\"") {
        t.Fatalf("did not expect triple-quoted docstring in header: %s", header)
    }
    if !strings.Contains(header, "\n    # A ") {
        t.Fatalf("expected description to appear as indented comments, got: %s", header)
    }
    if !strings.Contains(header, "homepage = \"") {
        t.Fatalf("expected homepage assignment present, got: %s", header)
    }
}

func TestTemplateUsesFilenameSuffix(t *testing.T) {
    header, _, err := getTemplate("+", "My_Pkg", "desc", "https://example.com", "MyPkg", "My_Pkg-1.0.zip", "")
    if err != nil {
        t.Fatalf("getTemplate error: %v", err)
    }
    // pypi name should normalize underscores to hyphens and pick .zip suffix
    // With no selectedVersion provided, we still emit placeholder '@'
    if !strings.Contains(header, "pypi = \"my-pkg/my-pkg-@.zip\"") {
        t.Fatalf("expected pypi path to use .zip suffix when filename indicates zip, got header: %s", header)
    }
}

func TestTemplateBakesSelectedVersionIntoPypiPath(t *testing.T) {
    header, _, err := getTemplate("+", "Peppy", "desc", "https://example.com", "Peppy", "peppy-0.40.7.tar.gz", "0.40.7")
    if err != nil {
        t.Fatalf("getTemplate error: %v", err)
    }
    if !strings.Contains(header, "pypi = \"peppy/peppy-0.40.7.tar.gz\"") {
        t.Fatalf("expected explicit version baked into pypi path, got header: %s", header)
    }
}

func TestWriteRecipeAddsDebugBannerHints(t *testing.T) {
    // Minimal header/footer; we only assert banner presence in output file
    header := "from spack.package import *\n\nclass PyFoo(PythonPackage):\n    pass\n"
    footer := ""
    versions := []string{}
    deps := []string{}
    pkg := "foo"
    // Ensure clean slate
    _ = os.RemoveAll("packages/py-foo")
    if err := writeRecipe(header, footer, versions, deps, pkg, nil, "debug-line"); err != nil {
        t.Fatalf("writeRecipe error: %v", err)
    }
    data, err := os.ReadFile("packages/py-foo/package.py")
    if err != nil {
        t.Fatalf("failed reading generated recipe: %v", err)
    }
    content := string(data)
    if !strings.Contains(content, "Generated by py-package-creator") {
        t.Fatalf("expected debug banner in recipe, got: %s", content)
    }
    if !strings.Contains(content, "spack install py-foo") {
        t.Fatalf("expected reproduce hint with py-foo, got: %s", content)
    }
    if !strings.Contains(content, "# debug-line") {
        t.Fatalf("expected embedded debug comment line, got: %s", content)
    }
}

func TestWriteRecipeDebugBannerIndented(t *testing.T) {
    header := "from spack.package import *\n\nclass PyBar(PythonPackage):\n    pass\n"
    footer := ""
    _ = os.RemoveAll("packages/py-bar")
    if err := writeRecipe(header, footer, nil, nil, "bar", nil, ""); err != nil {
        t.Fatalf("writeRecipe error: %v", err)
    }
    data, err := os.ReadFile("packages/py-bar/package.py")
    if err != nil { t.Fatalf("failed reading generated recipe: %v", err) }
    content := string(data)
    if !strings.Contains(content, "\n    # Generated by py-package-creator") {
        t.Fatalf("expected debug banner to be indented inside class body, got: %s", content)
    }
}

func TestWriteRecipeBannerNoVerboseDebugArtifacts(t *testing.T) {
    // We no longer embed Spack log tails or explicit debug paths in recipes.
    _ = os.MkdirAll("logs", 0o755)
    path := "logs/spack-install_20991231-235959.log"
    _ = os.WriteFile(path, []byte("irrelevant"), 0o644)

    header := "from spack.package import *\n\nclass PyFoo(PythonPackage):\n    pass\n"
    footer := ""
    pkg := "foo-embed"
    _ = os.RemoveAll("packages/py-foo-embed")
    if err := writeRecipe(header, footer, nil, nil, pkg, nil, ""); err != nil {
        t.Fatalf("writeRecipe error: %v", err)
    }
    data, err := os.ReadFile("packages/py-foo-embed/package.py")
    if err != nil { t.Fatalf("failed reading generated recipe: %v", err) }
    content := string(data)
    if !strings.Contains(content, "Generated by py-package-creator") {
        t.Fatalf("expected short banner, got: %s", content)
    }
    if strings.Contains(content, "Spack install log") {
        t.Fatalf("did not expect Spack install log details in recipe, got: %s", content)
    }
}

func TestWriteRecipeIgnoresExplicitSpackLogEnv(t *testing.T) {
    // Even if SPACK_INSTALL_LOG is set, it should not be embedded.
    _ = os.MkdirAll("logs", 0o755)
    older := "logs/spack-install_20000101-000000.log"
    _ = os.WriteFile(older, []byte("older-log-line"), 0o644)
    t.Setenv("SPACK_INSTALL_LOG", older)

    header := "from spack.package import *\n\nclass PyBaz(PythonPackage):\n    pass\n"
    footer := ""
    _ = os.RemoveAll("packages/py-baz")
    if err := writeRecipe(header, footer, nil, nil, "baz", nil, ""); err != nil {
        t.Fatalf("writeRecipe error: %v", err)
    }
    data, err := os.ReadFile("packages/py-baz/package.py")
    if err != nil { t.Fatalf("failed reading generated recipe: %v", err) }
    content := string(data)
    if strings.Contains(content, older) {
        t.Fatalf("did not expect recipe to include SPACK_INSTALL_LOG path %s, got: %s", older, content)
    }
}

func TestWriteRecipeDoesNotIncludeSessionDebugPathWhenEnabled(t *testing.T) {
    _ = os.MkdirAll("logs", 0o755)
    // Simulate a debug session but ensure path is not embedded
    debugEnabled = true
    debugLogPath = "logs/session-test.log"
    _ = os.WriteFile(debugLogPath, []byte(""), 0o644)

    header := "from spack.package import *\n\nclass PyQux(PythonPackage):\n    pass\n"
    footer := ""
    _ = os.RemoveAll("packages/py-qux")
    if err := writeRecipe(header, footer, nil, nil, "qux", nil, ""); err != nil {
        t.Fatalf("writeRecipe error: %v", err)
    }
    data, err := os.ReadFile("packages/py-qux/package.py")
    if err != nil { t.Fatalf("failed reading generated recipe: %v", err) }
    content := string(data)
    if strings.Contains(content, debugLogPath) {
        t.Fatalf("did not expect recipe to include session debug log path %s, got: %s", debugLogPath, content)
    }
    // reset
    debugEnabled = false
    debugLogPath = ""
}

func TestLocalRecipeDependsOn(t *testing.T) {
    // create a temp recipe content in memory by writing to a temp dir is overkill here;
    // instead, ensure the helper safely returns false when file missing
    if localRecipeDependsOn("py-nonexistent-foo", "py-bar") {
        t.Fatalf("expected false for missing local recipe")
    }
}

func TestLocalRecipeDependsOnWithVersionedNeedle(t *testing.T) {
    // Ensure the detector matches depends_on("py-bar@...") not just plain quotes
    dir := "packages/py-foo"
    _ = os.MkdirAll(dir, 0o755)
    content := "" +
        "from spack.package import *\n\n" +
        "class PyFoo(PythonPackage):\n    pass\n\n" +
        "    depends_on(\"py-bar@1.0:\", type=(\"build\", \"run\"))\n"
    if err := os.WriteFile(dir+"/package.py", []byte(content), 0o644); err != nil {
        t.Fatalf("failed to write temp recipe: %v", err)
    }
    if !localRecipeDependsOn("py-foo", "py-bar") {
        t.Fatalf("expected true when local recipe depends on py-bar with version constraint")
    }
}

func TestModuleImportNameNormalization(t *testing.T) {
    cases := map[string]string{
        "Peppy":             "peppy",
        "foo-bar":           "foo_bar",
        "pkg.name":          "pkg_name",
        "Name With Spaces":  "name_with_spaces",
        "foo[extra,dev]":    "foo",
    }
    for in, want := range cases {
        if got := moduleImportName(in); got != want {
            t.Fatalf("moduleImportName(%q)=%q want %q", in, got, want)
        }
    }
}

func TestSanitizePackageName(t *testing.T) {
    cases := map[string]string{
        "pkg >=1.2":                    "pkg",
        "pkg[extra] (>=1.0)":           "pkg",
        "pkg; python_version<'3.9'":     "pkg",
        "Pkg_Name==1.2.3,":              "pkg_name",
    }
    for in, want := range cases {
        if got := sanitizePackageName(in); got != want {
            t.Fatalf("sanitizePackageName(%q)=%q want %q", in, got, want)
        }
    }
}

func TestSanitizeVariantName(t *testing.T) {
    cases := map[string]string{
        "Dev":           "dev",
        "dev-extra":     "dev_extra",
        "dev.extra":     "dev_extra",
        "Dev Extra":     "dev_extra",
        "weird@name!":   "weirdname",
        "__A__B__":      "__a__b__",
        "":              "extra",
    }
    for in, want := range cases {
        if got := sanitizeVariantName(in); got != want {
            t.Fatalf("sanitizeVariantName(%q)=%q want %q", in, got, want)
        }
    }
}

