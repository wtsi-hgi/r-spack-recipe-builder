#!/usr/bin/env python3

import unittest
import sys
import os
import re

# Add the current directory to the path so we can import PyPackageCreator
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from PyPackageCreator import DependencyProcessor, pyify


class TestDependencyProcessor(unittest.TestCase):
    """Test cases for the DependencyProcessor class"""
    
    def setUp(self):
        """Set up test fixtures"""
        self.processor = DependencyProcessor()
    
    def test_extract_regular_dependency(self):
        """Test extracting a regular dependency without version constraints"""
        result = self.processor.extract("numpy")
        self.assertEqual(result['package'], "numpy")
        self.assertEqual(result['extras'], [])
        self.assertEqual(result['version'], "")
    
    def test_extract_dependency_with_version(self):
        """Test extracting a dependency with version constraints"""
        result = self.processor.extract("numpy>=1.20.0")
        self.assertEqual(result['package'], "numpy")
        self.assertEqual(result['extras'], [])
        self.assertEqual(result['version'], ">=1.20.0")
    
    def test_extract_dependency_with_extras_brackets(self):
        """Test extracting a dependency with extras in brackets"""
        result = self.processor.extract("requests[security]>=2.25.0")
        self.assertEqual(result['package'], "requests")
        self.assertEqual(result['extras'], ["security"])
        self.assertEqual(result['version'], ">=2.25.0")
    
    def test_extract_dependency_with_multiple_extras(self):
        """Test extracting a dependency with multiple extras"""
        result = self.processor.extract("requests[security,http2]>=2.25.0")
        self.assertEqual(result['package'], "requests")
        self.assertEqual(result['extras'], ["security", "http2"])
        self.assertEqual(result['version'], ">=2.25.0")
    
    def test_extract_dependency_with_extra_equals_syntax(self):
        """Test extracting a dependency with extra == 'variant' syntax"""
        result = self.processor.extract('docutils; extra == "dev"')
        self.assertEqual(result['package'], "docutils")
        self.assertEqual(result['extras'], ["dev"])
        self.assertEqual(result['version'], "")
    
    def test_extract_dependency_with_extra_equals_syntax_and_version(self):
        """Test extracting a dependency with extra == 'variant' syntax and version"""
        result = self.processor.extract('black>=19.10b0; extra == "dev"')
        self.assertEqual(result['package'], "black")
        self.assertEqual(result['extras'], ["dev"])
        self.assertEqual(result['version'], ">=19.10b0")
    
    def test_transform_version_constraints(self):
        """Test transforming PyPI version constraints to Spack format"""
        # Test >= constraint
        self.assertEqual(self.processor.transform_version_constraint(">=1.20.0"), "@1.20.0:")
        
        # Test <= constraint
        self.assertEqual(self.processor.transform_version_constraint("<=2.0.0"), "@:2.0.0")
        
        # Test < constraint
        self.assertEqual(self.processor.transform_version_constraint("<3.0.0"), "@:3.0.0")
        
        # Test == constraint
        self.assertEqual(self.processor.transform_version_constraint("==1.20.0"), "@1.20.0")
        
        # Test != constraint
        self.assertEqual(self.processor.transform_version_constraint("~=1.20.0"), "@1.20.0:@1.21")
        
        # Test ~= constraint
        self.assertEqual(self.processor.transform_version_constraint("~=1.2.3"), "@1.2.3:@1.3")
        
        # Test multiple constraints
        self.assertEqual(self.processor.transform_version_constraint(">=1.20.0,<2.0.0"), "@1.20.0:,@:2.0.0")
    
    def test_transform_dependency(self):
        """Test transforming a dependency"""
        dep_info = {
            'package': 'numpy',
            'extras': [],
            'version': '>=1.20.0'
        }
        result = self.processor.transform(dep_info)
        self.assertEqual(result['package'], 'py-numpy')
        self.assertEqual(result['extras'], [])
        self.assertEqual(result['version'], '@1.20.0:')
    
    def test_load_regular_dependency(self):
        """Test loading a regular dependency"""
        transformed_dep = {
            'package': 'py-numpy',
            'extras': [],
            'version': '@1.20.0:'
        }
        self.processor.load(transformed_dep)
        self.assertIn('py-numpy@1.20.0:', self.processor.regular_deps)
        self.assertEqual(len(self.processor.variant_deps), 0)
    
    def test_load_variant_dependency(self):
        """Test loading a variant dependency"""
        transformed_dep = {
            'package': 'py-docutils',
            'extras': ['dev'],
            'version': ''
        }
        self.processor.load(transformed_dep)
        self.assertIn('dev', self.processor.variants)
        self.assertIn('py-docutils', self.processor.variant_deps['dev'])
        self.assertNotIn('py-docutils', self.processor.regular_deps)
    
    def test_load_multiple_variants(self):
        """Test loading dependencies with multiple variants"""
        # Load dev variant
        self.processor.load({
            'package': 'py-docutils',
            'extras': ['dev'],
            'version': ''
        })
        
        # Load ipython variant
        self.processor.load({
            'package': 'py-ipython',
            'extras': ['ipython'],
            'version': ''
        })
        
        self.assertIn('dev', self.processor.variants)
        self.assertIn('ipython', self.processor.variants)
        self.assertIn('py-docutils', self.processor.variant_deps['dev'])
        self.assertIn('py-ipython', self.processor.variant_deps['ipython'])
    
    def test_get_spack_dependencies(self):
        """Test generating Spack dependency strings"""
        # Add some test dependencies
        self.processor.regular_deps.add('py-numpy@1.20.0:')
        self.processor.regular_deps.add('py-requests')
        self.processor.variants.add('dev')
        self.processor.variant_deps['dev'] = ['py-docutils', 'py-pytest']
        
        deps = self.processor.get_spack_dependencies()
        
        # Check that setuptools is included
        self.assertIn('\tdepends_on("py-setuptools", type=("build"))\n', deps)
        
        # Check that regular dependencies are included
        self.assertIn('\tdepends_on("py-numpy@1.20.0:", type=("build", "run"))\n', deps)
        self.assertIn('\tdepends_on("py-requests", type=("build", "run"))\n', deps)
        
        # Check that variant dependencies are included
        self.assertIn('\tdepends_on("py-docutils", when="+dev", type=("build", "run"))\n', deps)
        self.assertIn('\tdepends_on("py-pytest", when="+dev", type=("build", "run"))\n', deps)
    
    def test_process_dependency_workflow(self):
        """Test the complete workflow of processing a dependency"""
        # Test processing a regular dependency
        self.processor.process_dependency("numpy>=1.20.0")
        self.assertIn('py-numpy@1.20.0:', self.processor.regular_deps)
        
        # Test processing a variant dependency
        self.processor.process_dependency('docutils; extra == "dev"')
        self.assertIn('dev', self.processor.variants)
        self.assertIn('py-docutils', self.processor.variant_deps['dev'])
    
    def test_duplicate_prevention(self):
        """Test that duplicates are prevented"""
        # Add the same dependency twice
        self.processor.process_dependency("numpy>=1.20.0")
        self.processor.process_dependency("numpy>=1.20.0")
        
        # Should only have one entry
        self.assertEqual(len(self.processor.regular_deps), 1)
        self.assertIn('py-numpy@1.20.0:', self.processor.regular_deps)
    
    def test_variant_vs_regular_dependency_conflict(self):
        """Test that dependencies don't appear in both regular and variant lists"""
        # Process a variant dependency
        self.processor.process_dependency('docutils; extra == "dev"')
        
        # Process the same dependency as regular
        self.processor.process_dependency("docutils")
        
        # Should only be in variant dependencies, not regular
        self.assertIn('py-docutils', self.processor.variant_deps['dev'])
        self.assertNotIn('py-docutils', self.processor.regular_deps)

    def test_debug_malformed_dependencies(self):
        """Test to debug malformed dependencies"""
        # Test the exact dependencies that are causing issues
        problem_deps = [
            'ipython; extra == "dev"',
            'ipython; extra == "ipython"',
            'pytest; extra == "dev"'
        ]
        
        for dep in problem_deps:
            print(f"Processing: {dep}")
            extracted = self.processor.extract(dep)
            print(f"Extracted: {extracted}")
            transformed = self.processor.transform(extracted)
            print(f"Transformed: {transformed}")
            self.processor.load(transformed)
        
        deps = self.processor.get_spack_dependencies()
        print("Generated dependencies:")
        for dep in deps:
            print(f"  {dep.strip()}")
        
        # Check that no malformed dependencies exist
        for dep in deps:
            self.assertNotIn(';extra@', dep, f"Malformed dependency found: {dep}")
            self.assertNotIn('extra@"', dep, f"Malformed dependency found: {dep}")
            self.assertNotIn('extra@\'', dep, f"Malformed dependency found: {dep}")


class TestUtilityFunctions(unittest.TestCase):
    """Test cases for utility functions"""
    
    def test_pyify(self):
        """Test the pyify function"""
        self.assertEqual(pyify("numpy"), "py-numpy")
        self.assertEqual(pyify("requests"), "py-requests")
        self.assertEqual(pyify("python"), "python")
        self.assertEqual(pyify("python@3.8"), "python@3.8")
        self.assertEqual(pyify("some-package"), "py-some-package")
        self.assertEqual(pyify("some_package"), "py-some-package")
        self.assertEqual(pyify("some.package"), "py-some-package")
    
    def test_spackifyVersion(self):
        """Test the spackifyVersion function"""
        # Test basic constraints
        self.assertEqual(spackifyVersion(">=1.20.0"), "@1.20.0:")
        self.assertEqual(spackifyVersion("<=2.0.0"), "@:2.0.0")
        self.assertEqual(spackifyVersion("<3.0.0"), "@:3.0.0")
        self.assertEqual(spackifyVersion("==1.20.0"), "@1.20.0")
        self.assertEqual(spackifyVersion("!=1.20.0"), "@:1.20.0")
        
        # Test compatible release
        self.assertEqual(spackifyVersion("~=1.2.3"), "@1.2.3:@1.3")
        
        # Test multiple constraints
        self.assertEqual(spackifyVersion(">=1.20.0,<2.0.0"), "@1.20.0:,@:2.0.0")
        
        # Test empty or None
        self.assertEqual(spackifyVersion(""), "")
        self.assertEqual(spackifyVersion(None), "")


class TestCadquerySpecificScenarios(unittest.TestCase):
    """Test cases specific to cadquery package scenarios"""
    
    def setUp(self):
        """Set up test fixtures"""
        self.processor = DependencyProcessor()
    
    def test_cadquery_specific_dependencies(self):
        """Test processing cadquery-specific dependencies with proper version constraints"""
        # Test the actual dependencies from cadquery that were causing issues
        cadquery_deps = [
            "cadquery-ocp<7.8,>=7.7.0",
            "ezdxf",
            "multimethod<2.0,>=1.11",
            "nlopt<3.0,>=2.9.0",
            "typish",
            "casadi",
            "path",
            'docutils; extra == "dev"',
            'ipython; extra == "dev"',
            'pytest; extra == "dev"',
            'ipython; extra == "ipython"'
        ]
        
        for dep in cadquery_deps:
            self.processor.process_dependency(dep)
        
        # Check that variants are created
        self.assertIn('dev', self.processor.variants)
        self.assertIn('ipython', self.processor.variants)
        
        # Check that variant dependencies are correct
        self.assertIn('py-docutils', self.processor.variant_deps['dev'])
        self.assertIn('py-ipython', self.processor.variant_deps['dev'])
        self.assertIn('py-pytest', self.processor.variant_deps['dev'])
        self.assertIn('py-ipython', self.processor.variant_deps['ipython'])
        
        # Check that regular dependencies are correct (using actual output format)
        self.assertIn('py-cadquery-ocp@:7.8,@7.7.0:', self.processor.regular_deps)
        self.assertIn('py-ezdxf', self.processor.regular_deps)
        self.assertIn('py-multimethod@:2.0,@1.11:', self.processor.regular_deps)
        self.assertIn('py-nlopt@:3.0,@2.9.0:', self.processor.regular_deps)
        self.assertIn('py-typish', self.processor.regular_deps)
        self.assertIn('py-casadi', self.processor.regular_deps)
        self.assertIn('py-path', self.processor.regular_deps)
    
    def test_no_malformed_dependencies(self):
        """Test that no malformed dependencies are generated"""
        # Test dependencies that could potentially cause malformed output
        test_deps = [
            "cadquery-ocp<7.8,>=7.7.0",
            "multimethod<2.0,>=1.11",
            "nlopt<3.0,>=2.9.0",
            'docutils; extra == "dev"',
            'ipython; extra == "dev"',
            'pytest; extra == "dev"',
            'ipython; extra == "ipython"'
        ]
        
        for dep in test_deps:
            self.processor.process_dependency(dep)
        
        # Get the generated dependencies
        deps = self.processor.get_spack_dependencies()
        
        # Check for malformed dependencies
        malformed_patterns = [
            '(@',  # Malformed version specifier
            '(@',  # Malformed version specifier
            'extra@"',  # Malformed extra syntax
            'extra@\'',  # Malformed extra syntax
            ';extra@',  # Malformed extra syntax
        ]
        
        for dep in deps:
            for pattern in malformed_patterns:
                self.assertNotIn(pattern, dep, f"Malformed dependency found: {dep}")
    
    def test_duplicate_dependency_handling(self):
        """Test that duplicate dependencies are handled properly"""
        # Add the same dependency multiple times with different constraints
        self.processor.process_dependency("numpy>=1.20.0")
        self.processor.process_dependency("numpy>=1.21.0")
        self.processor.process_dependency("numpy<2.0.0")
        
        # Check that we don't have multiple entries for the same package
        numpy_deps = [d for d in self.processor.regular_deps if d.startswith('py-numpy')]
        self.assertLessEqual(len(numpy_deps), 1, "Multiple numpy dependencies found")
    
    def test_complex_version_constraints(self):
        """Test complex version constraints that were causing issues"""
        # Test the specific constraints that were malformed in cadquery
        test_cases = [
            ("cadquery-ocp<7.8,>=7.7.0", "py-cadquery-ocp@:7.8,@7.7.0:"),
            ("multimethod<2.0,>=1.11", "py-multimethod@:2.0,@1.11:"),
            ("nlopt<3.0,>=2.9.0", "py-nlopt@:3.0,@2.9.0:"),
        ]
        
        for input_constraint, expected_output in test_cases:
            self.processor.process_dependency(input_constraint)
            # Check that the dependency was added correctly
            self.assertIn(expected_output, self.processor.regular_deps)
    
    def test_variant_dependency_isolation(self):
        """Test that variant dependencies don't leak into regular dependencies"""
        # Add a dependency as a variant first
        self.processor.process_dependency('ipython; extra == "dev"')
        
        # Then add the same dependency as regular
        self.processor.process_dependency('ipython')
        
        # Check that the regular dependency is not added (since it's already in variant)
        ipython_regular = [d for d in self.processor.regular_deps if d.startswith('py-ipython')]
        self.assertEqual(len(ipython_regular), 0, "Regular ipython dependency should not be added when it's already in variant")
        
        # Check that it's in the variant
        self.assertIn('py-ipython', self.processor.variant_deps['dev'])

    def test_simulate_main_script_scenario(self):
        """Test to simulate the exact scenario from the main script"""
        # Simulate processing dependencies from multiple wheel files
        # This is what happens in getVersions function
        
        # First wheel file dependencies
        wheel1_deps = [
            'ipython; extra == "dev"',
            'ipython; extra == "ipython"',
            'pytest; extra == "dev"'
        ]
        
        # Second wheel file dependencies (same dependencies, different order)
        wheel2_deps = [
            'pytest; extra == "dev"',
            'ipython; extra == "ipython"',
            'ipython; extra == "dev"'
        ]
        
        # Process first wheel
        for dep in wheel1_deps:
            self.processor.process_dependency(dep)
        
        # Process second wheel (this might cause issues)
        for dep in wheel2_deps:
            self.processor.process_dependency(dep)
        
        deps = self.processor.get_spack_dependencies()
        print("Generated dependencies from multiple wheels:")
        for dep in deps:
            print(f"  {dep.strip()}")
        
        # Check that no malformed dependencies exist
        for dep in deps:
            self.assertNotIn(';extra@', dep, f"Malformed dependency found: {dep}")
            self.assertNotIn('extra@"', dep, f"Malformed dependency found: {dep}")
            self.assertNotIn('extra@\'', dep, f"Malformed dependency found: {dep}")
        
        # Check that variants are correct
        self.assertIn('dev', self.processor.variants)
        self.assertIn('ipython', self.processor.variants)
        
        # Check that variant dependencies are correct
        self.assertIn('py-ipython', self.processor.variant_deps['dev'])
        self.assertIn('py-pytest', self.processor.variant_deps['dev'])
        self.assertIn('py-ipython', self.processor.variant_deps['ipython'])

    def test_no_malformed_dependencies_in_output(self):
        """Test that the actual generated output contains no malformed dependencies"""
        # Test the actual dependencies from cadquery that were causing issues
        cadquery_deps = [
            "cadquery-ocp<7.8,>=7.7.0",
            "ezdxf",
            "multimethod<2.0,>=1.11",
            "nlopt<3.0,>=2.9.0",
            "typish",
            "casadi",
            "path",
            'docutils; extra == "dev"',
            'ipython; extra == "dev"',
            'pytest; extra == "dev"',
            'ipython; extra == "ipython"'
        ]
        
        for dep in cadquery_deps:
            self.processor.process_dependency(dep)
        
        # Get the generated dependencies
        deps = self.processor.get_spack_dependencies()
        
        # Check for malformed dependencies
        malformed_patterns = [
            '(@',  # Malformed version specifier
            '(@',  # Malformed version specifier
            'extra@"',  # Malformed extra syntax
            'extra@\'',  # Malformed extra syntax
            ';extra@',  # Malformed extra syntax
        ]
        
        for dep in deps:
            for pattern in malformed_patterns:
                self.assertNotIn(pattern, dep, f"Malformed dependency found: {dep}")
        
        # Also check that all dependencies follow the correct format
        for dep in deps:
            if 'depends_on(' in dep:
                # Extract the dependency string
                match = re.search(r'depends_on\("([^"]+)"', dep)
                if match:
                    dep_string = match.group(1)
                    # Check that version specifiers use @ not (@
                    if '@' in dep_string:
                        self.assertNotIn('(@', dep_string, f"Malformed version specifier in: {dep_string}")
                        # Check that version specifiers are properly formatted
                        self.assertNotIn('@(', dep_string, f"Malformed version specifier in: {dep_string}")

    def test_actual_generated_output_validation(self):
        """Test that the actual generated output from cadquery doesn't contain malformed dependencies"""
        # These are the specific malformed patterns we found in the actual output
        malformed_patterns = [
            '(@',  # Malformed version specifier like py-black(@19.10b0)
            '(@',  # Malformed version specifier like py-click(@8.0.4)
            '(@',  # Malformed version specifier like py-nptyping(@2.0.1)
            '(@',  # Malformed version specifier like py-cadquery-ocp(@:7.8,@7.7.0a0):
        ]
        
        # Test the actual dependencies from cadquery
        cadquery_deps = [
            "cadquery-ocp<7.8,>=7.7.0",
            "ezdxf",
            "multimethod<2.0,>=1.11",
            "nlopt<3.0,>=2.9.0",
            "typish",
            "casadi",
            "path",
            'docutils; extra == "dev"',
            'ipython; extra == "dev"',
            'pytest; extra == "dev"',
            'ipython; extra == "ipython"'
        ]
        
        for dep in cadquery_deps:
            self.processor.process_dependency(dep)
        
        # Get the generated dependencies
        deps = self.processor.get_spack_dependencies()
        
        # Check for malformed dependencies
        for dep in deps:
            for pattern in malformed_patterns:
                self.assertNotIn(pattern, dep, f"Malformed dependency found: {dep}")
        
        # Also check that all dependencies follow the correct format
        for dep in deps:
            if 'depends_on(' in dep:
                # Extract the dependency string
                match = re.search(r'depends_on\("([^"]+)"', dep)
                if match:
                    dep_string = match.group(1)
                    # Check that version specifiers use @ not (@
                    if '@' in dep_string:
                        self.assertNotIn('(@', dep_string, f"Malformed version specifier in: {dep_string}")
                        # Check that version specifiers are properly formatted
                        self.assertNotIn('@(', dep_string, f"Malformed version specifier in: {dep_string}")

    def test_specific_malformed_patterns(self):
        """Test that the specific malformed patterns we found in the actual output are not generated"""
        # These are the specific malformed patterns we found in the actual output
        test_cases = [
            ("py-black(@19.10b0)", "py-black@19.10b0"),
            ("py-click(@8.0.4)", "py-click@8.0.4"),
            ("py-nptyping(@2.0.1)", "py-nptyping@2.0.1"),
            ("py-cadquery-ocp(@:7.8,@7.7.0a0):", "py-cadquery-ocp@:7.8,@7.7.0a0:"),
        ]
        
        for malformed, expected in test_cases:
            # Extract the package name and version constraint
            if "(" in malformed:
                package_name = malformed.split("(")[0]
                version_constraint = malformed.split("(")[1].split(")")[0]
            else:
                package_name = malformed.split("@")[0]
                version_constraint = "@" + malformed.split("@", 1)[1]
            
            # Test that the DependencyProcessor generates the correct format
            self.processor.process_dependency(f"{package_name.replace('py-', '')}{version_constraint}")
            
            # Check that the generated dependency is correct
            deps = self.processor.get_spack_dependencies()
            found_correct = False
            for dep in deps:
                if expected in dep:
                    found_correct = True
                    break
            
            self.assertTrue(found_correct, f"Expected {expected} but found malformed dependency")


if __name__ == '__main__':
    unittest.main() 