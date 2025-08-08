package main

import "testing"

func TestTransformVersionPart(t *testing.T) {
    dp := NewDependencyProcessor()
    cases := []struct{ in, want string }{
        {">=1.2.3", "@1.2.3:"},
        {"<=2.0", "@:2.0"},
        {"<2.1", "@:2.1"},
        {"==0.9", "@0.9:"}, // treat == as lower bound
        {"~=1.2.3", "@1.2.3:@1.3"},
        {"~=1", "@1:"},
        {">=1.2, <2.0", "@1.2:@2.0"},
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

