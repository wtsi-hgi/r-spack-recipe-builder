package main

import "testing"

func TestParseWheelPythonSpecFromFilename(t *testing.T) {
    cases := []struct{ in, want string }{
        {"pkg-1.0.0-cp310-cp310-manylinux_2_17_x86_64.whl", "3.10:3.10"},
        {"pkg-0.1.0-cp39-abi3-manylinux_2_17_x86_64.whl", "3.9:"},
        {"pkg-0.1.0-py3-none-any.whl", "3:"},
        {"pkg-0.1.0-cp39.cp310-cp39.whl", "3.9:3.10"},
        {"pkg-0.1.0-pp39-pypy39_pp73-any.whl", ""},
    }
    for _, c := range cases {
        got := parseWheelPythonSpecFromFilename(c.in)
        if got != c.want {
            t.Fatalf("parseWheelPythonSpecFromFilename(%q) = %q, want %q", c.in, got, c.want)
        }
    }
}

func TestIntersectSpackRanges(t *testing.T) {
    cases := []struct{ a, b, want string }{
        {"3.8:", "3.10:3.10", "3.10:3.10"},
        {"3.10:3.10", "3.10:", "3.10:3.10"},
        {"3.9:3.11", "3:", "3.9:3.11"},
        {"3.11:", "3.10:3.10", ""},
    }
    for _, c := range cases {
        got := intersectSpackRanges(c.a, c.b)
        if got != c.want {
            t.Fatalf("intersectSpackRanges(%q,%q) = %q, want %q", c.a, c.b, got, c.want)
        }
    }
}

func TestMergePythonSpecs(t *testing.T) {
    // When intersection is non-empty, use it
    if got := mergePythonSpecs("3.8:", "3.10:3.10"); got != "3.10:3.10" {
        t.Fatalf("mergePythonSpecs returned %q, want %q", got, "3.10:3.10")
    }
    // When intersection is empty, prefer wheel-derived spec
    if got := mergePythonSpecs("3.11:", "3.10:3.10"); got != "3.10:3.10" {
        t.Fatalf("mergePythonSpecs returned %q, want %q", got, "3.10:3.10")
    }
    // When only one side present
    if got := mergePythonSpecs("", "3:"); got != "3:" {
        t.Fatalf("mergePythonSpecs returned %q, want %q", got, "3:")
    }
    if got := mergePythonSpecs("3.8:", ""); got != "3.8:" {
        t.Fatalf("mergePythonSpecs returned %q, want %q", got, "3.8:")
    }
}


