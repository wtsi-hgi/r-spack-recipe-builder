package main

import "testing"

func TestTransformVersionPart(t *testing.T) {
    tests := map[string]string{
        ">=1.2.3": "@1.2.3:",
        "<=2.0":   "@:2.0",
        "<2.1":    "@:2.1",
        "==0.9":   "@0.9",
        "~=1.2.3": "@1.2.3:@1.3",
        "~=1":      "@1:",
        "":         "",
    }
    for in, want := range tests {
        got := transformVersionPart(in)
        if got != want {
            t.Fatalf("transformVersionPart(%q) = %q, want %q", in, got, want)
        }
    }
}

func TestDependencyProcessorExtract(t *testing.T) {
    dp := NewDependencyProcessor()
    dep := dp.Extract("pkg[dev,extras]>=1.0")
    if dep.Package != "pkg" || dep.Version != ">=1.0" || len(dep.Extras) != 2 {
        t.Fatalf("unexpected extract: %+v", dep)
    }
}

func TestShouldSkipExisting(t *testing.T) {
    existingVersions = map[string][]string{
        "py-foo": {"1.0"},
    }
    if !shouldSkipExisting("foo", false) {
        t.Fatal("expected skip when exists and not forced")
    }
    if shouldSkipExisting("foo", true) {
        t.Fatal("expected not skip when forced")
    }
    if shouldSkipExisting("bar", false) {
        t.Fatal("expected not skip when package not present")
    }
}

