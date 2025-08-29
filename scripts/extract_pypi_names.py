#!/usr/bin/env python3
import os
import re
from pathlib import Path

def main():
    root = Path("/home/ubuntu/spack-repo")
    out_path = Path("/home/ubuntu/r-spack-recipe-builder/pypi-pythonpackages.txt")
    inherit_re = re.compile(r"class\s+\w+\(PythonPackage\)")
    pypi_re = re.compile(r"^\s*pypi\s*=\s*['\"]([^'\"]+)['\"]", re.M)
    projects = set()
    for pkg_dir in root.rglob("packages/*"):
        pkg_file = pkg_dir / "package.py"
        if not pkg_file.is_file():
            continue
        try:
            content = pkg_file.read_text(encoding="utf-8", errors="ignore")
        except Exception:
            continue
        if not inherit_re.search(content):
            continue
        m = pypi_re.search(content)
        if not m:
            continue
        val = m.group(1)
        name = val.split("/", 1)[0].strip().lower()
        if name:
            projects.add(name)
    out_path.write_text("\n".join(sorted(projects)) + "\n", encoding="utf-8")
    print(f"Saved {out_path} ({len(projects)} entries)")

if __name__ == "__main__":
    main()

