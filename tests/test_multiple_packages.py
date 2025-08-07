#!/usr/bin/env python3

import sys
import os

# Add the current directory to the path so we can import PyPackageCreator
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from PyPackageCreator import DependencyProcessor

def test_multiple_packages():
    """Test if DependencyProcessor is being reused across multiple packages"""
    
    # Test processing multiple packages with the same processor
    processor = DependencyProcessor()
    
    # First package
    print("=== Processing first package ===")
    deps1 = [
        'docutils; extra == "dev"',
        'ipython; extra == "dev"',
        'pytest; extra == "dev"'
    ]
    
    for dep in deps1:
        processor.process_dependency(dep)
    
    deps = processor.get_spack_dependencies()
    print("First package dependencies:")
    for dep in deps:
        print(f"  {dep.strip()}")
    
    # Second package (without resetting)
    print("\n=== Processing second package (without reset) ===")
    deps2 = [
        'black; extra == "dev"',
        'click; extra == "dev"'
    ]
    
    for dep in deps2:
        processor.process_dependency(dep)
    
    deps = processor.get_spack_dependencies()
    print("Combined dependencies:")
    for dep in deps:
        print(f"  {dep.strip()}")
    
    # Check for malformed dependencies
    malformed_found = False
    for dep in deps:
        if ';extra@' in dep or 'extra@"' in dep or 'extra@\'' in dep:
            print(f"ERROR: Malformed dependency found: {dep}")
            malformed_found = True
    
    if not malformed_found:
        print("SUCCESS: No malformed dependencies found!")
    else:
        print("ERROR: Malformed dependencies found!")

if __name__ == "__main__":
    test_multiple_packages() 