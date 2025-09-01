# Python.py Differences Summary

## Overview
This document summarizes the differences between the original `python.py` from `spack/ubuntu-jammy:0.21.1` and the modified version in `spack-img/python.py`.

## Key Changes

### 1. Import Addition
**Line 30**: Added import for `Executable` class
```python
from spack.util.executable import Executable
```

### 2. Enhanced test_imports Method
**Lines 189-207**: Completely rewrote the `test_imports` method in `PythonExtension` class

**Original:**
```python
def test_imports(self):
    """Attempts to import modules of the installed package."""
    # Make sure we are importing the installed modules,
    # not the ones in the source directory
    python = inspect.getmodule(self).python
    for module in self.import_modules:
        with test_part(
            self,
            f"test_imports_{module}",
            purpose=f"checking import of {module}",
            work_dir="spack-test",
        ):
            python("-c", f"import {module}")
```

**Modified:**
```python
def test_imports(self):
    """Attempts to import modules of the installed package."""
    # Ensure imports use the installed site-packages for this prefix
    python = self.spec["python"].command
    
    # Construct PYTHONPATH to include both platlib and purelib under this package prefix
    pkg = self.spec["python"].package
    site_dirs = []
    for directory in {pkg.platlib, pkg.purelib}:
        root = os.path.join(self.prefix, directory)
        if os.path.isdir(root):
            site_dirs.append(root)
    
    existing_pythonpath = os.environ.get("PYTHONPATH", "")
    if site_dirs or existing_pythonpath:
        new_pythonpath = os.pathsep.join(site_dirs + ([existing_pythonpath] if existing_pythonpath else []))
        python.add_default_env("PYTHONPATH", new_pythonpath)
    
    # Hide user packages to avoid interference
    python.add_default_env("PYTHONNOUSERSITE", "1")
    
    for module in self.import_modules:
        with test_part(
            self,
            f"test_imports_{module}",
            purpose=f"checking import of {module}",
            work_dir="spack-test",
        ):
            python("-c", f"import {module}")
```

### 3. New UvPackage Class
**Lines 515-594**: Added entirely new `UvPackage` class and `PythonUvBuilder` class

#### UvPackage Class Features:
- Extends `PythonPackage`
- Uses `python_uv` build system
- Depends on `py-wheel` and `py-setuptools` for build
- Supports both `python_uv` and `python_pip` build systems

#### PythonUvBuilder Class Features:
- Implements uv-based installation
- Uses `uv pip install` command
- Sets `UV_PYTHON` environment variable
- Installs to explicit site-packages directory using `--target`
- Supports both wheel and source installations

## Summary
The modifications add support for uv as an alternative Python package installer alongside pip, with enhanced import testing that properly handles site-packages directories and environment isolation.
