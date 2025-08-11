#!/usr/bin/env python3

import json
import os
import re
import subprocess
import sys

import requests

spackBin = "spack"


class DependencyProcessor:
    """ETL class to process PyPI dependencies and convert them to Spack format"""
    
    def __init__(self):
        self.variants = set()
        self.variant_deps = {}
        self.regular_deps = set()  # Use set to avoid duplicates
        self.python_deps = {}
    
    def extract(self, dep_spec):
        """Extract dependency information from PyPI specification"""
        # Handle extra == "variant" syntax: package; extra == "dev"
        extra_equals_match = re.match(r'^([^;]+);\s*extra\s*==\s*"([^"]+)"(.*)$', dep_spec)
        if extra_equals_match:
            package_part = extra_equals_match.group(1).strip()
            extra_name = extra_equals_match.group(2).strip()
            version_constraint = extra_equals_match.group(3).strip()
            
            # Parse package name and version from the package part
            version_match = re.match(r'^([^<>=!~]+)(.*)$', package_part)
            if version_match:
                package_name = version_match.group(1).strip()
                version_constraint = version_match.group(2).strip() + version_constraint
            else:
                package_name = package_part
                version_constraint = version_constraint
            
            return {
                'package': package_name,
                'extras': [extra_name],
                'version': version_constraint
            }
        
        # Handle extra == 'variant' syntax: package; extra == 'dev'
        extra_equals_single_match = re.match(r"^([^;]+);\s*extra\s*==\s*'([^']+)'(.*)$", dep_spec)
        if extra_equals_single_match:
            package_part = extra_equals_single_match.group(1).strip()
            extra_name = extra_equals_single_match.group(2).strip()
            version_constraint = extra_equals_single_match.group(3).strip()
            
            # Parse package name and version from the package part
            version_match = re.match(r'^([^<>=!~]+)(.*)$', package_part)
            if version_match:
                package_name = version_match.group(1).strip()
                version_constraint = version_match.group(2).strip() + version_constraint
            else:
                package_name = package_part
                version_constraint = version_constraint
            
            return {
                'package': package_name,
                'extras': [extra_name],
                'version': version_constraint
            }
        
        # Handle extras: package[extra1,extra2]>=1.0
        extra_match = re.match(r'^([^[]+)\[([^\]]+)\](.*)$', dep_spec)
        if extra_match:
            package_name = extra_match.group(1).strip()
            extras = [e.strip() for e in extra_match.group(2).split(",")]
            version_constraint = extra_match.group(3).strip()
            return {
                'package': package_name,
                'extras': extras,
                'version': version_constraint
            }
        
        # Handle version constraints: package>=1.0
        version_match = re.match(r'^([^<>=!~]+)(.*)$', dep_spec)
        if version_match:
            package_name = version_match.group(1).strip()
            version_constraint = version_match.group(2).strip()
            return {
                'package': package_name,
                'extras': [],
                'version': version_constraint
            }
        
        # No version constraint
        return {
            'package': dep_spec.strip(),
            'extras': [],
            'version': ""
        }
    
    def transform_version_constraint(self, version_constraint):
        """Transform PyPI version constraints to Spack format"""
        if not version_constraint:
            return ""
        
        # Handle multiple constraints separated by commas
        if "," in version_constraint:
            constraints = [c.strip() for c in version_constraint.split(",")]
            spack_constraints = []
            for constraint in constraints:
                spack_constraint = self.transform_single_constraint(constraint)
                if spack_constraint:
                    spack_constraints.append(spack_constraint)
            return ",".join(spack_constraints)
        else:
            return self.transform_single_constraint(version_constraint)
    
    def transform_single_constraint(self, constraint):
        """Transform a single version constraint to Spack format"""
        constraint = constraint.strip()
        
        # Handle complex constraints with extra conditions (like platform-specific)
        if ";" in constraint:
            # Split on semicolon to separate version from conditions
            parts = constraint.split(";", 1)
            version_part = parts[0].strip()
            conditions = parts[1].strip()
            
            # Transform the version part
            version_constraint = self.transform_version_part(version_part)
            
            # For now, skip complex conditions to avoid malformed constraints
            # In a more sophisticated implementation, we could parse and transform conditions
            return version_constraint
        else:
            return self.transform_version_part(constraint)
    
    def transform_version_part(self, version_part):
        """Transform just the version part of a constraint"""
        version_part = version_part.strip()
        
        if ">=" in version_part:
            version = version_part.replace(">=", "").strip()
            return f"@{version}:"
        elif "<=" in version_part:
            version = version_part.replace("<=", "").strip()
            return f"@:{version}"
        elif "<" in version_part:
            version = version_part.replace("<", "").strip()
            return f"@:{version}"
        elif "==" in version_part:
            version = version_part.replace("==", "").strip()
            return f"@{version}"
        elif "~=" in version_part:
            # Compatible release - convert to >= and <
            base_version = version_part.replace("~=", "").strip()
            parts = base_version.split(".")
            if len(parts) >= 2:
                # For ~= 1.2.3, this means >= 1.2.3, < 1.3
                major, minor = parts[0], parts[1]
                next_minor = str(int(minor) + 1)
                return f"@{base_version}:@{major}.{next_minor}"
            else:
                return f"@{base_version}:"
        else:
            return ""
    
    def transform(self, dep_info):
        """Transform extracted dependency information to Spack format"""
        package_name = pyify(dep_info['package'])
        version_constraint = self.transform_version_constraint(dep_info['version'])
        
        return {
            'package': package_name,
            'extras': dep_info['extras'],
            'version': version_constraint
        }
    
    def load(self, transformed_dep):
        """Load transformed dependency into appropriate collections"""
        package_name = transformed_dep['package']
        version_constraint = transformed_dep['version']
        
        # Skip if the version constraint is malformed or empty
        if not version_constraint or ";" in version_constraint or "(" in version_constraint or "andextra" in version_constraint or "platform-" in version_constraint:
            return
        
        if transformed_dep['extras']:
            # Handle extras as variants - only add to variant dependencies, not regular dependencies
            for extra in transformed_dep['extras']:
                self.variants.add(extra)
                if extra not in self.variant_deps:
                    self.variant_deps[extra] = []
                
                # Create dependency string for this variant
                dep_string = f"{package_name}"
                if version_constraint:
                    dep_string += version_constraint
                
                # Only add if not already present
                if dep_string not in self.variant_deps[extra]:
                    self.variant_deps[extra].append(dep_string)
        else:
            # Regular dependency - handle potential duplicates
            dep_string = f"{package_name}"
            if version_constraint:
                dep_string += version_constraint
            
            # Check if this dependency is already in any variant dependencies
            in_variant = False
            for variant_deps_list in self.variant_deps.values():
                if dep_string in variant_deps_list:
                    in_variant = True
                    break
            
            # Only add to regular dependencies if not in any variant
            if not in_variant:
                # Check if we already have this package
                existing_deps = [d for d in self.regular_deps if d.startswith(f"{package_name}@")]
                if existing_deps:
                    # If we have existing version constraints, skip to avoid duplicates
                    # In a more sophisticated implementation, we could merge version ranges
                    pass
                else:
                    # No existing version constraints for this package, add it
                    self.regular_deps.add(dep_string)
    
    def process_dependency(self, dep_spec):
        """Process a single dependency specification"""
        extracted = self.extract(dep_spec)
        transformed = self.transform(extracted)
        self.load(transformed)
    
    def process_dependencies(self, dependencies):
        """Process a list of dependency specifications"""
        for dep_spec in dependencies:
            self.process_dependency(dep_spec)
    
    def get_spack_dependencies(self):
        """Generate Spack dependency strings"""
        depends_on = []
        depends_on.append('\tdepends_on("py-setuptools", type=("build"))\n')
        
        # Add regular dependencies
        for dep in sorted(self.regular_deps):
            depends_on.append(f'\tdepends_on("{dep}", type=("build", "run"))\n')
        
        # Add variant-specific dependencies
        for variant in sorted(self.variant_deps.keys()):
            for dep in sorted(self.variant_deps[variant]):
                depends_on.append(f'\tdepends_on("{dep}", when="+{variant}", type=("build", "run"))\n')
        
        return depends_on
    
    def get_variants(self):
        """Get the set of variants"""
        return self.variants


def getExistingVersions():
    os.makedirs("packages", exist_ok=True)
    os.makedirs("libs", exist_ok=True)
    print("Fetching package versions, this could take a while...")
    stream = subprocess.run([spackBin, "list", "--format", "version_json", "py-*"], capture_output=True)
    decoded = stream.stdout.decode("utf-8").strip()
    try:
        builtin = json.loads(decoded)
    except json.JSONDecodeError:
        print(stream.stderr.decode("utf-8"))
        exit(1)
    print("Versions successfully fetched!\n")
    packageVersions = {}
    for row in builtin:
        packageVersions[row["name"]] = row["versions"]
    return packageVersions


def getPyPiJson(package_name):
    request = requests.get(f"https://pypi.org/pypi/{package_name}/json")
    if request.status_code != 200:
        print(f"Failed to retrieve package {package_name}")
        exit()
    return request.json()


def pyify(package):
    if package == "python" or package.startswith("python@"):
        return package
    return "py-" + package.lower().replace(".", "-").replace("_", "-").replace(" ", "-").split("[")[0]


def getVersions(versionList):
    versions = []
    filename = ""
    
    # Create dependency processor
    dep_processor = DependencyProcessor()
    
    for i in versionList.keys():
        if versionList[i] == []:
            continue

        # Group wheels by Python version
        python_version_wheels = {}
        sdist_info = None

        for release in versionList[i]:
            if release["yanked"]:
                continue
            
            # Skip release candidate versions
            if "rc" in i.lower():
                continue

            if release["packagetype"] == "bdist_wheel" and (
                release["filename"].endswith("any.whl") or
                re.search(r"manylinux[^x]*_x86_64\.whl", release["filename"])
            ):
                py_ver = release["python_version"]
                if py_ver not in python_version_wheels:
                    python_version_wheels[py_ver] = []
                python_version_wheels[py_ver].append(release)
            elif release["packagetype"] == "sdist":
                sdist_info = release

        # Process wheels for each Python version
        for py_ver, wheels in python_version_wheels.items():
            if py_ver in ["any", "py3", "py2.py3"]:  # Handle both universal and py3 wheels
                wheel = wheels[0]  # Take first wheel since it's universal/py3-compatible
                versions.append(
                    f'\tversion("{i}", sha256="{wheel["digests"]["sha256"]}", expand=False, url="{wheel["url"]}")\n'
                )
                filename = wheel["filename"]

                # Get dependencies
                cmd = subprocess.run(["pyPIMD/pypi", wheel["url"]], capture_output=True)
                decoded = cmd.stdout.decode("utf-8").split("\n\n")[0].split("\n")
                for j in decoded:
                    if j.startswith("Requires-Dist:"):
                        dep_spec = j.replace("Requires-Dist: ", "").replace(" ", "")
                        dep_processor.process_dependency(dep_spec)
            else:
                # Handle Python-specific wheels
                wheel = wheels[0]  # Take first wheel for this Python version
                py_ver_clean = py_ver.replace("cp", "").replace("py", "").replace("pp", "")
                version_suffix = f"-py{py_ver_clean}"

                versions.append(
                    f'\tversion("{i}{version_suffix}", sha256="{wheel["digests"]["sha256"]}", expand=False, url="{wheel["url"]}")\n'
                )
                filename = wheel["filename"]

                # Get dependencies
                cmd = subprocess.run(["pyPIMD/pypi", wheel["url"]], capture_output=True)
                decoded = cmd.stdout.decode("utf-8").split("\n\n")[0].split("\n")
                for j in decoded:
                    if j.startswith("Requires-Dist:"):
                        dep_spec = j.replace("Requires-Dist: ", "").replace(" ", "")
                        dep_processor.process_dependency(dep_spec)

        # If no wheels found, use sdist
        if not python_version_wheels and sdist_info:
            versions.append(f'\tversion("{i}", sha256="{sdist_info["digests"]["sha256"]}")\n')
            filename = sdist_info["filename"]

    return versions, filename, dep_processor


def getClassname(package):
    classname = package.replace("-", ".").replace("_", ".").split(".")
    for i in range(len(classname)):
        classname[i] = classname[i].capitalize()
    return "".join(classname)


def writeRecipe(header, footer, versions, depends, package, variants=None):
    os.makedirs("packages/" + pyify(package), exist_ok=True)
    
    # Add variants if they exist
    variants_section = ""
    if variants:
        variants_section = "\n    # Variants for extras\n"
        for variant in sorted(variants):
            variants_section += f'    variant("{variant}", default=False, description="Enable {variant} extra")\n'
    
    content = f"""{header}{variants_section}

{"".join(versions)}
{"".join(depends)}{footer}"""
    # Replace all tabs with 4 spaces for PEP8 compliance
    content = content.replace("\t", "    ")
    with open("packages/" + pyify(package) + "/package.py", "w") as f:
        f.write(content)


def getDepends(dependencies, py_deps=None, dep_processor=None):
    depends_on = []
    
    # Process dependencies using the dependency processor
    if dep_processor:
        depends_on.extend(dep_processor.get_spack_dependencies())
    else:
        # Fallback for when no dependency processor is available
        depends_on.append('\tdepends_on("py-setuptools", type=("build"))\n')
        
        # Process regular dependencies
        for dep in dependencies:
            if dep.startswith("python"):
                # Handle Python version constraints
                depends_on.append(f'\tdepends_on("{dep}", type=("build", "run"))\n')
            else:
                # Handle regular package dependencies
                depends_on.append(f'\tdepends_on("{pyify(dep)}", type=("build", "run"))\n')
    
    # Add Python version dependencies for specific wheel versions
    if py_deps:
        for version, py_ver in py_deps.items():
            depends_on.append(f'\tdepends_on("python@{py_ver}", when="@{version}", type=("build", "run"))\n')
    
    return depends_on


def getTemplate(mode, package, description, homepage, classname, filename):
    if mode == "+":
        header = f"""# Copyright 2013-2023 Lawrence Livermore National Security, LLC and other
# Spack Project Developers. See the top-level COPYRIGHT file for details.
#
# SPDX-License-Identifier: (Apache-2.0 OR MIT)

from spack.package import *


class Py{classname}(PythonPackage):
    \"\"\"{description}\"\"\"
    
    homepage = "{homepage}"
    pypi = "{package}/{filename}" """
        footer = ""
    else:
        # Read existing package file
        with open("packages/" + pyify(package) + "/package.py", "r") as f:
            lines = f.readlines()
        
        # Find the first version line
        firstline = 0
        for i, line in enumerate(lines):
            line = line.replace("    ", "\t")  # Normalize indentation
            if "\tversion(" in line or "\turl =" in line or "\turls =" in line:
                firstline = i
                break
        
        # Find the first depends_on line
        lastline = len(lines)
        for i, line in enumerate(lines):
            line = line.replace("    ", "\t")  # Normalize indentation
            if "\tdepends_on(" in line:
                lastline = i
                break
        
        # Extract header and footer
        header = "".join(lines[:firstline]).strip()
        footer = "".join(lines[lastline:])
        
    return header, footer


def get(package_name, package_version, recurse=False, force=False):
    if pyify(package_name) in existingVersions and not force:
        print(f"	✴️ {pyify(package_name)} already exists in spack")
        return
    url = f"https://libraries.io/api/pypi/{package_name}/{package_version}/dependencies?api_key=ebb39aed4c41baa4c4e8a384a8775cd9"
    response = requests.get(url)
    if response.status_code != 200:
        print(f"	❌ Failed to retrieve package {package_name}")
        return

    json = response.json()
    pypiRequest = getPyPiJson(package_name)
    
    # Create a dependency processor for all dependencies
    dep_processor = DependencyProcessor()
    
    # Process Python version constraint
    python_version = pypiRequest["info"]["requires_python"]
    if python_version:
        # Process Python version constraint through the dependency processor
        dep_processor.process_dependency(f"python{python_version}")

    # Process dependencies from libraries.io API
    for i in json["dependencies"]:
        if str(i["platform"]).lower() == "pypi" and i["optional"] == False:
            dep_name = str(i["project_name"]).lower()
            # Add version constraint if available
            if "latest_stable" in i and i["latest_stable"]:
                dep_processor.process_dependency(f"{dep_name}=={i['latest_stable']}")
            else:
                dep_processor.process_dependency(dep_name)
            if recurse:
                get(str(i["project_name"]).lower(), i["latest_stable"], True, False)
        else:
            continue

    versions, filename, wheel_dep_processor = getVersions(pypiRequest["releases"])
    
    # Merge the wheel dependencies into our main processor
    for dep in wheel_dep_processor.regular_deps:
        dep_processor.process_dependency(dep.replace("py-", ""))
    
    for variant, deps in wheel_dep_processor.variant_deps.items():
        for dep in deps:
            dep_processor.process_dependency(dep.replace("py-", ""))
    
    header, footer = getTemplate(
        "+", package_name, json["description"], json["homepage"], getClassname(package_name), filename
    )
    
    # Add Python version dependencies for specific wheel versions
    python_deps = {}
    for version in versions:
        ver = re.search(r'version\("([^"]+)"', version)
        if ver:
            version = ver.group(1)
        if "-py" in version:
            _, py_ver = version.split("-py")
            # Fix: Handle cases where py_ver already contains dots (e.g., "3.7") or not (e.g., "37")
            if "." in py_ver:
                # py_ver is already formatted like "3.7"
                python_deps[version] = py_ver
            elif py_ver[0] == "3":
                # py_ver is like "37", convert to "3.7"
                py_ver = "3." + py_ver[1:]
                python_deps[version] = py_ver
            elif py_ver[0] == "2":
                # py_ver is like "27", convert to "2.7"
                py_ver = "2." + py_ver[1:]
                python_deps[version] = py_ver

    dependencies = getDepends([], python_deps, dep_processor)

    writeRecipe(header, footer, versions, dependencies, package_name, dep_processor.get_variants())


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: python3 PyPackageCreator.py [-f] package_name [...package_name]")
        exit()
    package_version = "latest"
    existingVersions = getExistingVersions()
    force = False
    for i in range(1, len(sys.argv)):
        if sys.argv[i] == "-f":
            force = True
            print("Forcing replacing of builtin Spack packages")
        else:
            print(f"Building recipes for {sys.argv[i]}...")
            get(sys.argv[i], package_version, True, force)
