#!/usr/bin/env python3

import ast
import json
import os
import re
import subprocess
import sys

import requests

spackBin = "spack"


def getExistingVersions():
    os.makedirs("packages", exist_ok=True)
    os.makedirs("libs", exist_ok=True)
    print("Fetching package versions, this could take a while...")
    stream = subprocess.run([spackBin, "list", "--format", "version_json", "py-*"], capture_output=True)
    decoded = stream.stdout.decode("utf-8").strip()
    try:
        builtin = json.loads(decoded)
    except:
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


def spackifyVersion(version):
    if version == None:
        return ""
    if "," in version:
        split = version.split(",")
        if len(split) != 2:
            return version
        if "<" in split[0]:
            split[0], split[1] = split[1], split[0]
        return split[0].replace(">=", "@") + ":" + split[1].replace("<", "")
    if ">=" in version:
        return version.replace(">=", "@") + ":"
    elif "<=" in version or "<" in version:
        return version.replace("<=", "@:").replace("<", "@:")
    else:
        return version.replace("=", "@", 1).replace("=", "")


def getVersions(versionList):
    versions = []
    filename = ""
    extradeps = {}
    for i in versionList.keys():
        if versionList[i] == []:
            continue

        # Group wheels by Python version
        python_version_wheels = {}
        sdist_info = None

        for release in versionList[i]:
            if release["yanked"]:
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
                depends = []
                for j in decoded:
                    if j.startswith("Requires-Dist:"):
                        depends.append(j.replace("Requires-Dist: ", "").replace(" ", ""))
                for j in depends:
                    if j in extradeps.keys():
                        extradeps[j].append(i)
                    else:
                        extradeps[j] = [i]
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
                depends = []
                for j in decoded:
                    if j.startswith("Requires-Dist:"):
                        depends.append(j.replace("Requires-Dist: ", "").replace(" ", ""))
                for j in depends:
                    if j in extradeps.keys():
                        extradeps[j].append(f"{i}{version_suffix}")
                    else:
                        extradeps[j] = [f"{i}{version_suffix}"]

        # If no wheels found, use sdist
        if not python_version_wheels and sdist_info:
            versions.append(f'\tversion("{i}", sha256="{sdist_info["digests"]["sha256"]}")\n')
            filename = sdist_info["filename"]

    return versions, filename, extradeps


def getClassname(package):
    classname = package.replace("-", ".").replace("_", ".").split(".")
    for i in range(len(classname)):
        classname[i] = classname[i].capitalize()
    return "".join(classname)


def writeRecipe(header, footer, versions, depends, package):
    os.makedirs("packages/" + pyify(package), exist_ok=True)
    content = f"""{header}

{"".join(versions)}
{"".join(depends)}{footer}"""
    # Replace all tabs with 4 spaces for PEP8 compliance
    content = content.replace("\t", "    ")
    with open("packages/" + pyify(package) + "/package.py", "w") as f:
        f.write(content)


def getDepends(dependencies, py_deps=None):
    depends_on = []
    depends_on.append('\tdepends_on("py-setuptools", type=("build"))\n')
    for i in dependencies:
        depends_on.append('\tdepends_on("' + pyify(i) + '", type=("build", "run"))\n')
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
    dependencies = []
    pypiRequest = getPyPiJson(package_name)
    python_version = spackifyVersion(pypiRequest["info"]["requires_python"])
    if python_version != "":
        dependencies.append("python" + python_version)

    for i in json["dependencies"]:
        if str(i["platform"]).lower() == "pypi" and i["optional"] == False:
            dependencies.append(str(i["project_name"]).lower())
            if recurse:
                get(str(i["project_name"]).lower(), i["latest_stable"], True, False)
        else:
            continue

    versions, filename, extradeps = getVersions(pypiRequest["releases"])
    header, footer = getTemplate(
        "+", package_name, json["description"], json["homepage"], getClassname(package_name), filename
    )
    
    # Merge wheel dependencies with Libraries.io dependencies
    for dep in extradeps.keys():
        if dep not in dependencies:
            dependencies.append(dep)
    
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

    dependencies = getDepends(dependencies, python_deps)

    if extradeps != {}:
        footer += f"\n# {str(extradeps)}"
    writeRecipe(header, footer, versions, dependencies, package_name)


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
