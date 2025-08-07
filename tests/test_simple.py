#!/usr/bin/env python3

import sys
import os

# Add the current directory to the path so we can import PyPackageCreator
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from PyPackageCreator import DependencyProcessor

def test_simple():
    """Simple test to verify the DependencyProcessor is working"""
    processor = DependencyProcessor()
    
    # Test the exact dependencies from cadquery
    test_deps = [
        'docutils; extra == "dev"',
        'ipython; extra == "dev"',
        'pytest; extra == "dev"',
        'ipython; extra == "ipython"'
    ]
    
    for dep in test_deps:
        processor.process_dependency(dep)
    
    deps = processor.get_spack_dependencies()
    
    print("Generated dependencies:")
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
    test_simple() 