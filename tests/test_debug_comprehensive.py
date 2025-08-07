#!/usr/bin/env python3

import sys
import os
import subprocess

# Add the current directory to the path so we can import PyPackageCreator
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from PyPackageCreator import DependencyProcessor, getVersions, getDepends

def test_debug_comprehensive():
    """Comprehensive test to debug the exact issue"""
    
    # Test the DependencyProcessor in isolation
    print("=== Testing DependencyProcessor in isolation ===")
    processor = DependencyProcessor()
    
    test_deps = [
        'docutils; extra == "dev"',
        'ipython; extra == "dev"',
        'pytest; extra == "dev"',
        'ipython; extra == "ipython"'
    ]
    
    for dep in test_deps:
        processor.process_dependency(dep)
    
    deps = processor.get_spack_dependencies()
    print("DependencyProcessor output:")
    for dep in deps:
        print(f"  {dep.strip()}")
    
    # Test the getVersions function
    print("\n=== Testing getVersions function ===")
    
    # Create a mock version list similar to what getVersions receives
    mock_releases = {
        "2.5.2": [
            {
                "packagetype": "bdist_wheel",
                "filename": "cadquery-2.5.2-py3-none-any.whl",
                "url": "https://files.pythonhosted.org/packages/fc/96/e0e863c85d4a467302d064faf0a90c688a8a415f176b698b47b57232bed1/cadquery-2.5.2-py3-none-any.whl",
                "digests": {"sha256": "d9d375c702b1e599069a4f049f5d751e6cd296400cd32f72b6c88d1655e61dbc"},
                "python_version": "py3",
                "yanked": False
            }
        ]
    }
    
    try:
        versions, filename, dep_processor = getVersions(mock_releases)
        print(f"getVersions output - versions: {len(versions)}, filename: {filename}")
        print(f"dep_processor variants: {dep_processor.get_variants()}")
        
        # Test getDepends with the dep_processor
        print("\n=== Testing getDepends with dep_processor ===")
        deps = getDepends([], None, dep_processor)
        print("getDepends output:")
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
            
    except Exception as e:
        print(f"ERROR: Exception occurred: {e}")
        import traceback
        traceback.print_exc()

if __name__ == "__main__":
    test_debug_comprehensive() 