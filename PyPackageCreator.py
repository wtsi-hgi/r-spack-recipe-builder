import json
import os
import subprocess
import requests
import ast
# currently only lists all pypi dependencies for a given pypi package
spackBin = "spack/bin/spack"

def getExistingVersions():
	os.makedirs("packages", exist_ok=True)
	os.makedirs("libs", exist_ok=True)
	print("Fetching package versions, this could take a while...")
	stream = subprocess.run([spackBin, "list", "--format", "version_json", "py-*"], capture_output=True)
	decoded = stream.stdout.decode("utf-8").strip()
	builtin = json.loads(decoded)
	print("Versions successfully fetched!\n")
	packageVersions = {}
	for row in builtin:
		packageVersions[row["name"]] = row["versions"]
	return packageVersions

def addPythonAsDependency(package_name):
	request = requests.get(f"https://pypi.org/pypi/{package_name}/json")
	if request.status_code != 200:
		print(f"Failed to retrieve package {package_name}")
		exit()
	python_version = request.json()["info"]["requires_python"]
	dependencies.append("python"+python_version)

dependencies = []
def get_package_dependencies(package_name, package_version, recurse=False):
	if pyify(package_name) in existingVersions:
		return 
	url = f"https://libraries.io/api/pypi/{package_name}/{package_version}/dependencies?api_key=ebb39aed4c41baa4c4e8a384a8775cd9"
	response = requests.get(url)
	if response.status_code != 200:
		return None
	
	json = response.json()
	thisPackagesDependencies = []
	for i in json["dependencies"]:
		if str(i["project_name"]).lower() not in dependencies and str(i["platform"]).lower() == "pypi" and i["optional"] == False:
			dependencies.append(str(i["project_name"]).lower())
			thisPackagesDependencies.append(str(i["project_name"]).lower())
			# print(i["project_name"])
			if recurse:
				get_package_dependencies(str(i["project_name"]).lower(), i["latest_stable"], True)
		else:
			continue
	
	header, footer = getTemplate("+", package_name, json["description"], json["homepage"], getClassname(package_name))
	dependencies = getDepends(thisPackagesDependencies)

def pyify(package):
	return "py-" + package.lower().replace(".","-")

def getClassname(package):
	classname = package.split(".")
	for i in range(len(classname)):
		classname[i] = classname[i].capitalize()
	return "".join(classname)

def writeRecipe(header, footer, versions, depends, package):
	os.makedirs("packages/" + pyify(package), exist_ok=True)
	f = open("packages/" + pyify(package) + "/package.py", "w")
	f.write(f"""{header}

{"".join(versions)}
{"".join(depends)}{footer}""")
	f.close()

def getDepends(dependencies):
	depends_on = []
	for i in dependencies:
		depends_on.append("\tdepends_on(\"" + pyify(i) + "\", type=(\"build\", \"run\"))\n")
	return depends_on

def getTemplate(mode, package, description, homepage, classname):
	if mode == "+":
		header = f"""# Copyright 2013-2023 Lawrence Livermore National Security, LLC and other
# Spack Project Developers. See the top-level COPYRIGHT file for details.
#
# SPDX-License-Identifier: (Apache-2.0 OR MIT)

from spack.package import *


class Py{classname}(PythonPackage):
	\"\"\"{package}

	{description}
	\"\"\"
	
	homepage = "{homepage}"
	pypi = "{package}" """
		footer = ""
	else:
		with open("packages/" + pyify(package) + "/package.py", "r") as f:
			lines = ast.parse(f.read())
		firstline = 0
		for node in ast.walk(lines):
			if isinstance(node, ast.Assign) or isinstance(node, ast.Call):
				if (isinstance(node.value.func, ast.Name) and node.value.func.id == "version") or (isinstance(node.value.func, ast.Attribute) and (node.value.func.attr == "url" or node.value.func.attr == "urls")):
					firstline = i
					break
		header = "".join(lines[:firstline]).strip()
		lastline = len(lines)
		for node in ast.walk(lines):
			if isinstance(node, ast.Assign) or isinstance(node, ast.Call):
				if (isinstance(node.value.func, ast.Name) and node.value.func.id == "depends_on") or (isinstance(node.value.func, ast.Attribute) and (node.value.func.attr == "depends_on")):
					lastline = i + 1
					break
		for i in range(len(lines)):
			lines[i] = lines[i].replace("    ", "\t")
			if "\tversion(" in lines[i] or "\turl =" in lines[i] or "\turls =" in lines[i]:
				firstline = i
				break
		header = "".join(lines[:firstline]).strip()
		lastline = len(lines)
		for j in range(len(lines)):
			lines[j] = lines[j].replace("    ", "\t")
			if "\tdepends_on(" in lines[j] or "\tversion(" in lines[j]:
				ast.parse()
				lastline = j + 1
				break
		footer = "".join(lines[lastline:])
	return header, footer

package_name = str(input("Enter package name: "))
package_version = "latest"

addPythonAsDependency(package_name)
existingVersions = getExistingVersions()

get_package_dependencies(package_name, package_version)

# prints the dependencies, will be replaced by a function that writes to a file
if dependencies != None:
	print(f"Dependencies for {package_name}:")
	# print(dependencies)
else:
	print(f"Failed to retrieve dependencies for {package_name}")

for i in dependencies:
	if "python>" in i or "python<" in i:
		print(f"	🐍 {i} required")
		continue
	pkg = pyify(i)
	if pkg in existingVersions:
		print(f"	✅ {pkg} already exists in spack")
	else:
		print(f"	❌ {pkg} does not exist in spack")
		# create a package for the missing package